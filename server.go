package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/projecteru2/core/log"
	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/client-go/kubernetes"
)

const maxAdmissionBody = 10 << 20 // 10 MiB upper bound on request body size.

// Server hosts the admission webhook HTTP handlers. Dependencies are
// injected so each handler stays trivially testable.
type Server struct {
	clientset kubernetes.Interface
}

// NewServer constructs a Server with the supplied dependencies.
func NewServer(clientset kubernetes.Interface) *Server {
	return &Server{clientset: clientset}
}

// Routes returns the HTTP handler exposing every webhook endpoint.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mutate", s.handleMutate)
	mux.HandleFunc("/validate", s.handleValidate)
	mux.HandleFunc("/validate-cocoonset", s.handleValidateCocoonSet)
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	// Distinguish liveness (always ok once the binary is up) from readiness
	// (ok only once the dependencies the webhook needs to serve traffic are
	// reachable). For now both are trivially ok; later commits will plumb
	// real probes through s.clientset.
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}

func (s *Server) handleMutate(w http.ResponseWriter, r *http.Request) {
	s.serveAdmission(w, r, func(_ context.Context, review *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
		// Subsequent commits replace this with mutatePod which writes the
		// VMSpec contract and pins the pod to a sticky cocoon node.
		_ = review
		return allowResponse()
	})
}

func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	s.serveAdmission(w, r, func(_ context.Context, review *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
		// Subsequent commits replace this with validateWorkload which blocks
		// destructive scale-down on cocoon Deployments / StatefulSets.
		_ = review
		return allowResponse()
	})
}

func (s *Server) handleValidateCocoonSet(w http.ResponseWriter, r *http.Request) {
	s.serveAdmission(w, r, func(_ context.Context, review *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
		// Subsequent commits replace this with validateCocoonSet which
		// rejects malformed CocoonSet specs ahead of the operator.
		_ = review
		return allowResponse()
	})
}

// serveAdmission decodes an AdmissionReview, dispatches it to the
// supplied admission function, copies the request UID onto the
// response (required by the API server), and writes the response.
func (s *Server) serveAdmission(
	w http.ResponseWriter,
	r *http.Request,
	admit func(context.Context, *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse,
) {
	logger := log.WithFunc("serveAdmission")
	review, err := decodeAdmissionReview(r)
	if err != nil {
		logger.Warn(r.Context(), "decode admission review")
		http.Error(w, "decode admission review", http.StatusBadRequest)
		return
	}
	resp := admit(r.Context(), review)
	if resp == nil {
		resp = allowResponse()
	}
	resp.UID = review.Request.UID
	review.Response = resp

	out, err := json.Marshal(review)
	if err != nil {
		logger.Error(r.Context(), err, "marshal admission review")
		http.Error(w, "encode response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out) //nolint:gosec // marshaled JSON API response, not rendered as HTML
}

func decodeAdmissionReview(r *http.Request) (*admissionv1.AdmissionReview, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxAdmissionBody))
	if err != nil {
		return nil, err
	}
	var review admissionv1.AdmissionReview
	if err := json.Unmarshal(body, &review); err != nil {
		return nil, err
	}
	return &review, nil
}
