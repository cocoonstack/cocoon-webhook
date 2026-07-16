package admission

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/projecteru2/core/log"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"

	commonadmission "github.com/cocoonstack/cocoon-common/k8s/admission"
	"github.com/cocoonstack/cocoon-common/meta"
	"github.com/cocoonstack/cocoon-webhook/metrics"
)

// mutatePod emits no patches, only Allow/Deny: it stays a mutating webhook
// because those run first, so a bare-pod Deny short-circuits the chain
// (config/webhook/configuration.yaml carries the same note).
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
		metrics.RecordAdmission(metrics.HandlerMutate, metrics.ResultSkipped, metrics.ReasonDecode)
		return commonadmission.Allow()
	}

	if !meta.HasCocoonTolerationKey(pod.Spec.Tolerations) {
		metrics.RecordAdmission(metrics.HandlerMutate, metrics.ResultSkipped, metrics.ReasonNotCocoon)
		return commonadmission.Allow()
	}

	if !meta.IsOwnedByCocoonSet(pod.OwnerReferences) {
		logger.Warnf(ctx, "deny bare cocoon pod %s/%s: not owned by CocoonSet", req.Namespace, req.Name)
		metrics.RecordAdmission(metrics.HandlerMutate, metrics.ResultDeny, "")
		return commonadmission.Deny("cocoon pods must be managed by a CocoonSet")
	}

	// Owner references are client-settable and unverified by the apiserver;
	// the authenticated requester is the only unforgeable signal.
	if !slices.Contains(s.podCreators, req.UserInfo.Username) {
		logger.Warnf(ctx, "deny cocoon pod %s/%s: creator %q is not an allowed controller", req.Namespace, req.Name, req.UserInfo.Username)
		metrics.RecordAdmission(metrics.HandlerMutate, metrics.ResultDeny, "")
		return commonadmission.Deny(fmt.Sprintf("cocoon pods must be created by the CocoonSet controller, got user %q", req.UserInfo.Username))
	}

	metrics.RecordAdmission(metrics.HandlerMutate, metrics.ResultAllow, "")
	return commonadmission.Allow()
}
