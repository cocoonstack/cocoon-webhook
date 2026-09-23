package admission

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/projecteru2/core/log"
	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"

	cocoonv1 "github.com/cocoonstack/cocoon-common/apis/v1"
	"github.com/cocoonstack/cocoon-common/meta"
	"github.com/cocoonstack/cocoon-common/ociutil"
	"github.com/cocoonstack/cocoon-webhook/metrics"
)

const (
	maxVMNameLength        = 63
	maxManagedVMNameLength = maxVMNameLength - len("-hibernate-import")
)

var cocoonSetGVR = cocoonv1.GroupVersion.WithResource("cocoonsets")

func (s *Server) validateCocoonSet(ctx context.Context, review *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	logger := log.WithFunc("admission.validateCocoonSet")
	req := review.Request

	if req.Operation != admissionv1.Create && req.Operation != admissionv1.Update {
		return recordAllow(metrics.HandlerValidateCocoonSet, metrics.ResultSkipped, metrics.ReasonOperation)
	}

	var cs cocoonv1.CocoonSet
	if resp := decodeOrDeny(ctx, logger, metrics.HandlerValidateCocoonSet, "CocoonSet", req, &cs); resp != nil {
		return resp
	}

	// Older invalid CRs must remain deletable through finalizer updates.
	if req.Operation == admissionv1.Update && req.OldObject.Raw != nil {
		var old cocoonv1.CocoonSet
		if err := json.Unmarshal(req.OldObject.Raw, &old); err != nil {
			logger.Warnf(ctx, "decode old cocoonset %s/%s: %v", req.Namespace, req.Name, err)
		} else if equality.Semantic.DeepEqual(cs.Spec, old.Spec) {
			return recordAllow(metrics.HandlerValidateCocoonSet, metrics.ResultSkipped, metrics.ReasonNoChange)
		}
	}

	if errs := validateCocoonSetSpec(&cs); len(errs) > 0 {
		return denyf(ctx, logger, metrics.HandlerValidateCocoonSet, req, "cocoon-webhook: invalid CocoonSet spec: "+strings.Join(errs, "; "))
	}
	if resp := s.denyVMNameCollision(ctx, logger, req, &cs); resp != nil {
		return resp
	}
	return recordAllow(metrics.HandlerValidateCocoonSet, metrics.ResultAllow, "")
}

// denyVMNameCollision lists every namespace: VM names join namespace and name with a plain hyphen, so team-a/dev and team/a-dev derive the same VM.
func (s *Server) denyVMNameCollision(ctx context.Context, logger *log.Fields, req *admissionv1.AdmissionRequest, cs *cocoonv1.CocoonSet) *admissionv1.AdmissionResponse {
	existing, err := s.dyn.Resource(cocoonSetGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		logger.Errorf(ctx, err, "list cocoonsets")
		return recordDeny(metrics.HandlerValidateCocoonSet, metrics.ResultError, metrics.ReasonList, fmt.Sprintf("cocoon-webhook: cannot verify VM name uniqueness: %v", err))
	}
	mine := derivedVMNames(cs)
	for i := range existing.Items {
		var other cocoonv1.CocoonSet
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(existing.Items[i].Object, &other); err != nil {
			logger.Errorf(ctx, err, "decode cocoonset %s/%s", existing.Items[i].GetNamespace(), existing.Items[i].GetName())
			return recordDeny(metrics.HandlerValidateCocoonSet, metrics.ResultError, metrics.ReasonDecode, fmt.Sprintf("cocoon-webhook: cannot verify VM name uniqueness: %v", err))
		}
		if other.Namespace == cs.Namespace && other.Name == cs.Name {
			continue
		}
		theirs := derivedVMNames(&other)
		hit := slices.IndexFunc(mine, func(name string) bool { return slices.Contains(theirs, name) })
		if hit >= 0 {
			return denyf(ctx, logger, metrics.HandlerValidateCocoonSet, req, fmt.Sprintf("cocoon-webhook: derived VM name %q collides with CocoonSet %s/%s", mine[hit], other.Namespace, other.Name))
		}
	}
	return nil
}

func validateCocoonSetSpec(cs *cocoonv1.CocoonSet) []string {
	var errs []string

	vmName := meta.VMNameForDeployment(cs.Namespace, cs.Name, max(0, int(cs.Spec.Agent.Replicas)))
	if msg := vmNameLengthError("spec.agent", vmName, cs.Spec.Agent.OS); msg != "" {
		errs = append(errs, msg)
	}
	if cs.Spec.Agent.Image == "" {
		errs = append(errs, "spec.agent.image is required")
	}
	if cs.Spec.Agent.Replicas < 0 {
		errs = append(errs, fmt.Sprintf("spec.agent.replicas must be >= 0, got %d", cs.Spec.Agent.Replicas))
	}
	if cs.Spec.Agent.Mode != "" && !cs.Spec.Agent.Mode.IsValid() {
		errs = append(errs, fmt.Sprintf("spec.agent.mode must be clone or run, got %q", cs.Spec.Agent.Mode))
	}
	errs = append(errs, validateVMOptions("spec.agent", cs.Spec.Agent.VMOptions, cs.Spec.Agent.Image)...)
	agentMode := string(cs.Spec.Agent.Mode.Default())
	if msg := firecrackerModeError("spec.agent", cs.Spec.Agent.Backend, agentMode); msg != "" {
		errs = append(errs, msg)
	}
	if msg := cloneImageError("spec.agent", agentMode, cs.Spec.Agent.Image); msg != "" {
		errs = append(errs, msg)
	}

	seen := map[string]struct{}{}
	agentBackend := cs.Spec.Agent.Backend.Default()
	for i, tb := range cs.Spec.Toolboxes {
		path := fmt.Sprintf("spec.toolboxes[%d]", i)
		if tb.Name == "" {
			errs = append(errs, path+".name is required")
			continue
		}
		if vErrs := validation.IsDNS1123Label(tb.Name); len(vErrs) > 0 {
			errs = append(errs, fmt.Sprintf("%s.name %q must match RFC 1123 label: %s", path, tb.Name, strings.Join(vErrs, "; ")))
		}
		if _, err := strconv.Atoi(tb.Name); err == nil {
			errs = append(errs, fmt.Sprintf("%s.name %q must not be purely numeric (conflicts with agent slot naming)", path, tb.Name))
		}
		if _, ok := seen[tb.Name]; ok {
			errs = append(errs, fmt.Sprintf("%s.name %q duplicates an earlier toolbox", path, tb.Name))
		}
		seen[tb.Name] = struct{}{}

		if tb.Mode != "" && !tb.Mode.IsValid() {
			errs = append(errs, fmt.Sprintf("%s.mode must be run, clone, or static, got %q", path, tb.Mode))
		}

		// Static toolboxes use external VMs but still expose a connection protocol.
		if tb.Mode == cocoonv1.ToolboxModeStatic {
			if tb.StaticIP == "" {
				errs = append(errs, path+".staticIP is required when mode=static")
			}
			if tb.StaticVMID == "" {
				errs = append(errs, path+".staticVMID is required when mode=static")
			}
			if msg := validateConnType(path, tb.ConnType); msg != "" {
				errs = append(errs, msg)
			}
			continue
		}

		vmName := meta.VMNameForPod(cs.Namespace, cs.Name+"-"+tb.Name)
		if msg := vmNameLengthError(path, vmName, tb.OS); msg != "" {
			errs = append(errs, msg)
		}
		if tb.Image == "" {
			errs = append(errs, path+".image is required when mode is run or clone")
		}
		errs = append(errs, validateVMOptions(path, tb.VMOptions, tb.Image)...)
		if tb.Backend.Default() != agentBackend {
			errs = append(errs, fmt.Sprintf("%s.backend %q must match spec.agent.backend %q", path, tb.Backend.Default(), agentBackend))
		}
		tbMode := string(tb.Mode.Default())
		if msg := firecrackerModeError(path, tb.Backend, tbMode); msg != "" {
			errs = append(errs, msg)
		}
		if msg := cloneImageError(path, tbMode, tb.Image); msg != "" {
			errs = append(errs, msg)
		}
	}

	if cs.Spec.SnapshotPolicy != "" && !cs.Spec.SnapshotPolicy.IsValid() {
		errs = append(errs, fmt.Sprintf("spec.snapshotPolicy must be always, main-only, or never, got %q", cs.Spec.SnapshotPolicy))
	}
	if cs.Spec.HibernatePolicy != "" && !cs.Spec.HibernatePolicy.IsValid() {
		errs = append(errs, fmt.Sprintf("spec.hibernatePolicy must be retain or release, got %q", cs.Spec.HibernatePolicy))
	}

	return errs
}

func vmNameLengthError(path, name string, os cocoonv1.OSType) string {
	if os.Default() == cocoonv1.OSMacos {
		if len(name) <= maxVMNameLength {
			return ""
		}
		return fmt.Sprintf("%s derives VM name %q (%d characters); maximum is %d", path, name, len(name), maxVMNameLength)
	}
	if len(name) <= maxManagedVMNameLength {
		return ""
	}
	return fmt.Sprintf("%s derives VM name %q (%d characters); maximum is %d to reserve the hibernate import suffix", path, name, len(name), maxManagedVMNameLength)
}

func validateVMOptions(path string, opts cocoonv1.VMOptions, image string) []string {
	var errs []string

	if opts.OS != "" && !opts.OS.IsValid() {
		errs = append(errs, fmt.Sprintf("%s.os must be linux, windows, android, or macos, got %q", path, opts.OS))
	}
	if msg := validateConnType(path, opts.ConnType); msg != "" {
		errs = append(errs, msg)
	}
	if opts.Backend != "" && !opts.Backend.IsValid() {
		errs = append(errs, fmt.Sprintf("%s.backend must be cloud-hypervisor or firecracker, got %q", path, opts.Backend))
	}

	// Firecracker direct-boots kernels without UEFI or BIOS firmware.
	if opts.Backend.Default() == cocoonv1.BackendFirecracker {
		if opts.OS.Default() == cocoonv1.OSWindows {
			errs = append(errs, fmt.Sprintf("%s: firecracker does not support Windows guests", path))
		}
		if strings.HasPrefix(image, "http://") || strings.HasPrefix(image, "https://") {
			errs = append(errs, fmt.Sprintf("%s: firecracker requires an OCI image, cloudimg URLs are not supported (got %q)", path, image))
		}
	}

	return errs
}

func validateConnType(path string, ct cocoonv1.ConnType) string {
	if ct == "" || ct.IsValid() {
		return ""
	}
	return fmt.Sprintf("%s.connType must be ssh, rdp, vnc, or adb, got %q", path, ct)
}

// cloneImageError rejects clone-mode images ParseRef cannot split: the snapshot pull joins repo[:tag] under a fixed registry base.
func cloneImageError(path, mode, image string) string {
	if mode != string(cocoonv1.AgentModeClone) || image == "" || ociutil.IsRelativeRef(image) {
		return ""
	}
	return fmt.Sprintf("%s.image %q must be repo[:tag] when mode is clone (no registry port or digest)", path, image)
}

// firecrackerModeError rejects firecracker with mode != run: an FC restore freezes the guest MAC+IP, so clones land on a dead lease.
func firecrackerModeError(path string, backend cocoonv1.Backend, mode string) string {
	if backend.Default() != cocoonv1.BackendFirecracker || mode == string(cocoonv1.AgentModeRun) {
		return ""
	}
	return fmt.Sprintf("%s: firecracker does not support %s mode, use mode=run instead", path, mode)
}

func derivedVMNames(cs *cocoonv1.CocoonSet) []string {
	names := make([]string, 0, int(cs.Spec.Agent.Replicas)+1+len(cs.Spec.Toolboxes))
	for slot := range max(0, int(cs.Spec.Agent.Replicas)) + 1 {
		names = append(names, meta.VMNameForDeployment(cs.Namespace, cs.Name, slot))
	}
	for _, tb := range cs.Spec.Toolboxes {
		names = append(names, meta.VMNameForPod(cs.Namespace, cs.Name+"-"+tb.Name))
	}
	return names
}
