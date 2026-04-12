package admission

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/projecteru2/core/log"
	admissionv1 "k8s.io/api/admission/v1"
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
		return commonadmission.Allow()
	}

	var cs cocoonv1.CocoonSet
	if err := json.Unmarshal(req.Object.Raw, &cs); err != nil {
		logger.Warnf(ctx, "decode cocoonset %s/%s: %v", req.Namespace, req.Name, err)
		return commonadmission.Deny(fmt.Sprintf("decode CocoonSet: %v", err))
	}

	if errs := validateCocoonSetSpec(&cs); len(errs) > 0 {
		msg := "cocoon-webhook: invalid CocoonSet spec: " + strings.Join(errs, "; ")
		logger.Warnf(ctx, "validate %s/%s DENY: %s", req.Namespace, req.Name, msg)
		metrics.RecordAdmission(metrics.HandlerValidateCocoonSet, metrics.DecisionDeny)
		return commonadmission.Deny(msg)
	}
	metrics.RecordAdmission(metrics.HandlerValidateCocoonSet, metrics.DecisionAllow)
	return commonadmission.Allow()
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
	if cs.Spec.Agent.OS != "" && !cs.Spec.Agent.OS.IsValid() {
		errs = append(errs, fmt.Sprintf("spec.agent.os must be linux or windows, got %q", cs.Spec.Agent.OS))
	}

	seen := map[string]bool{}
	for i, tb := range cs.Spec.Toolboxes {
		path := fmt.Sprintf("spec.toolboxes[%d]", i)
		if tb.Name == "" {
			errs = append(errs, path+".name is required")
			continue
		}
		if vErrs := validation.IsDNS1123Label(tb.Name); len(vErrs) > 0 {
			errs = append(errs, fmt.Sprintf("%s.name %q must match RFC 1123 label: %s", path, tb.Name, strings.Join(vErrs, "; ")))
		}
		if seen[tb.Name] {
			errs = append(errs, fmt.Sprintf("%s.name %q duplicates an earlier toolbox", path, tb.Name))
		}
		seen[tb.Name] = true

		if tb.Mode != "" && !tb.Mode.IsValid() {
			errs = append(errs, fmt.Sprintf("%s.mode must be run, clone, or static, got %q", path, tb.Mode))
		}
		if tb.OS != "" && !tb.OS.IsValid() {
			errs = append(errs, fmt.Sprintf("%s.os must be linux, windows, or android, got %q", path, tb.OS))
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
	}

	if cs.Spec.SnapshotPolicy != "" && !cs.Spec.SnapshotPolicy.IsValid() {
		errs = append(errs, fmt.Sprintf("spec.snapshotPolicy must be always, main-only, or never, got %q", cs.Spec.SnapshotPolicy))
	}

	return errs
}
