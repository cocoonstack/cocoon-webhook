package admission

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

	cocoonv1 "github.com/cocoonstack/cocoon-common/apis/v1"
	"github.com/cocoonstack/cocoon-common/meta"
	"github.com/cocoonstack/cocoon-webhook/metrics"
)

const testPodCreator = "system:serviceaccount:cocoon-system:cocoon-operator"

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

func TestMutatePodDeniesForgedOwnerFromOtherCreator(t *testing.T) {
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
	review := buildPodReview(t, pod)
	review.Request.UserInfo.Username = "system:serviceaccount:ns:attacker"
	resp := srv.mutatePod(t.Context(), review)
	if resp.Allowed {
		t.Errorf("cocoonset owner ref forged by a non-controller creator should be denied")
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

func TestMutatePodDeniesBareVMPodWithoutToleration(t *testing.T) {
	srv := newTestServer(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "rogue",
			Namespace:   "tenant",
			Annotations: map[string]string{meta.AnnotationVMName: "vk-tenant-rogue"},
		},
		Spec: corev1.PodSpec{NodeName: "cocoon-pool-node-1"},
	}
	if resp := srv.mutatePod(t.Context(), buildPodReview(t, pod)); resp.Allowed {
		t.Error("a pod that names a VM must pass the CocoonSet gate even without the toleration")
	}
}

func TestMutatePodDeniesVMPodFromOtherCreatorWithoutToleration(t *testing.T) {
	srv := newTestServer(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "rogue",
			Namespace:       "tenant",
			Annotations:     map[string]string{meta.AnnotationVMName: "vk-tenant-rogue"},
			OwnerReferences: []metav1.OwnerReference{{Kind: meta.KindCocoonSet, Name: "demo"}},
		},
		Spec: corev1.PodSpec{NodeName: "cocoon-pool-node-1"},
	}
	review := buildPodReview(t, pod)
	review.Request.UserInfo.Username = "system:serviceaccount:tenant:default"
	resp := srv.mutatePod(t.Context(), review)
	if resp.Allowed || !strings.Contains(resp.Result.Message, "created by the CocoonSet controller") {
		t.Errorf("a pod naming a VM with a forged owner must reach the creator check without the toleration, got %v", resp.Result)
	}
}

func TestMutatePodUpdateDeniesAPodThatEntersTheGate(t *testing.T) {
	srv := newTestServer(t)
	old := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "rogue", Namespace: "tenant"}, Spec: corev1.PodSpec{NodeName: "cocoon-pool-node-1"}}
	for name, mutate := range map[string]func(*corev1.Pod){
		"toleration appended": func(pod *corev1.Pod) {
			pod.Spec.Tolerations = []corev1.Toleration{{Key: meta.TolerationKey, Operator: corev1.TolerationOpExists}}
		},
		"vm name added": func(pod *corev1.Pod) {
			pod.Annotations = map[string]string{meta.AnnotationVMName: "vk-tenant-rogue"}
		},
	} {
		updated := old.DeepCopy()
		mutate(updated)
		review := buildPodUpdateReview(t, old, updated)
		review.Request.UserInfo.Username = "system:serviceaccount:tenant:default"
		if resp := srv.mutatePod(t.Context(), review); resp.Allowed {
			t.Errorf("%s: an UPDATE that moves a pod into the gate must pass the CocoonSet checks", name)
		}
	}
}

func TestMutatePodUpdateSkipsAPodAlreadyInTheGate(t *testing.T) {
	srv := newTestServer(t)
	old := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-0", Namespace: "ns", Annotations: map[string]string{meta.AnnotationVMName: "vk-ns-demo-0"}},
		Spec:       corev1.PodSpec{Tolerations: []corev1.Toleration{{Key: meta.TolerationKey}}},
	}
	updated := old.DeepCopy()
	updated.Annotations[meta.AnnotationVMID] = "vmid-1"
	review := buildPodUpdateReview(t, old, updated)
	review.Request.UserInfo.Username = "system:serviceaccount:cocoon-system:vk-cocoon"
	if resp := srv.mutatePod(t.Context(), review); !resp.Allowed {
		t.Errorf("a runtime patch on a pod already inside the gate must pass, got %v", resp.Result)
	}
}

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

func newTestServer(t *testing.T, objs ...runtime.Object) *Server {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := cocoonv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	return NewServer(fake.NewSimpleClientset(), dynamicfake.NewSimpleDynamicClient(scheme, objs...), []string{testPodCreator})
}

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

func buildPodUpdateReview(t *testing.T, old, updated *corev1.Pod) *admissionv1.AdmissionReview {
	t.Helper()
	oldRaw, err := json.Marshal(old)
	if err != nil {
		t.Fatalf("marshal old pod: %v", err)
	}
	review := buildPodReview(t, updated)
	review.Request.Operation = admissionv1.Update
	review.Request.OldObject = runtime.RawExtension{Raw: oldRaw}
	return review
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
			UserInfo:  authenticationv1.UserInfo{Username: testPodCreator},
			Object:    runtime.RawExtension{Raw: raw},
		},
	}
}
