package admission

import (
	"context"
	"encoding/json"

	"github.com/projecteru2/core/log"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"

	commonadmission "github.com/cocoonstack/cocoon-common/k8s/admission"
	"github.com/cocoonstack/cocoon-common/meta"
	"github.com/cocoonstack/cocoon-webhook/affinity"
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

	if pod.Spec.NodeName != "" {
		metrics.RecordAdmission(metrics.HandlerMutate, metrics.DecisionAllow)
		return commonadmission.Allow()
	}

	pool := meta.PodNodePool(&pod)
	name := podDisplayName(&pod, req)
	res, err := s.store.Reserve(ctx, affinity.ReserveRequest{
		Pool:       pool,
		Namespace:  req.Namespace,
		Deployment: meta.OwnerDeploymentName(pod.OwnerReferences),
		PodName:    name,
	})
	if err != nil {
		// Fail-open.
		logger.Errorf(ctx, err, "reserve affinity for pod %s/%s", req.Namespace, name)
		metrics.RecordAdmission(metrics.HandlerMutate, metrics.DecisionAffinityFailed)
		return commonadmission.Allow()
	}
	metrics.RecordReservation(pool)

	patch, err := buildMutatePatch(&pod, res)
	if err != nil {
		logger.Errorf(ctx, err, "build mutate patch for pod %s/%s", req.Namespace, name)
		metrics.RecordAdmission(metrics.HandlerMutate, metrics.DecisionError)
		return commonadmission.Allow()
	}

	logger.Infof(ctx, "mutate %s/%s: vm=%s node=%s", req.Namespace, name, res.VMName, res.Node)
	metrics.RecordAdmission(metrics.HandlerMutate, metrics.DecisionAllow)

	pt := admissionv1.PatchTypeJSONPatch
	return &admissionv1.AdmissionResponse{
		Allowed:   true,
		Patch:     patch,
		PatchType: &pt,
	}
}

func podDisplayName(pod *corev1.Pod, req *admissionv1.AdmissionRequest) string {
	if pod.Name != "" {
		return pod.Name
	}
	if req.Name != "" {
		return req.Name
	}
	return pod.GenerateName + "<unnamed>"
}

func buildMutatePatch(pod *corev1.Pod, res affinity.Reservation) ([]byte, error) {
	var ops []commonadmission.JSONPatchOp
	if pod.Annotations == nil {
		ops = append(ops, commonadmission.JSONPatchOp{
			Op:    "add",
			Path:  "/metadata/annotations",
			Value: map[string]string{},
		})
	}
	ops = append(ops, commonadmission.JSONPatchOp{
		Op:    "add",
		Path:  "/metadata/annotations/" + commonadmission.EscapeJSONPointer(meta.AnnotationVMName),
		Value: res.VMName,
	})
	if res.Node != "" {
		ops = append(ops, commonadmission.JSONPatchOp{
			Op:    "add",
			Path:  "/spec/nodeName",
			Value: res.Node,
		})
	}
	return commonadmission.MarshalPatch(ops)
}
