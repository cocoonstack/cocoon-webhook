package admission

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/projecteru2/core/log"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cocoonstack/cocoon-common/meta"
	"github.com/cocoonstack/cocoon-webhook/metrics"
)

// podShape is the narrow slice of a Pod this hook reads: it fires for every
// pod created cluster-wide, so skip decoding containers/volumes/probes.
type podShape struct {
	Metadata struct {
		OwnerReferences []metav1.OwnerReference `json:"ownerReferences"`
	} `json:"metadata"`
	Spec struct {
		Tolerations []corev1.Toleration `json:"tolerations"`
	} `json:"spec"`
}

// mutatePod emits no patches, only Allow/Deny: it stays a mutating webhook
// because those run first, so a bare-pod Deny short-circuits the chain
// (config/webhook/configuration.yaml carries the same note).
func (s *Server) mutatePod(ctx context.Context, review *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	req := review.Request

	if req.Kind.Kind != "Pod" {
		return recordAllow(metrics.HandlerMutate, metrics.ResultSkipped, metrics.ReasonKind)
	}

	var pod podShape
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		// Bad client input — apiserver will reject it anyway, so fail open.
		log.WithFunc("mutatePod").Warnf(ctx, "decode pod %s/%s: %v", req.Namespace, req.Name, err)
		return recordAllow(metrics.HandlerMutate, metrics.ResultSkipped, metrics.ReasonDecode)
	}

	if !meta.HasCocoonTolerationKey(pod.Spec.Tolerations) {
		return recordAllow(metrics.HandlerMutate, metrics.ResultSkipped, metrics.ReasonNotCocoon)
	}

	if !meta.IsOwnedByCocoonSet(pod.Metadata.OwnerReferences) {
		log.WithFunc("mutatePod").Warnf(ctx, "deny bare cocoon pod %s/%s: not owned by CocoonSet", req.Namespace, req.Name)
		return recordDeny(metrics.HandlerMutate, metrics.ResultDeny, "", "cocoon pods must be managed by a CocoonSet")
	}

	// Owner references are client-settable and unverified by the apiserver;
	// the authenticated requester is the only unforgeable signal.
	if !slices.Contains(s.podCreators, req.UserInfo.Username) {
		log.WithFunc("mutatePod").Warnf(ctx, "deny cocoon pod %s/%s: creator %q is not an allowed controller", req.Namespace, req.Name, req.UserInfo.Username)
		return recordDeny(metrics.HandlerMutate, metrics.ResultDeny, "", fmt.Sprintf("cocoon pods must be created by the CocoonSet controller, got user %q", req.UserInfo.Username))
	}

	return recordAllow(metrics.HandlerMutate, metrics.ResultAllow, "")
}
