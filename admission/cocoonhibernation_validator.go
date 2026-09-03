package admission

import (
	"context"
	"fmt"

	"github.com/projecteru2/core/log"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	cocoonv1 "github.com/cocoonstack/cocoon-common/apis/v1"
	"github.com/cocoonstack/cocoon-webhook/metrics"
)

var cocoonHibernationGVR = cocoonv1.GroupVersion.WithResource("cocoonhibernations")

// validateCocoonHibernation requires metadata.name == spec.podRef.name so duplicates collide on name uniqueness; the LIST catches pre-rule names.
func (s *Server) validateCocoonHibernation(ctx context.Context, review *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	logger := log.WithFunc("validateCocoonHibernation")
	req := review.Request

	if req.Operation != admissionv1.Create {
		return recordAllow(metrics.HandlerValidateHibernation, metrics.ResultSkipped, metrics.ReasonOperation)
	}

	var hib cocoonv1.CocoonHibernation
	if resp := decodeOrDeny(ctx, logger, metrics.HandlerValidateHibernation, "CocoonHibernation", req, &hib); resp != nil {
		return resp
	}

	if !hib.Spec.Desire.IsValid() {
		return denyf(ctx, logger, metrics.HandlerValidateHibernation, req, fmt.Sprintf("cocoon-webhook: spec.desire must be %s or %s, got %q",
			cocoonv1.HibernationDesireHibernate, cocoonv1.HibernationDesireWake, hib.Spec.Desire))
	}

	if hib.Name != hib.Spec.PodRef.Name {
		return denyf(ctx, logger, metrics.HandlerValidateHibernation, req, fmt.Sprintf("cocoon-webhook: metadata.name %q must equal spec.podRef.name %q (one CocoonHibernation per pod, named after it)",
			hib.Name, hib.Spec.PodRef.Name))
	}

	// Fail closed on list errors: admitting a possible duplicate risks the
	// non-converging flip-flop this validator exists to prevent.
	existing, err := s.dyn.Resource(cocoonHibernationGVR).Namespace(req.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		logger.Errorf(ctx, err, "list cocoonhibernations in %s", req.Namespace)
		return recordDeny(metrics.HandlerValidateHibernation, metrics.ResultError, metrics.ReasonList, fmt.Sprintf("cocoon-webhook: cannot verify podRef uniqueness: %v", err))
	}
	for i := range existing.Items {
		other := &existing.Items[i]
		if podName, _, _ := unstructured.NestedString(other.Object, "spec", "podRef", "name"); podName != hib.Spec.PodRef.Name {
			continue
		}
		msg := fmt.Sprintf("cocoon-webhook: pod %q already has a live CocoonHibernation %q; flip its spec.desire instead of creating a second CR",
			hib.Spec.PodRef.Name, other.GetName())
		if other.GetDeletionTimestamp() != nil {
			msg = fmt.Sprintf("cocoon-webhook: pod %q's CocoonHibernation %q is still terminating; retry after its cleanup finishes",
				hib.Spec.PodRef.Name, other.GetName())
		}
		return denyf(ctx, logger, metrics.HandlerValidateHibernation, req, msg)
	}
	return recordAllow(metrics.HandlerValidateHibernation, metrics.ResultAllow, "")
}
