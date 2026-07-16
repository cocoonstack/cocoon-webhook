package admission

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	cocoonv1 "github.com/cocoonstack/cocoon-common/apis/v1"
)

func TestValidateCocoonHibernationRejectsSecondLiveCR(t *testing.T) {
	srv := newHibernationServer(t, hibernation("existing", "pod-a", nil))
	resp := srv.validateCocoonHibernation(t.Context(), hibernationReview(t, admissionv1.Create, "new", "pod-a"))
	if resp.Allowed {
		t.Fatalf("second live CR on pod-a should be denied")
	}
	if !strings.Contains(resp.Result.Message, `"existing"`) {
		t.Errorf("denial should name the existing CR, got %q", resp.Result.Message)
	}
}

func TestValidateCocoonHibernationAllowsDeletingPredecessor(t *testing.T) {
	now := metav1.Now()
	srv := newHibernationServer(t, hibernation("existing", "pod-a", &now))
	resp := srv.validateCocoonHibernation(t.Context(), hibernationReview(t, admissionv1.Create, "new", "pod-a"))
	if !resp.Allowed {
		t.Errorf("deleting predecessor should admit the new CR, got %q", resp.Result.Message)
	}
}

func TestValidateCocoonHibernationAllowsDistinctPods(t *testing.T) {
	srv := newHibernationServer(t, hibernation("existing", "pod-a", nil))
	resp := srv.validateCocoonHibernation(t.Context(), hibernationReview(t, admissionv1.Create, "new", "pod-b"))
	if !resp.Allowed {
		t.Errorf("distinct pods should both be admitted, got %q", resp.Result.Message)
	}
}

func TestValidateCocoonHibernationSkipsNonCreate(t *testing.T) {
	srv := newHibernationServer(t, hibernation("existing", "pod-a", nil))
	resp := srv.validateCocoonHibernation(t.Context(), hibernationReview(t, admissionv1.Update, "existing", "pod-a"))
	if !resp.Allowed {
		t.Errorf("non-CREATE operations pass through, got %q", resp.Result.Message)
	}
}

func TestValidateCocoonHibernationFailsClosedOnListError(t *testing.T) {
	srv := newHibernationServer(t)
	srv.dyn.(*dynamicfake.FakeDynamicClient).PrependReactor("list", "cocoonhibernations", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("apiserver unavailable")
	})
	resp := srv.validateCocoonHibernation(t.Context(), hibernationReview(t, admissionv1.Create, "new", "pod-a"))
	if resp.Allowed {
		t.Errorf("list error should fail closed (deny)")
	}
}

func newHibernationServer(t *testing.T, objs ...runtime.Object) *Server {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := cocoonv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	return NewServer(nil, dynamicfake.NewSimpleDynamicClient(scheme, objs...), nil)
}

func hibernation(name, podName string, deleted *metav1.Time) *cocoonv1.CocoonHibernation {
	return &cocoonv1.CocoonHibernation{
		TypeMeta:   metav1.TypeMeta{APIVersion: cocoonv1.GroupVersion.String(), Kind: "CocoonHibernation"},
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: name, DeletionTimestamp: deleted},
		Spec: cocoonv1.CocoonHibernationSpec{
			PodRef: cocoonv1.HibernationPodRef{Name: podName},
			Desire: cocoonv1.HibernationDesireHibernate,
		},
	}
}

func hibernationReview(t *testing.T, op admissionv1.Operation, name, podName string) *admissionv1.AdmissionReview {
	t.Helper()
	raw, err := json.Marshal(hibernation(name, podName, nil))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return &admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			UID:       "uid",
			Namespace: "ns",
			Name:      name,
			Operation: op,
			Object:    runtime.RawExtension{Raw: raw},
		},
	}
}
