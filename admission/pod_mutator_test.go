package admission

import (
	"context"
	"encoding/json"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/cocoonstack/cocoon-common/meta"
	"github.com/cocoonstack/cocoon-webhook/affinity"
)

func TestPodNodePoolPrecedence(t *testing.T) {
	cases := []struct {
		name string
		pod  corev1.Pod
		want string
	}{
		{
			name: "default when nothing set",
			pod:  corev1.Pod{},
			want: "default",
		},
		{
			name: "annotation",
			pod: corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{meta.LabelNodePool: "ann"}},
			},
			want: "ann",
		},
		{
			name: "label beats annotation",
			pod: corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      map[string]string{meta.LabelNodePool: "label"},
					Annotations: map[string]string{meta.LabelNodePool: "ann"},
				},
			},
			want: "label",
		},
		{
			name: "nodeSelector beats label",
			pod: corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{meta.LabelNodePool: "label"},
				},
				Spec: corev1.PodSpec{NodeSelector: map[string]string{meta.LabelNodePool: "selector"}},
			},
			want: "selector",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pod := c.pod
			if got := podNodePool(&pod); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestEscapeJSONPointer(t *testing.T) {
	cases := map[string]string{
		"vm.cocoonstack.io/name": "vm.cocoonstack.io~1name",
		"a/b~c":                  "a~1b~0c",
		"plain":                  "plain",
	}
	for in, want := range cases {
		if got := escapeJSONPointer(in); got != want {
			t.Errorf("escape %q = %q, want %q", in, got, want)
		}
	}
}

func TestBuildMutatePatchAddsAnnotationsMap(t *testing.T) {
	pod := &corev1.Pod{}
	res := affinity.Reservation{VMName: "vk-ns-demo-0", Node: "node-a"}
	patch, err := buildMutatePatch(pod, res)
	if err != nil {
		t.Fatalf("buildMutatePatch: %v", err)
	}
	var ops []map[string]any
	if err := json.Unmarshal(patch, &ops); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if len(ops) != 3 {
		t.Fatalf("expected 3 ops (add annotations + vmname + nodeName), got %d", len(ops))
	}
	if ops[0]["path"] != "/metadata/annotations" {
		t.Errorf("first op should add annotations object, got %+v", ops[0])
	}
	if ops[2]["path"] != "/spec/nodeName" || ops[2]["value"] != "node-a" {
		t.Errorf("third op should pin nodeName, got %+v", ops[2])
	}
}

func TestBuildMutatePatchSkipsNodeWhenEmpty(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"x": "y"}}}
	res := affinity.Reservation{VMName: "vk-ns-demo-0", Node: ""}
	patch, err := buildMutatePatch(pod, res)
	if err != nil {
		t.Fatalf("buildMutatePatch: %v", err)
	}
	var ops []map[string]any
	if err := json.Unmarshal(patch, &ops); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected just the vmname op, got %d", len(ops))
	}
	if ops[0]["value"] != "vk-ns-demo-0" {
		t.Errorf("vmname op value: %v", ops[0]["value"])
	}
}

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

func TestMutatePodPatchesCocoonPod(t *testing.T) {
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
	if !resp.Allowed {
		t.Errorf("cocoon pod should be allowed")
	}
	if len(resp.Patch) == 0 {
		t.Fatalf("cocoon pod should be patched")
	}
	var ops []map[string]any
	if err := json.Unmarshal(resp.Patch, &ops); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if len(ops) < 2 {
		t.Fatalf("expected at least 2 ops, got %d", len(ops))
	}
}

// fixedNodePicker is a NodePicker test helper local to the admission
// package. The affinity package has its own copy under _test.go that
// cannot be imported across packages, so we duplicate the trivial
// four-line type rather than expose a test helper from production.
type fixedNodePicker string

func (n fixedNodePicker) Pick(_ context.Context, _ string) (string, error) {
	return string(n), nil
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	client := fake.NewSimpleClientset()
	store := affinity.NewConfigMapStore(client, fixedNodePicker("node-test"))
	return NewServer(client, store)
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
