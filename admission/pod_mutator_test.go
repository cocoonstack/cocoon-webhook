package admission

import (
	"encoding/json"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/cocoonstack/cocoon-common/meta"
	"github.com/cocoonstack/cocoon-webhook/metrics"
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

// TestMutatePodRecordsExactlyOneSample pins one-sample-per-request on an
// early-return path: exactly one series, tagged skipped/not_cocoon.
func TestMutatePodRecordsExactlyOneSample(t *testing.T) {
	metrics.AdmissionTotal.Reset()
	srv := newTestServer(t)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"}}
	srv.mutatePod(t.Context(), buildPodReview(t, pod))

	if series, total := collectAdmission(t); series != 1 || total != 1 {
		t.Fatalf("want exactly one admission sample, got series=%d total=%v", series, total)
	}
	if got := admissionValue(t, metrics.HandlerMutate, metrics.ResultSkipped, metrics.ReasonNotCocoon); got != 1 {
		t.Errorf("mutate/skipped/not_cocoon = %v, want 1", got)
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	client := fake.NewSimpleClientset()
	return NewServer(client)
}

// collectAdmission reads metrics.AdmissionTotal off the collector directly —
// testutil would add a module not in go.mod.
func collectAdmission(t *testing.T) (series int, total float64) {
	t.Helper()
	ch := make(chan prometheus.Metric)
	go func() {
		metrics.AdmissionTotal.Collect(ch)
		close(ch)
	}()
	for m := range ch {
		var dm dto.Metric
		if err := m.Write(&dm); err != nil {
			t.Fatalf("write metric: %v", err)
		}
		series++
		total += dm.GetCounter().GetValue()
	}
	return series, total
}

func admissionValue(t *testing.T, handler, result, reason string) float64 {
	t.Helper()
	var dm dto.Metric
	if err := metrics.AdmissionTotal.WithLabelValues(handler, result, reason).Write(&dm); err != nil {
		t.Fatalf("write metric: %v", err)
	}
	return dm.GetCounter().GetValue()
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
