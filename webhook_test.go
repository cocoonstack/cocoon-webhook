package main

import (
	"context"
	"encoding/json"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/cocoonstack/cocoon-common/meta"
)

func TestAllocateSlotReusesExistingAndGap(t *testing.T) {
	t.Run("existing slot for same pod", func(t *testing.T) {
		cm := &corev1.ConfigMap{Data: map[string]string{
			"vk-prod-demo-0": "node:cocoon-a,pod:pod-0",
			"vk-prod-demo-1": "node:cocoon-b,pod:pod-1",
		}}
		slot, err := allocateSlot("prod", "demo", "pod-1", cm)
		if err != nil {
			t.Fatalf("allocateSlot error: %v", err)
		}
		if slot != 1 {
			t.Fatalf("expected slot 1, got %d", slot)
		}
	})

	t.Run("fills first gap", func(t *testing.T) {
		cm := &corev1.ConfigMap{Data: map[string]string{
			"vk-prod-demo-0": "node:cocoon-a,pod:pod-0",
			"vk-prod-demo-2": "node:cocoon-b,pod:pod-2",
		}}
		slot, err := allocateSlot("prod", "demo", "pod-3", cm)
		if err != nil {
			t.Fatalf("allocateSlot error: %v", err)
		}
		if slot != 1 {
			t.Fatalf("expected gap slot 1, got %d", slot)
		}
	})
}

func TestDeriveVMName(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			OwnerReferences: []metav1.OwnerReference{{
				Kind: "ReplicaSet",
				Name: "demo-7b7c9d9d5f",
			}},
		},
	}
	cm := &corev1.ConfigMap{Data: map[string]string{
		"vk-prod-demo-0": "node:cocoon-a,pod:existing-pod",
	}}

	got := deriveVMName(context.Background(), pod, "prod", "fresh-pod", cm)
	if got != "vk-prod-demo-1" {
		t.Fatalf("expected deployment-derived vm name, got %q", got)
	}

	got = deriveVMName(context.Background(), &corev1.Pod{}, "prod", "bare-pod", nil)
	if got != "vk-prod-bare-pod" {
		t.Fatalf("expected bare pod vm name, got %q", got)
	}
}

func TestCheckScaleDown(t *testing.T) {
	req := &admissionv1.AdmissionRequest{
		Namespace: "prod",
		Name:      "demo",
	}

	if resp := checkScaleDown(context.Background(), req, "Deployment", 2, 3); !resp.Allowed {
		t.Fatalf("expected scale-up to be allowed")
	}

	resp := checkScaleDown(context.Background(), req, "Deployment", 3, 2)
	if resp.Allowed {
		t.Fatalf("expected scale-down to be denied")
	}
	if resp.Result == nil || resp.Result.Message == "" {
		t.Fatalf("expected denial message")
	}
}

func TestMutateAssignsVMNameAndNode(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: affinityConfigMap, Namespace: "prod"},
			Data:       map[string]string{},
		},
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "cocoon-a",
				Labels: map[string]string{"type": "virtual-kubelet"},
			},
		},
	)

	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo-abc",
			Namespace: "prod",
			OwnerReferences: []metav1.OwnerReference{{
				Kind: "ReplicaSet",
				Name: "demo-7b7c9d9d5f",
			}},
		},
		Spec: corev1.PodSpec{
			Tolerations: []corev1.Toleration{{Key: meta.TolerationKey}},
		},
	}
	raw, err := json.Marshal(pod)
	if err != nil {
		t.Fatalf("marshal pod: %v", err)
	}
	req := &admissionv1.AdmissionRequest{
		Namespace: "prod",
		Name:      "demo-abc",
		Kind: metav1.GroupVersionKind{
			Kind: "Pod",
		},
		Object: runtime.RawExtension{Raw: raw},
	}

	resp := mutate(context.Background(), clientset, req)
	if !resp.Allowed {
		t.Fatalf("expected mutate to allow request")
	}
	if resp.PatchType == nil || *resp.PatchType != admissionv1.PatchTypeJSONPatch {
		t.Fatalf("expected JSON patch response")
	}

	var patches []jsonPatch
	if err := json.Unmarshal(resp.Patch, &patches); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	if len(patches) != 3 {
		t.Fatalf("expected 3 patches, got %d", len(patches))
	}
}

func TestValidateDeploymentScale(t *testing.T) {
	oldReplicas := int32(3)
	newReplicas := int32(2)
	oldDeploy := appsv1.Deployment{
		Spec: appsv1.DeploymentSpec{
			Replicas: &oldReplicas,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Tolerations: []corev1.Toleration{{Key: meta.TolerationKey}},
				},
			},
		},
	}
	newDeploy := oldDeploy
	newDeploy.Spec.Replicas = &newReplicas

	oldRaw, err := json.Marshal(oldDeploy)
	if err != nil {
		t.Fatalf("marshal old deploy: %v", err)
	}
	newRaw, err := json.Marshal(newDeploy)
	if err != nil {
		t.Fatalf("marshal new deploy: %v", err)
	}

	req := &admissionv1.AdmissionRequest{
		Operation: admissionv1.Update,
		Kind: metav1.GroupVersionKind{
			Kind: "Deployment",
		},
		Namespace: "prod",
		Name:      "demo",
		OldObject: runtime.RawExtension{Raw: oldRaw},
		Object:    runtime.RawExtension{Raw: newRaw},
	}

	resp := validate(context.Background(), req)
	if resp.Allowed {
		t.Fatalf("expected deployment scale-down to be denied")
	}
}
