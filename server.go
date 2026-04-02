package main

import (
	"net/http"

	"k8s.io/client-go/kubernetes"

	"github.com/cocoonstack/cocoon-common/meta"
)

// Constants for ConfigMap name, annotation key, and toleration key used to
// identify and schedule cocoon VM workloads.
const (
	affinityConfigMap = "cocoon-vm-affinity"
	vmNameAnnotation  = meta.AnnotationVMName
)

type webhookServer struct {
	clientset kubernetes.Interface
}

func newWebhookServer(clientset kubernetes.Interface) *webhookServer {
	return &webhookServer{clientset: clientset}
}

func (s *webhookServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mutate", s.handleMutate)
	mux.HandleFunc("/validate", s.handleValidate)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

// handleMutate processes mutating admission requests for Pod CREATE operations.
func (s *webhookServer) handleMutate(w http.ResponseWriter, r *http.Request) {
	review, err := decodeAdmissionReview(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	review.Response = mutate(r.Context(), s.clientset, review.Request)
	review.Response.UID = review.Request.UID

	writeJSON(w, review)
}

// handleValidate processes validating admission requests for scale-down protection.
func (s *webhookServer) handleValidate(w http.ResponseWriter, r *http.Request) {
	review, err := decodeAdmissionReview(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	review.Response = validate(r.Context(), review.Request)
	review.Response.UID = review.Request.UID

	writeJSON(w, review)
}
