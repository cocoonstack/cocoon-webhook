package admission

import (
	"context"
	"encoding/json"

	"github.com/projecteru2/core/log"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"

	commonadmission "github.com/cocoonstack/cocoon-common/k8s/admission"
	"github.com/cocoonstack/cocoon-common/meta"
	"github.com/cocoonstack/cocoon-webhook/metrics"
)

func (s *Server) mutatePod(ctx context.Context, review *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	logger := log.WithFunc("mutatePod")
	req := review.Request

	if req.Kind.Kind != "Pod" {
		metrics.RecordAdmission(metrics.HandlerMutate, metrics.DecisionAllow)
		return commonadmission.Allow()
	}

	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		logger.Warnf(ctx, "decode pod %s/%s: %v", req.Namespace, req.Name, err)
		metrics.RecordAdmission(metrics.HandlerMutate, metrics.DecisionError)
		return commonadmission.Allow()
	}

	if !meta.HasCocoonToleration(pod.Spec.Tolerations) {
		metrics.RecordAdmission(metrics.HandlerMutate, metrics.DecisionAllow)
		return commonadmission.Allow()
	}

	if meta.IsOwnedByCocoonSet(pod.OwnerReferences) {
		metrics.RecordAdmission(metrics.HandlerMutate, metrics.DecisionAllow)
		return commonadmission.Allow()
	}

	// Bare pods with cocoon toleration are not supported; must use CocoonSet.
	logger.Warnf(ctx, "deny bare cocoon pod %s/%s: not owned by CocoonSet", req.Namespace, req.Name)
	metrics.RecordAdmission(metrics.HandlerMutate, metrics.DecisionDeny)
	return commonadmission.Deny("cocoon pods must be managed by a CocoonSet")
}
