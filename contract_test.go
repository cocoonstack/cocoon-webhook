package main

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cocoonstack/cocoon-common/meta"
)

func TestDeriveVMNameMatchesSharedContract(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			OwnerReferences: []metav1.OwnerReference{{
				Kind: "ReplicaSet",
				Name: "demo-7b7c9d9d5f",
			}},
		},
	}
	cm := &corev1.ConfigMap{Data: map[string]string{
		meta.VMNameForDeployment("prod", "demo", 0): "node:cocoon-a,pod:existing-pod",
	}}

	if got := deriveVMName(context.Background(), pod, "prod", "fresh-pod", cm); got != meta.VMNameForDeployment("prod", "demo", 1) {
		t.Fatalf("deployment vm name mismatch: got %q", got)
	}
	if got := deriveVMName(context.Background(), &corev1.Pod{}, "prod", "toolbox", nil); got != meta.VMNameForPod("prod", "toolbox") {
		t.Fatalf("bare pod vm name mismatch: got %q", got)
	}
}

func TestHasCocoonTolerationMatchesSharedContract(t *testing.T) {
	tolerations := []corev1.Toleration{{Key: meta.TolerationKey}}
	if got := hasCocoonToleration(tolerations); got != meta.HasCocoonToleration(tolerations) {
		t.Fatalf("toleration contract mismatch: got %v", got)
	}
}
