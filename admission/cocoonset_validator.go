package admission

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/projecteru2/core/log"
	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/util/validation"

	cocoonv1 "github.com/cocoonstack/cocoon-common/apis/v1"
	"github.com/cocoonstack/cocoon-common/meta"
	"github.com/cocoonstack/cocoon-common/ociutil"
	"github.com/cocoonstack/cocoon-webhook/metrics"
)

const (
	maxVMNameLength        = 63
	maxManagedVMNameLength = maxVMNameLength - len(meta.HibernateImportSuffix)
)

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
	return recordAllow(metrics.HandlerValidateCocoonSet, metrics.ResultAllow, "")
}

func validateCocoonSetSpec(cs *cocoonv1.CocoonSet) []string {
	var errs []string

	vmName := meta.VMNameForDeployment(cs.Namespace, cs.Name, max(0, int(cs.Spec.Agent.Replicas)))
	errs = appendMsgs(errs, vmNameLengthError("spec.agent", vmName, cs.Spec.Agent.OS))
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
	errs = appendMsgs(errs, firecrackerModeError("spec.agent", cs.Spec.Agent.Backend, agentMode), cloneImageError("spec.agent", agentMode, cs.Spec.Agent.Image))

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
			errs = appendMsgs(errs, validateConnType(path, tb.ConnType))
			continue
		}

		errs = appendMsgs(errs, vmNameLengthError(path, meta.VMNameForPod(cs.Namespace, meta.ToolboxPodName(cs.Name, tb.Name)), tb.OS))
		if tb.Image == "" {
			errs = append(errs, path+".image is required when mode is run or clone")
		}
		errs = append(errs, validateVMOptions(path, tb.VMOptions, tb.Image)...)
		if tb.Backend.Default() != agentBackend {
			errs = append(errs, fmt.Sprintf("%s.backend %q must match spec.agent.backend %q", path, tb.Backend.Default(), agentBackend))
		}
		tbMode := string(tb.Mode.Default())
		errs = appendMsgs(errs, firecrackerModeError(path, tb.Backend, tbMode), cloneImageError(path, tbMode, tb.Image))
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
	errs = appendMsgs(errs, validateConnType(path, opts.ConnType))
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

func appendMsgs(errs []string, msgs ...string) []string {
	for _, msg := range msgs {
		if msg != "" {
			errs = append(errs, msg)
		}
	}
	return errs
}
