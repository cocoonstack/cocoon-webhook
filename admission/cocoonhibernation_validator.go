package admission

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/projecteru2/core/log"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cocoonv1 "github.com/cocoonstack/cocoon-common/apis/v1"
	commonk8s "github.com/cocoonstack/cocoon-common/k8s"
	commonadmission "github.com/cocoonstack/cocoon-common/k8s/admission"
	"github.com/cocoonstack/cocoon-webhook/metrics"
)

var cocoonHibernationGVR = cocoonv1.GroupVersion.WithResource("cocoonhibernations")

// validateCocoonHibernation rejects a CREATE whose pod already has a live
// CocoonHibernation — two CRs with opposing desires never converge. CREATE is
// the only gate needed: retargets are blocked by the CRD's CEL rule.
func (s *Server) validateCocoonHibernation(ctx context.Context, review *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	logger := log.WithFunc("validateCocoonHibernation")
	req := review.Request

	if req.Operation != admissionv1.Create {
		metrics.RecordAdmission(metrics.HandlerValidateHibernation, metrics.ResultSkipped, metrics.ReasonOperation)
		return commonadmission.Allow()
	}

	var hib cocoonv1.CocoonHibernation
	if err := json.Unmarshal(req.Object.Raw, &hib); err != nil {
		logger.Errorf(ctx, err, "decode cocoonhibernation %s/%s", req.Namespace, req.Name)
		metrics.RecordAdmission(metrics.HandlerValidateHibernation, metrics.ResultError, metrics.ReasonDecode)
		return commonadmission.Deny(fmt.Sprintf("decode CocoonHibernation: %v", err))
	}

	// Fail closed on list errors: admitting a possible duplicate risks the
	// non-converging flip-flop this validator exists to prevent.
	existing, err := s.dyn.Resource(cocoonHibernationGVR).Namespace(req.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		logger.Errorf(ctx, err, "list cocoonhibernations in %s", req.Namespace)
		metrics.RecordAdmission(metrics.HandlerValidateHibernation, metrics.ResultError, metrics.ReasonList)
		return commonadmission.Deny(fmt.Sprintf("cocoon-webhook: cannot verify podRef uniqueness: %v", err))
	}
	for i := range existing.Items {
		other, err := commonk8s.DecodeUnstructured[cocoonv1.CocoonHibernation](&existing.Items[i])
		if err != nil {
			logger.Warnf(ctx, "decode existing cocoonhibernation %s/%s: %v", req.Namespace, existing.Items[i].GetName(), err)
			continue
		}
		if other.DeletionTimestamp == nil && other.Spec.PodRef.Name == hib.Spec.PodRef.Name {
			msg := fmt.Sprintf("cocoon-webhook: pod %q already has a live CocoonHibernation %q; flip its spec.desire instead of creating a second CR",
				hib.Spec.PodRef.Name, other.Name)
			logger.Warnf(ctx, "validate %s/%s DENY: %s", req.Namespace, req.Name, msg)
			metrics.RecordAdmission(metrics.HandlerValidateHibernation, metrics.ResultDeny, "")
			return commonadmission.Deny(msg)
		}
	}
	metrics.RecordAdmission(metrics.HandlerValidateHibernation, metrics.ResultAllow, "")
	return commonadmission.Allow()
}
