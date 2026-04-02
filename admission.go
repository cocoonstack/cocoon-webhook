package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/projecteru2/core/log"
	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/cocoonstack/cocoon-common/meta"
)

// mutate applies placement and vm-name annotations for Pod CREATE operations.
func mutate(ctx context.Context, clientset kubernetes.Interface, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	logger := log.WithFunc("mutate")

	if req.Kind.Kind != "Pod" {
		return allowResponse()
	}

	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		return allowResponse()
	}

	if !hasCocoonToleration(pod.Spec.Tolerations) {
		return allowResponse()
	}

	for _, ref := range pod.OwnerReferences {
		if ref.Kind == meta.KindCocoonSet {
			logger.Infof(ctx, "mutate %s/%s: owned by CocoonSet %s, skipping", req.Namespace, req.Name, ref.Name)
			return allowResponse()
		}
	}

	if pod.Spec.NodeName != "" {
		return allowResponse()
	}

	cm, err := clientset.CoreV1().ConfigMaps(req.Namespace).Get(ctx, affinityConfigMap, metav1.GetOptions{})
	if err != nil {
		logger.Warnf(ctx, "mutate %s/%s: failed to get ConfigMap %s: %v", req.Namespace, req.Name, affinityConfigMap, err)
		cm = nil
	}

	if pod.Annotations != nil && pod.Annotations[vmNameAnnotation] != "" {
		vmName := pod.Annotations[vmNameAnnotation]
		if nodeName := lookupVMNode(cm, vmName); nodeName != "" {
			return patchNodeName(ctx, nodeName)
		}
		return pickCocoonNode(ctx, clientset)
	}

	vmName := deriveVMName(ctx, &pod, req.Namespace, req.Name, cm)
	nodeName := lookupVMNode(cm, vmName)

	var patches []jsonPatch
	if pod.Annotations == nil {
		patches = append(patches, jsonPatch{
			Op:    "add",
			Path:  "/metadata/annotations",
			Value: map[string]string{},
		})
	}

	patches = append(patches, jsonPatch{
		Op:    "add",
		Path:  "/metadata/annotations/" + escapeJSONPointer(vmNameAnnotation),
		Value: vmName,
	})

	if nodeName != "" {
		patches = append(patches, jsonPatch{
			Op:    "add",
			Path:  "/spec/nodeName",
			Value: nodeName,
		})
		logger.Infof(ctx, "mutate %s/%s: vm=%s -> node=%s (affinity)", req.Namespace, req.Name, vmName, nodeName)
	} else if node := pickAnyCocoonNode(ctx, clientset); node != "" {
		patches = append(patches, jsonPatch{
			Op:    "add",
			Path:  "/spec/nodeName",
			Value: node,
		})
		logger.Infof(ctx, "mutate %s/%s: vm=%s -> node=%s (new, round-robin)", req.Namespace, req.Name, vmName, node)
	}

	patchBytes, err := json.Marshal(patches)
	if err != nil {
		logger.Error(ctx, err, "marshal patches")
		return allowResponse()
	}
	pt := admissionv1.PatchTypeJSONPatch
	return &admissionv1.AdmissionResponse{
		Allowed:   true,
		Patch:     patchBytes,
		PatchType: &pt,
	}
}

// validate rejects scale-down (replicas decrease) for cocoon-type Deployments
// and StatefulSets. Agents are stateful VMs — scale-down would destroy state.
// Use the Hibernation CRD to suspend individual agents instead.
func validate(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	if req.Operation != admissionv1.Update {
		return allowResponse()
	}

	switch req.Kind.Kind {
	case "Deployment":
		return validateDeploymentScale(ctx, req)
	case "StatefulSet":
		return validateStatefulSetScale(ctx, req)
	default:
		return allowResponse()
	}
}

func validateDeploymentScale(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	var oldDeploy, newDeploy appsv1.Deployment
	if err := json.Unmarshal(req.OldObject.Raw, &oldDeploy); err != nil {
		return allowResponse()
	}
	if err := json.Unmarshal(req.Object.Raw, &newDeploy); err != nil {
		return allowResponse()
	}

	if !hasCocoonToleration(newDeploy.Spec.Template.Spec.Tolerations) {
		return allowResponse()
	}

	return checkScaleDown(
		ctx, req, "Deployment",
		replicaCount(oldDeploy.Spec.Replicas),
		replicaCount(newDeploy.Spec.Replicas),
	)
}

func validateStatefulSetScale(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	var oldSTS, newSTS appsv1.StatefulSet
	if err := json.Unmarshal(req.OldObject.Raw, &oldSTS); err != nil {
		return allowResponse()
	}
	if err := json.Unmarshal(req.Object.Raw, &newSTS); err != nil {
		return allowResponse()
	}

	if !hasCocoonToleration(newSTS.Spec.Template.Spec.Tolerations) {
		return allowResponse()
	}

	return checkScaleDown(
		ctx, req, "StatefulSet",
		replicaCount(oldSTS.Spec.Replicas),
		replicaCount(newSTS.Spec.Replicas),
	)
}

// checkScaleDown denies the request if newReplicas < oldReplicas.
func checkScaleDown(ctx context.Context, req *admissionv1.AdmissionRequest, kind string, oldReplicas, newReplicas int32) *admissionv1.AdmissionResponse {
	if newReplicas >= oldReplicas {
		return allowResponse()
	}
	msg := fmt.Sprintf(
		"cocoon-webhook: scale-down blocked for cocoon %s %s/%s (%d -> %d). "+
			"Use Hibernation CRD to suspend individual agents.",
		kind, req.Namespace, req.Name, oldReplicas, newReplicas)
	logger := log.WithFunc("checkScaleDown")
	logger.Warnf(ctx, "validate DENY: %s", msg)
	return denyResponse(msg)
}
