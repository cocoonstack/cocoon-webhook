package admission

import (
	"encoding/json"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/cocoonstack/cocoon-common/meta"
	"github.com/cocoonstack/cocoon-webhook/affinity"
)

func TestCheckScaleDownAllowsScaleUp(t *testing.T) {
	resp := checkScaleDown(t.Context(), &admissionv1.AdmissionRequest{
		Kind:      metav1.GroupVersionKind{Kind: "Deployment"},
		Namespace: "ns", Name: "demo",
	}, 2, 5)
	if !resp.Allowed {
		t.Errorf("scale-up should be allowed")
	}
}

func TestCheckScaleDownAllowsEqual(t *testing.T) {
	resp := checkScaleDown(t.Context(), &admissionv1.AdmissionRequest{
		Kind:      metav1.GroupVersionKind{Kind: "Deployment"},
		Namespace: "ns", Name: "demo",
	}, 3, 3)
	if !resp.Allowed {
		t.Errorf("equal replicas should be allowed")
	}
}

func TestCheckScaleDownDeniesDecrement(t *testing.T) {
	resp := checkScaleDown(t.Context(), &admissionv1.AdmissionRequest{
		Kind:      metav1.GroupVersionKind{Kind: "Deployment"},
		Namespace: "ns", Name: "demo",
	}, 5, 2)
	if resp.Allowed {
		t.Errorf("scale-down should be denied")
	}
	if resp.Result == nil || resp.Result.Reason != metav1.StatusReasonForbidden {
		t.Errorf("denial reason: %+v", resp.Result)
	}
}

func TestValidateDeploymentScaleDownBlocked(t *testing.T) {
	old := newDeployment(5, true)
	updated := newDeployment(2, true)
	resp := validateScaleDown[appsv1.Deployment](t.Context(), buildUpdateReview(t, "Deployment", old, updated).Request)
	if resp.Allowed {
		t.Errorf("cocoon scale-down should be blocked")
	}
}

func TestValidateDeploymentScaleAllowsNonCocoon(t *testing.T) {
	old := newDeployment(5, false)
	updated := newDeployment(2, false)
	resp := validateScaleDown[appsv1.Deployment](t.Context(), buildUpdateReview(t, "Deployment", old, updated).Request)
	if !resp.Allowed {
		t.Errorf("non-cocoon deployment should pass through")
	}
}

func TestValidateStatefulSetScaleDownBlocked(t *testing.T) {
	old := newStatefulSet(5, true)
	updated := newStatefulSet(2, true)
	resp := validateScaleDown[appsv1.StatefulSet](t.Context(), buildUpdateReview(t, "StatefulSet", old, updated).Request)
	if resp.Allowed {
		t.Errorf("cocoon statefulset scale-down should be blocked")
	}
}

func TestServerValidateWorkloadIgnoresCreate(t *testing.T) {
	srv := newTestServer(t)
	review := buildUpdateReview(t, "Deployment", newDeployment(5, true), newDeployment(2, true))
	review.Request.Operation = admissionv1.Create
	resp := srv.validateWorkload(t.Context(), review)
	if !resp.Allowed {
		t.Errorf("CREATE operation should pass through")
	}
}

func TestServerValidateWorkloadDeploymentScaleSubresourceDenied(t *testing.T) {
	srv := newServerWithObjects(t, newDeployment(5, true))
	review := buildScaleReview(t, "deployments", 5, 2)
	resp := srv.validateWorkload(t.Context(), review)
	if resp.Allowed {
		t.Errorf("scale-down via /scale subresource should be denied")
	}
}

func TestServerValidateWorkloadDeploymentScaleSubresourceUpAllowed(t *testing.T) {
	srv := newServerWithObjects(t, newDeployment(2, true))
	review := buildScaleReview(t, "deployments", 2, 5)
	resp := srv.validateWorkload(t.Context(), review)
	if !resp.Allowed {
		t.Errorf("scale-up via /scale subresource should be allowed")
	}
}

func TestServerValidateWorkloadStatefulSetScaleSubresourceDenied(t *testing.T) {
	srv := newServerWithObjects(t, newStatefulSet(5, true))
	review := buildScaleReview(t, "statefulsets", 5, 2)
	resp := srv.validateWorkload(t.Context(), review)
	if resp.Allowed {
		t.Errorf("statefulset scale-down via /scale subresource should be denied")
	}
}

func TestServerValidateWorkloadScaleSubresourceNonCocoonAllowed(t *testing.T) {
	srv := newServerWithObjects(t, newDeployment(5, false))
	review := buildScaleReview(t, "deployments", 5, 2)
	resp := srv.validateWorkload(t.Context(), review)
	if !resp.Allowed {
		t.Errorf("non-cocoon scale-down via /scale subresource should pass through")
	}
}

func TestServerValidateWorkloadScaleSubresourceParentMissingAllowed(t *testing.T) {
	srv := newServerWithObjects(t)
	review := buildScaleReview(t, "deployments", 5, 2)
	resp := srv.validateWorkload(t.Context(), review)
	if !resp.Allowed {
		t.Errorf("missing parent should fail-open")
	}
}

func newServerWithObjects(t *testing.T, objs ...runtime.Object) *Server {
	t.Helper()
	client := fake.NewSimpleClientset(objs...)
	store := affinity.NewConfigMapStore(client, fixedNodePicker("node-test"))
	return NewServer(store, client)
}

func buildScaleReview(t *testing.T, resource string, oldReplicas, newReplicas int32) *admissionv1.AdmissionReview {
	t.Helper()
	oldScale := autoscalingv1.Scale{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "demo"},
		Spec:       autoscalingv1.ScaleSpec{Replicas: oldReplicas},
	}
	newScale := autoscalingv1.Scale{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "demo"},
		Spec:       autoscalingv1.ScaleSpec{Replicas: newReplicas},
	}
	oldRaw, err := json.Marshal(&oldScale)
	if err != nil {
		t.Fatalf("marshal old scale: %v", err)
	}
	newRaw, err := json.Marshal(&newScale)
	if err != nil {
		t.Fatalf("marshal new scale: %v", err)
	}
	return &admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			UID:         "test-uid",
			Kind:        metav1.GroupVersionKind{Group: "autoscaling", Version: "v1", Kind: "Scale"},
			Resource:    metav1.GroupVersionResource{Group: "apps", Version: "v1", Resource: resource},
			SubResource: "scale",
			Namespace:   "ns",
			Name:        "demo",
			Operation:   admissionv1.Update,
			Object:      runtime.RawExtension{Raw: newRaw},
			OldObject:   runtime.RawExtension{Raw: oldRaw},
		},
	}
}

func newDeployment(replicas int32, cocoon bool) *appsv1.Deployment {
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "demo"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
		},
	}
	if cocoon {
		d.Spec.Template.Spec.Tolerations = []corev1.Toleration{{Key: meta.TolerationKey}}
	}
	return d
}

func newStatefulSet(replicas int32, cocoon bool) *appsv1.StatefulSet {
	s := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "demo"},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
		},
	}
	if cocoon {
		s.Spec.Template.Spec.Tolerations = []corev1.Toleration{{Key: meta.TolerationKey}}
	}
	return s
}

func buildUpdateReview(t *testing.T, kind string, oldObj, newObj any) *admissionv1.AdmissionReview {
	t.Helper()
	oldRaw, err := json.Marshal(oldObj)
	if err != nil {
		t.Fatalf("marshal old: %v", err)
	}
	newRaw, err := json.Marshal(newObj)
	if err != nil {
		t.Fatalf("marshal new: %v", err)
	}
	return &admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			UID:       "test-uid",
			Kind:      metav1.GroupVersionKind{Kind: kind, Group: "apps", Version: "v1"},
			Namespace: "ns",
			Name:      "demo",
			Operation: admissionv1.Update,
			Object:    runtime.RawExtension{Raw: newRaw},
			OldObject: runtime.RawExtension{Raw: oldRaw},
		},
	}
}
