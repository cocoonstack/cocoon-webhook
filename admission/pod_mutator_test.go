package admission

import (
	"encoding/json"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/cocoonstack/cocoon-common/meta"
)

func TestMutatePodAllowsNonCocoonPod(t *testing.T) {
	srv := newTestServer(t)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"}}
	resp := srv.mutatePod(t.Context(), buildPodReview(t, pod))
	if !resp.Allowed {
		t.Errorf("non-cocoon pod should be allowed")
	}
	if len(resp.Patch) != 0 {
		t.Errorf("non-cocoon pod should not get a patch")
	}
}

func TestMutatePodAllowsCocoonSetOwnedPod(t *testing.T) {
	srv := newTestServer(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "p",
			Namespace: "ns",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: meta.KindCocoonSet, Name: "demo"},
			},
		},
		Spec: corev1.PodSpec{
			Tolerations: []corev1.Toleration{{Key: meta.TolerationKey}},
		},
	}
	resp := srv.mutatePod(t.Context(), buildPodReview(t, pod))
	if !resp.Allowed {
		t.Errorf("cocoonset-owned pod should be allowed")
	}
	if len(resp.Patch) != 0 {
		t.Errorf("cocoonset-owned pod should not be patched")
	}
}

func TestMutatePodDeniesBareCocoonPod(t *testing.T) {
	srv := newTestServer(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo-0",
			Namespace: "ns",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: "demo-7b7c9d9d5f"},
			},
		},
		Spec: corev1.PodSpec{
			Tolerations: []corev1.Toleration{{Key: meta.TolerationKey}},
		},
	}
	resp := srv.mutatePod(t.Context(), buildPodReview(t, pod))
	if resp.Allowed {
		t.Errorf("bare cocoon pod should be denied")
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	client := fake.NewSimpleClientset()
	return NewServer(client)
}

func buildPodReview(t *testing.T, pod *corev1.Pod) *admissionv1.AdmissionReview {
	t.Helper()
	raw, err := json.Marshal(pod)
	if err != nil {
		t.Fatalf("marshal pod: %v", err)
	}
	return &admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			UID:       "test-uid",
			Kind:      metav1.GroupVersionKind{Kind: "Pod", Version: "v1"},
			Namespace: pod.Namespace,
			Name:      pod.Name,
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: raw},
		},
	}
}
