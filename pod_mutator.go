package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/projecteru2/core/log"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/cocoonstack/cocoon-common/meta"
)

const (
	// defaultNodePool is used when a pod does not request a specific
	// pool via the cocoonstack.io/pool label or annotation.
	defaultNodePool = "default"
)

// mutatePod is the admission entry point for Pod CREATE. It writes
// the canonical VM name annotation and pins the pod to a sticky
// cocoon node via spec.nodeName. Pods that are not cocoon-tolerated,
// are CocoonSet-owned, or already have spec.nodeName set are passed
// through unchanged.
func (s *Server) mutatePod(ctx context.Context, review *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	logger := log.WithFunc("mutatePod")
	req := review.Request

	if req.Kind.Kind != "Pod" {
		recordAdmission(HandlerMutate, DecisionAllow)
		return allowResponse()
	}

	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		logger.Warnf(ctx, "decode pod %s/%s: %v", req.Namespace, req.Name, err)
		recordAdmission(HandlerMutate, DecisionError)
		return allowResponse()
	}

	if !meta.HasCocoonToleration(pod.Spec.Tolerations) {
		recordAdmission(HandlerMutate, DecisionAllow)
		return allowResponse()
	}

	if isOwnedByCocoonSet(&pod) {
		// CocoonSet-managed pods come pre-annotated by the operator.
		recordAdmission(HandlerMutate, DecisionAllow)
		return allowResponse()
	}

	if pod.Spec.NodeName != "" {
		recordAdmission(HandlerMutate, DecisionAllow)
		return allowResponse()
	}

	pool := podNodePool(&pod)
	res, err := s.affinity.Reserve(ctx, ReserveRequest{
		Pool:       pool,
		Namespace:  req.Namespace,
		Deployment: meta.OwnerDeploymentName(pod.OwnerReferences),
		PodName:    podDisplayName(&pod, req),
	})
	if err != nil {
		// Preserve cluster availability if the affinity store is
		// unreachable: log loudly and let the pod through unmutated.
		logger.Errorf(ctx, err, "reserve affinity for pod %s/%s", req.Namespace, podDisplayName(&pod, req))
		recordAdmission(HandlerMutate, DecisionAffinityFailed)
		return allowResponse()
	}
	recordReservation(pool)

	patch, err := buildMutatePatch(&pod, res)
	if err != nil {
		logger.Errorf(ctx, err, "build mutate patch for pod %s/%s", req.Namespace, podDisplayName(&pod, req))
		recordAdmission(HandlerMutate, DecisionError)
		return allowResponse()
	}

	logger.Infof(ctx, "mutate %s/%s: vm=%s node=%s", req.Namespace, podDisplayName(&pod, req), res.VMName, res.Node)
	recordAdmission(HandlerMutate, DecisionAllow)

	pt := admissionv1.PatchTypeJSONPatch
	return &admissionv1.AdmissionResponse{
		Allowed:   true,
		Patch:     patch,
		PatchType: &pt,
	}
}

// isOwnedByCocoonSet reports whether any of the pod's OwnerReferences
// point at a CocoonSet. CocoonSet-managed pods come from the
// operator already carrying the full meta.VMSpec contract; the
// webhook leaves them alone.
func isOwnedByCocoonSet(pod *corev1.Pod) bool {
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == meta.KindCocoonSet {
			return true
		}
	}
	return false
}

// podDisplayName returns the most useful identifier for log lines.
// Pods created via a controller may have an empty Name on the
// admission request (the API server fills it in after admission); in
// that case fall back to GenerateName + req.Name.
func podDisplayName(pod *corev1.Pod, req *admissionv1.AdmissionRequest) string {
	if pod.Name != "" {
		return pod.Name
	}
	if req.Name != "" {
		return req.Name
	}
	return pod.GenerateName + "<unnamed>"
}

// podNodePool returns the cocoon pool the pod requests. Resolution
// order: nodeSelector[cocoonstack.io/pool] -> labels[cocoonstack.io/pool]
// -> annotations[cocoonstack.io/pool] -> default.
func podNodePool(pod *corev1.Pod) string {
	if v := pod.Spec.NodeSelector[nodePoolLabel]; v != "" {
		return v
	}
	if v := pod.Labels[nodePoolLabel]; v != "" {
		return v
	}
	if v := pod.Annotations[nodePoolLabel]; v != "" {
		return v
	}
	return defaultNodePool
}

// buildMutatePatch produces an RFC 6902 JSON patch that writes the
// VM name annotation and (when present) pins spec.nodeName.
func buildMutatePatch(pod *corev1.Pod, res Reservation) ([]byte, error) {
	var ops []jsonPatchOp
	if pod.Annotations == nil {
		ops = append(ops, jsonPatchOp{
			Op:    "add",
			Path:  "/metadata/annotations",
			Value: map[string]string{},
		})
	}
	ops = append(ops, jsonPatchOp{
		Op:    "add",
		Path:  "/metadata/annotations/" + escapeJSONPointer(meta.AnnotationVMName),
		Value: res.VMName,
	})
	if res.Node != "" {
		ops = append(ops, jsonPatchOp{
			Op:    "add",
			Path:  "/spec/nodeName",
			Value: res.Node,
		})
	}
	out, err := json.Marshal(ops)
	if err != nil {
		return nil, fmt.Errorf("marshal patch: %w", err)
	}
	return out, nil
}

// escapeJSONPointer escapes the two characters that are reserved in
// RFC 6901 JSON Pointer paths.
func escapeJSONPointer(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")
	return s
}

// jsonPatchOp is a single RFC 6902 patch operation.
type jsonPatchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}
