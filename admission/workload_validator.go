package admission

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/projecteru2/core/log"
	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	commonadmission "github.com/cocoonstack/cocoon-common/k8s/admission"
	"github.com/cocoonstack/cocoon-common/meta"
	"github.com/cocoonstack/cocoon-webhook/metrics"
)

// validateWorkload rejects scale-down on cocoon workloads (stateful VMs).
// Handles both direct UPDATE and /scale subresource requests.
func (s *Server) validateWorkload(ctx context.Context, review *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	req := review.Request
	if req.Operation != admissionv1.Update {
		metrics.RecordAdmission(metrics.HandlerValidate, metrics.ResultSkipped, metrics.ReasonOperation)
		return commonadmission.Allow()
	}
	if req.SubResource == "scale" {
		return s.validateScaleSubresource(ctx, req)
	}
	switch req.Kind.Kind {
	case "Deployment":
		return validateDeploymentScaleDown(ctx, req)
	case "StatefulSet":
		return validateStatefulSetScaleDown(ctx, req)
	default:
		metrics.RecordAdmission(metrics.HandlerValidate, metrics.ResultSkipped, metrics.ReasonKind)
		return commonadmission.Allow()
	}
}

// validateScaleSubresource fetches the parent workload to check tolerations.
// Fails closed on apiserver errors; fails open on malformed Scale payloads
// (apiserver controls that shape).
func (s *Server) validateScaleSubresource(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	logger := log.WithFunc("validateScaleSubresource")

	var oldScale, newScale autoscalingv1.Scale
	if err := json.Unmarshal(req.OldObject.Raw, &oldScale); err != nil {
		logger.Warnf(ctx, "decode old Scale %s/%s: %v", req.Namespace, req.Name, err)
		metrics.RecordAdmission(metrics.HandlerValidate, metrics.ResultSkipped, metrics.ReasonDecode)
		return commonadmission.Allow()
	}
	if err := json.Unmarshal(req.Object.Raw, &newScale); err != nil {
		logger.Warnf(ctx, "decode new Scale %s/%s: %v", req.Namespace, req.Name, err)
		metrics.RecordAdmission(metrics.HandlerValidate, metrics.ResultSkipped, metrics.ReasonDecode)
		return commonadmission.Allow()
	}

	tolerations, ok, err := s.fetchParentTolerations(ctx, req)
	if err != nil {
		logger.Errorf(ctx, err, "fetch parent tolerations %s/%s", req.Namespace, req.Name)
		metrics.RecordAdmission(metrics.HandlerValidate, metrics.ResultError, metrics.ReasonParentFetch)
		return commonadmission.Deny(fmt.Sprintf("cocoon-webhook: cannot verify parent workload: %v", err))
	}
	if !ok {
		metrics.RecordAdmission(metrics.HandlerValidate, metrics.ResultSkipped, metrics.ReasonNoParent)
		return commonadmission.Allow()
	}
	if !meta.HasCocoonTolerationKey(tolerations) {
		metrics.RecordAdmission(metrics.HandlerValidate, metrics.ResultSkipped, metrics.ReasonNotCocoon)
		return commonadmission.Allow()
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

func validateDeploymentScaleDown(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	var oldObj, newObj appsv1.Deployment
	if !decodeUpdatePair(ctx, "validateDeploymentScaleDown", req, &oldObj, &newObj) {
		metrics.RecordAdmission(metrics.HandlerValidate, metrics.ResultSkipped, metrics.ReasonDecode)
		return commonadmission.Allow()
	}
	if !meta.HasCocoonTolerationKey(oldObj.Spec.Template.Spec.Tolerations) {
		metrics.RecordAdmission(metrics.HandlerValidate, metrics.ResultSkipped, metrics.ReasonNotCocoon)
		return commonadmission.Allow()
	}
	return checkScaleDown(ctx, req, replicasOrDefault(oldObj.Spec.Replicas), replicasOrDefault(newObj.Spec.Replicas))
}

func validateStatefulSetScaleDown(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	var oldObj, newObj appsv1.StatefulSet
	if !decodeUpdatePair(ctx, "validateStatefulSetScaleDown", req, &oldObj, &newObj) {
		metrics.RecordAdmission(metrics.HandlerValidate, metrics.ResultSkipped, metrics.ReasonDecode)
		return commonadmission.Allow()
	}
	if !meta.HasCocoonTolerationKey(oldObj.Spec.Template.Spec.Tolerations) {
		metrics.RecordAdmission(metrics.HandlerValidate, metrics.ResultSkipped, metrics.ReasonNotCocoon)
		return commonadmission.Allow()
	}
	return checkScaleDown(ctx, req, replicasOrDefault(oldObj.Spec.Replicas), replicasOrDefault(newObj.Spec.Replicas))
}

// decodeUpdatePair decodes req.OldObject and req.Object into the provided
// pointers. Returns false and logs a warning on malformed payloads so callers
// can fail open — apiserver rejects the malformed request anyway.
func decodeUpdatePair(ctx context.Context, fn string, req *admissionv1.AdmissionRequest, oldObj, newObj any) bool {
	logger := log.WithFunc(fn)
	if err := json.Unmarshal(req.OldObject.Raw, oldObj); err != nil {
		logger.Warnf(ctx, "decode old %s %s/%s: %v", req.Kind.Kind, req.Namespace, req.Name, err)
		return false
	}
	if err := json.Unmarshal(req.Object.Raw, newObj); err != nil {
		logger.Warnf(ctx, "decode new %s %s/%s: %v", req.Kind.Kind, req.Namespace, req.Name, err)
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
	logger := log.WithFunc("checkScaleDown")
	if newReplicas >= oldReplicas {
		metrics.RecordAdmission(metrics.HandlerValidate, metrics.ResultAllow, "")
		return commonadmission.Allow()
	}
	msg := fmt.Sprintf(
		"cocoon-webhook: scale-down blocked for cocoon %s %s/%s (%d -> %d). "+
			"Use a CocoonHibernation CR to suspend individual agents.",
		req.Kind.Kind, req.Namespace, req.Name, oldReplicas, newReplicas,
	)
	logger.Warn(ctx, msg)
	metrics.RecordAdmission(metrics.HandlerValidate, metrics.ResultDeny, "")
	return commonadmission.Deny(msg)
}
