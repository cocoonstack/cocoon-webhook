package admission

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/projecteru2/core/log"
	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	"github.com/cocoonstack/cocoon-common/meta"
	"github.com/cocoonstack/cocoon-webhook/metrics"
)

// scalable is the small subset of appsv1.Deployment / appsv1.StatefulSet
// that the workload validator needs: replica count + the pod
// template's tolerations. The generic helper below uses it as the
// type constraint so the two scale-down code paths share one
// implementation.
type scalable interface {
	appsv1.Deployment | appsv1.StatefulSet
}

// validateWorkload is the admission entry point for Deployment and
// StatefulSet UPDATE. It rejects scale-down on cocoon workloads
// because the agents are stateful VMs — reducing replicas would
// destroy state. Operators should use a CocoonHibernation CR to
// suspend an individual agent instead.
func (s *Server) validateWorkload(ctx context.Context, review *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	req := review.Request
	if req.Operation != admissionv1.Update {
		return allowResponse()
	}
	switch req.Kind.Kind {
	case "Deployment":
		return validateScaleDown[appsv1.Deployment](ctx, req)
	case "StatefulSet":
		return validateScaleDown[appsv1.StatefulSet](ctx, req)
	default:
		return allowResponse()
	}
}

// validateScaleDown decodes the old and new objects, applies the
// cocoon-toleration filter, and runs checkScaleDown.
func validateScaleDown[T scalable](ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	logger := log.WithFunc("validateScaleDown")
	var oldObj, newObj T
	if err := json.Unmarshal(req.OldObject.Raw, &oldObj); err != nil {
		logger.Warnf(ctx, "decode old %s %s/%s: %v", req.Kind.Kind, req.Namespace, req.Name, err)
		return allowResponse()
	}
	if err := json.Unmarshal(req.Object.Raw, &newObj); err != nil {
		logger.Warnf(ctx, "decode new %s %s/%s: %v", req.Kind.Kind, req.Namespace, req.Name, err)
		return allowResponse()
	}

	if !meta.HasCocoonToleration(workloadTolerations(&newObj)) {
		return allowResponse()
	}
	return checkScaleDown(ctx, req, workloadReplicas(&oldObj), workloadReplicas(&newObj))
}

// workloadReplicas extracts the replica count from either Deployment
// or StatefulSet via a type switch on the generic argument. The
// apps controllers default a nil pointer to 1, so we do too.
func workloadReplicas[T scalable](obj *T) int32 {
	switch v := any(obj).(type) {
	case *appsv1.Deployment:
		return ptr.Deref(v.Spec.Replicas, 1)
	case *appsv1.StatefulSet:
		return ptr.Deref(v.Spec.Replicas, 1)
	default:
		return 0
	}
}

// workloadTolerations returns the pod-template tolerations of the
// workload — used to filter out non-cocoon workloads early.
func workloadTolerations[T scalable](obj *T) []corev1.Toleration {
	switch v := any(obj).(type) {
	case *appsv1.Deployment:
		return v.Spec.Template.Spec.Tolerations
	case *appsv1.StatefulSet:
		return v.Spec.Template.Spec.Tolerations
	default:
		return nil
	}
}

func checkScaleDown(ctx context.Context, req *admissionv1.AdmissionRequest, oldReplicas, newReplicas int32) *admissionv1.AdmissionResponse {
	if newReplicas >= oldReplicas {
		metrics.RecordAdmission(metrics.HandlerValidate, metrics.DecisionAllow)
		return allowResponse()
	}
	msg := fmt.Sprintf(
		"cocoon-webhook: scale-down blocked for cocoon %s %s/%s (%d -> %d). "+
			"Use a CocoonHibernation CR to suspend individual agents.",
		req.Kind.Kind, req.Namespace, req.Name, oldReplicas, newReplicas)
	log.WithFunc("checkScaleDown").Warn(ctx, msg)
	metrics.RecordAdmission(metrics.HandlerValidate, metrics.DecisionDeny)
	return denyResponse(msg)
}
