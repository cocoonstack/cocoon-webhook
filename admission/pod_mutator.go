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

// mutatePod emits no patches — it only Allows or Denies. It keeps the mutate
// name and MutatingWebhookConfiguration registration because mutating webhooks
// run before validating ones, so a bare-pod Deny here short-circuits the chain
// instead of running a full mutation pass on a doomed request.
// config/webhook/configuration.yaml carries the same note.
func (s *Server) mutatePod(ctx context.Context, review *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	logger := log.WithFunc("mutatePod")
	req := review.Request

	if req.Kind.Kind != "Pod" {
		metrics.RecordAdmission(metrics.HandlerMutate, metrics.ResultSkipped, metrics.ReasonKind)
		return commonadmission.Allow()
	}

	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		// Bad client input — apiserver will reject it anyway, so fail open.
		logger.Warnf(ctx, "decode pod %s/%s: %v", req.Namespace, req.Name, err)
		metrics.RecordAdmission(metrics.HandlerMutate, metrics.ResultError, metrics.ReasonDecode)
		return commonadmission.Allow()
	}

	if !meta.HasCocoonTolerationKey(pod.Spec.Tolerations) {
		metrics.RecordAdmission(metrics.HandlerMutate, metrics.ResultSkipped, metrics.ReasonNotCocoon)
		return commonadmission.Allow()
	}

	if meta.IsOwnedByCocoonSet(pod.OwnerReferences) {
		metrics.RecordAdmission(metrics.HandlerMutate, metrics.ResultAllow, "")
		return commonadmission.Allow()
	}

	logger.Warnf(ctx, "deny bare cocoon pod %s/%s: not owned by CocoonSet", req.Namespace, req.Name)
	metrics.RecordAdmission(metrics.HandlerMutate, metrics.ResultDeny, "")
	return commonadmission.Deny("cocoon pods must be managed by a CocoonSet")
}
