package admission

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/projecteru2/core/log"
	admissionv1 "k8s.io/api/admission/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/cocoonstack/cocoon-common/meta"
	"github.com/cocoonstack/cocoon-webhook/metrics"
)

// validateWorkload rejects scale-down on cocoon workloads (stateful VMs).
// Handles both direct UPDATE and /scale subresource requests.
func (s *Server) validateWorkload(ctx context.Context, review *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	req := review.Request
	if req.Operation != admissionv1.Update {
		return recordAllow(metrics.HandlerValidate, metrics.ResultSkipped, metrics.ReasonOperation)
	}
	if req.SubResource == "scale" {
		return s.validateScaleSubresource(ctx, req)
	}
	switch req.Kind.Kind {
	case "Deployment", "StatefulSet":
		return validateWorkloadScaleDown(ctx, req)
	default:
		return recordAllow(metrics.HandlerValidate, metrics.ResultSkipped, metrics.ReasonKind)
	}
}

// validateScaleSubresource fetches the parent workload to check tolerations:
// fail-closed on apiserver errors, fail-open on malformed Scale payloads.
func (s *Server) validateScaleSubresource(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	var oldScale, newScale autoscalingv1.Scale
	if !decodeUpdatePair(ctx, "validateScaleSubresource", req, &oldScale, &newScale) {
		return recordAllow(metrics.HandlerValidate, metrics.ResultSkipped, metrics.ReasonDecode)
	}

	tolerations, ok, err := s.fetchParentTolerations(ctx, req)
	if err != nil {
		log.WithFunc("validateScaleSubresource").Errorf(ctx, err, "fetch parent tolerations %s/%s", req.Namespace, req.Name)
		return recordDeny(metrics.HandlerValidate, metrics.ResultError, metrics.ReasonParentFetch, fmt.Sprintf("cocoon-webhook: cannot verify parent workload: %v", err))
	}
	if !ok {
		return recordAllow(metrics.HandlerValidate, metrics.ResultSkipped, metrics.ReasonNoParent)
	}
	if !meta.HasCocoonTolerationKey(tolerations) {
		return recordAllow(metrics.HandlerValidate, metrics.ResultSkipped, metrics.ReasonNotCocoon)
	}
	return checkScaleDown(ctx, req, oldScale.Spec.Replicas, newScale.Spec.Replicas)
}

func (s *Server) fetchParentTolerations(ctx context.Context, req *admissionv1.AdmissionRequest) ([]corev1.Toleration, bool, error) {
	switch req.Resource.Resource {
	case "deployments":
		dep, err := s.client.AppsV1().Deployments(req.Namespace).Get(ctx, req.Name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil, false, nil
			}
			return nil, false, fmt.Errorf("get parent deployment: %w", err)
		}
		return dep.Spec.Template.Spec.Tolerations, true, nil
	case "statefulsets":
		sts, err := s.client.AppsV1().StatefulSets(req.Namespace).Get(ctx, req.Name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil, false, nil
			}
			return nil, false, fmt.Errorf("get parent statefulset: %w", err)
		}
		return sts.Spec.Template.Spec.Tolerations, true, nil
	default:
		return nil, false, nil
	}
}

// workloadShape is the narrow slice Deployment and StatefulSet share and the
// only fields this gate reads — every workload UPDATE cluster-wide pays this decode.
type workloadShape struct {
	Spec struct {
		Replicas *int32 `json:"replicas"`
		Template struct {
			Spec struct {
				Tolerations []corev1.Toleration `json:"tolerations"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
}

func validateWorkloadScaleDown(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	var oldObj, newObj workloadShape
	if !decodeUpdatePair(ctx, "validateWorkloadScaleDown", req, &oldObj, &newObj) {
		return recordAllow(metrics.HandlerValidate, metrics.ResultSkipped, metrics.ReasonDecode)
	}
	if !meta.HasCocoonTolerationKey(oldObj.Spec.Template.Spec.Tolerations) {
		return recordAllow(metrics.HandlerValidate, metrics.ResultSkipped, metrics.ReasonNotCocoon)
	}
	return checkScaleDown(ctx, req, replicasOrDefault(oldObj.Spec.Replicas), replicasOrDefault(newObj.Spec.Replicas))
}

// decodeUpdatePair decodes req.OldObject and req.Object, returning false on
// malformed payloads so callers fail open (apiserver rejects those anyway).
func decodeUpdatePair(ctx context.Context, fn string, req *admissionv1.AdmissionRequest, oldObj, newObj any) bool {
	if err := json.Unmarshal(req.OldObject.Raw, oldObj); err != nil {
		log.WithFunc(fn).Warnf(ctx, "decode old %s %s/%s: %v", req.Kind.Kind, req.Namespace, req.Name, err)
		return false
	}
	if err := json.Unmarshal(req.Object.Raw, newObj); err != nil {
		log.WithFunc(fn).Warnf(ctx, "decode new %s %s/%s: %v", req.Kind.Kind, req.Namespace, req.Name, err)
		return false
	}
	return true
}

// replicasOrDefault defaults to 1 when the pointer is nil, matching the
// apps controller's default for Spec.Replicas.
func replicasOrDefault(r *int32) int32 {
	return ptr.Deref(r, 1)
}

func checkScaleDown(ctx context.Context, req *admissionv1.AdmissionRequest, oldReplicas, newReplicas int32) *admissionv1.AdmissionResponse {
	if newReplicas >= oldReplicas {
		return recordAllow(metrics.HandlerValidate, metrics.ResultAllow, "")
	}
	msg := fmt.Sprintf(
		"cocoon-webhook: scale-down blocked for cocoon %s %s/%s (%d -> %d). "+
			"Use a CocoonHibernation CR to suspend individual agents.",
		req.Kind.Kind, req.Namespace, req.Name, oldReplicas, newReplicas,
	)
	log.WithFunc("checkScaleDown").Warn(ctx, msg)
	return recordDeny(metrics.HandlerValidate, metrics.ResultDeny, "", msg)
}
