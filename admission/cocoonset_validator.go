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
	commonadmission "github.com/cocoonstack/cocoon-common/k8s/admission"
	"github.com/cocoonstack/cocoon-webhook/metrics"
)

// validateCocoonSet enforces cross-field business rules that the CRD OpenAPI schema cannot express.
func (s *Server) validateCocoonSet(ctx context.Context, review *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	logger := log.WithFunc("validateCocoonSet")
	req := review.Request

	if req.Operation != admissionv1.Create && req.Operation != admissionv1.Update {
		metrics.RecordAdmission(metrics.HandlerValidateCocoonSet, metrics.ResultSkipped, metrics.ReasonOperation)
		return commonadmission.Allow()
	}

	var cs cocoonv1.CocoonSet
	if err := json.Unmarshal(req.Object.Raw, &cs); err != nil {
		logger.Errorf(ctx, err, "decode cocoonset %s/%s", req.Namespace, req.Name)
		metrics.RecordAdmission(metrics.HandlerValidateCocoonSet, metrics.ResultError, metrics.ReasonDecode)
		return commonadmission.Deny(fmt.Sprintf("decode CocoonSet: %v", err))
	}

	// On UPDATE, skip spec validation when only metadata changed (e.g.
	// finalizer patches during deletion). Without this, an invalid CR
	// that slipped past an older webhook becomes undeletable because the
	// operator's finalizer-removal patch is denied.
	if req.Operation == admissionv1.Update && req.OldObject.Raw != nil {
		var old cocoonv1.CocoonSet
		if err := json.Unmarshal(req.OldObject.Raw, &old); err != nil {
			logger.Warnf(ctx, "decode old cocoonset %s/%s: %v", req.Namespace, req.Name, err)
		} else if specEqual(&cs, &old) {
			metrics.RecordAdmission(metrics.HandlerValidateCocoonSet, metrics.ResultSkipped, metrics.ReasonNoChange)
			return commonadmission.Allow()
		}
	}

	if errs := validateCocoonSetSpec(&cs); len(errs) > 0 {
		msg := "cocoon-webhook: invalid CocoonSet spec: " + strings.Join(errs, "; ")
		logger.Warnf(ctx, "validate %s/%s DENY: %s", req.Namespace, req.Name, msg)
		metrics.RecordAdmission(metrics.HandlerValidateCocoonSet, metrics.ResultDeny, "")
		return commonadmission.Deny(msg)
	}
	metrics.RecordAdmission(metrics.HandlerValidateCocoonSet, metrics.ResultAllow, "")
	return commonadmission.Allow()
}

func validateCocoonSetSpec(cs *cocoonv1.CocoonSet) []string {
	errs := make([]string, 0, 16)

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
	if msg := firecrackerModeError("spec.agent", cs.Spec.Agent.Backend, string(cs.Spec.Agent.Mode.Default())); msg != "" {
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
		if tb.Mode == cocoonv1.ToolboxModeStatic {
			if tb.StaticIP == "" {
				errs = append(errs, path+".staticIP is required when mode=static")
			}
			if tb.StaticVMID == "" {
				errs = append(errs, path+".staticVMID is required when mode=static")
			}
		} else if tb.Image == "" {
			errs = append(errs, path+".image is required when mode is run or clone")
		}

		// ConnType applies to every toolbox — clients still reach static
		// toolboxes via SSH/RDP/VNC/ADB, so the enum must be validated even
		// when the rest of the VM options are skipped.
		if err := validateConnType(path, tb.ConnType); err != "" {
			errs = append(errs, err)
		}

		// Static toolboxes run no hypervisor locally; skip backend/image consistency checks.
		if tb.Mode != cocoonv1.ToolboxModeStatic {
			errs = append(errs, validateVMOptions(path, tb.VMOptions, tb.Image)...)
			if tb.Backend.Default() != agentBackend {
				errs = append(errs, fmt.Sprintf("%s.backend %q must match spec.agent.backend %q", path, tb.Backend.Default(), agentBackend))
			}
			if msg := firecrackerModeError(path, tb.Backend, string(tb.Mode.Default())); msg != "" {
				errs = append(errs, msg)
			}
		}
	}

	if cs.Spec.SnapshotPolicy != "" && !cs.Spec.SnapshotPolicy.IsValid() {
		errs = append(errs, fmt.Sprintf("spec.snapshotPolicy must be always, main-only, or never, got %q", cs.Spec.SnapshotPolicy))
	}

	return errs
}

// specEqual reports whether two CocoonSets have identical specs.
func specEqual(a, b *cocoonv1.CocoonSet) bool {
	return equality.Semantic.DeepEqual(a.Spec, b.Spec)
}

// validateVMOptions validates shared VM knobs (OS / ConnType / Backend) plus
// firecracker-specific image constraints. path is the JSON path prefix used
// when reporting errors.
func validateVMOptions(path string, opts cocoonv1.VMOptions, image string) []string {
	var errs []string

	if opts.OS != "" && !opts.OS.IsValid() {
		errs = append(errs, fmt.Sprintf("%s.os must be linux, windows, or android, got %q", path, opts.OS))
	}
	if err := validateConnType(path, opts.ConnType); err != "" {
		errs = append(errs, err)
	}
	if opts.Backend != "" && !opts.Backend.IsValid() {
		errs = append(errs, fmt.Sprintf("%s.backend must be cloud-hypervisor or firecracker, got %q", path, opts.Backend))
	}

	// Firecracker uses direct kernel boot from OCI layers — it cannot boot
	// Windows, and cannot consume cloudimg URLs (those are full qcow2 images
	// that require UEFI/BIOS firmware).
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
// when unset or valid. Called outside validateVMOptions so static toolboxes
// can still validate the field.
func validateConnType(path string, ct cocoonv1.ConnType) string {
	if ct == "" || ct.IsValid() {
		return ""
	}
	return fmt.Sprintf("%s.connType must be ssh, rdp, vnc, or adb, got %q", path, ct)
}

// firecrackerModeError returns the error message when a firecracker backend
// is paired with mode != run. Firecracker restores the memory snapshot on
// clone, freezing guest network state (MAC + IP) — cross-node clones land
// with an unreachable IP from the source's DHCP pool. CH hot-swaps the NIC
// to work around this; FC has no equivalent. The mode arg accepts either
// AgentMode or ToolboxMode as a string since both enums share the "run"
// literal.
func firecrackerModeError(path string, backend cocoonv1.Backend, mode string) string {
	if backend.Default() != cocoonv1.BackendFirecracker {
		return ""
	}
	if mode == string(cocoonv1.AgentModeRun) {
		return ""
	}
	return fmt.Sprintf("%s: firecracker does not support %s mode, use mode=run instead", path, mode)
}
