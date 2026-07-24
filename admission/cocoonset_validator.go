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
	"github.com/cocoonstack/cocoon-common/ociutil"
	"github.com/cocoonstack/cocoon-webhook/metrics"
)

// validateCocoonSet enforces cross-field business rules that the CRD OpenAPI schema cannot express.
func (s *Server) validateCocoonSet(ctx context.Context, review *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	logger := log.WithFunc("validateCocoonSet")
	req := review.Request

	if req.Operation != admissionv1.Create && req.Operation != admissionv1.Update {
		return recordAllow(metrics.HandlerValidateCocoonSet, metrics.ResultSkipped, metrics.ReasonOperation)
	}

	var cs cocoonv1.CocoonSet
	if err := json.Unmarshal(req.Object.Raw, &cs); err != nil {
		logger.Errorf(ctx, err, "decode cocoonset %s/%s", req.Namespace, req.Name)
		return recordDeny(metrics.HandlerValidateCocoonSet, metrics.ResultError, metrics.ReasonDecode, fmt.Sprintf("decode CocoonSet: %v", err))
	}

	// Allow spec-unchanged UPDATEs (finalizer/metadata patches): an invalid
	// CR that predates stricter validation must stay deletable.
	if req.Operation == admissionv1.Update && req.OldObject.Raw != nil {
		var old cocoonv1.CocoonSet
		if err := json.Unmarshal(req.OldObject.Raw, &old); err != nil {
			logger.Warnf(ctx, "decode old cocoonset %s/%s: %v", req.Namespace, req.Name, err)
		} else if specEqual(&cs, &old) {
			return recordAllow(metrics.HandlerValidateCocoonSet, metrics.ResultSkipped, metrics.ReasonNoChange)
		}
	}

	if errs := validateCocoonSetSpec(&cs); len(errs) > 0 {
		msg := "cocoon-webhook: invalid CocoonSet spec: " + strings.Join(errs, "; ")
		logger.Warnf(ctx, "validate %s/%s DENY: %s", req.Namespace, req.Name, msg)
		return recordDeny(metrics.HandlerValidateCocoonSet, metrics.ResultDeny, "", msg)
	}
	return recordAllow(metrics.HandlerValidateCocoonSet, metrics.ResultAllow, "")
}

func validateCocoonSetSpec(cs *cocoonv1.CocoonSet) []string {
	var errs []string

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

		// Static toolboxes run no hypervisor locally: skip backend/image checks
		// but keep ConnType — clients still reach them via SSH/RDP/VNC/ADB.
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

func specEqual(a, b *cocoonv1.CocoonSet) bool {
	return equality.Semantic.DeepEqual(a.Spec, b.Spec)
}

// validateVMOptions validates shared VM knobs plus firecracker image
// constraints; path is the JSON path prefix for reported errors.
func validateVMOptions(path string, opts cocoonv1.VMOptions, image string) []string {
	var errs []string

	if opts.OS != "" && !opts.OS.IsValid() {
		errs = append(errs, fmt.Sprintf("%s.os must be linux, windows, or android, got %q", path, opts.OS))
	}
	if msg := validateConnType(path, opts.ConnType); msg != "" {
		errs = append(errs, msg)
	}
	if opts.Backend != "" && !opts.Backend.IsValid() {
		errs = append(errs, fmt.Sprintf("%s.backend must be cloud-hypervisor or firecracker, got %q", path, opts.Backend))
	}

	// Firecracker direct-boots kernels from OCI layers: no Windows, no
	// cloudimg qcow2 URLs (those need UEFI/BIOS firmware).
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

// validateConnType returns the error message for an invalid ConnType, empty
// when unset or valid; standalone so static toolboxes can validate it alone.
func validateConnType(path string, ct cocoonv1.ConnType) string {
	if ct == "" || ct.IsValid() {
		return ""
	}
	return fmt.Sprintf("%s.connType must be ssh, rdp, vnc, or adb, got %q", path, ct)
}

// cloneImageError rejects clone-mode images ParseRef cannot split (registry
// ports, digests): the snapshot pull path joins repo[:tag] under a fixed
// registry base with no external-ref fallback. mode accepts AgentMode or
// ToolboxMode strings.
func cloneImageError(path, mode, image string) string {
	if mode != string(cocoonv1.AgentModeClone) || image == "" || ociutil.IsRelativeRef(image) {
		return ""
	}
	return fmt.Sprintf("%s.image %q must be repo[:tag] when mode is clone (no registry port or digest)", path, image)
}

// firecrackerModeError rejects firecracker paired with mode != run: FC clone
// restores freeze guest MAC+IP, landing clones on an unreachable DHCP lease
// (CH hot-swaps the NIC instead). mode accepts AgentMode or ToolboxMode strings.
func firecrackerModeError(path string, backend cocoonv1.Backend, mode string) string {
	if backend.Default() != cocoonv1.BackendFirecracker {
		return ""
	}
	if mode == string(cocoonv1.AgentModeRun) {
		return ""
	}
	return fmt.Sprintf("%s: firecracker does not support %s mode, use mode=run instead", path, mode)
}
