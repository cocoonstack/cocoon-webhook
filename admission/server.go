// Package admission implements the cocoon-webhook mutate and validate
// handlers (pods, workloads, and CocoonSet CRs).
package admission

import (
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	commonadmission "github.com/cocoonstack/cocoon-common/k8s/admission"
	"github.com/cocoonstack/cocoon-webhook/metrics"
)

// Server is the admission webhook HTTP server that handles mutate and validate requests.
type Server struct {
	client      kubernetes.Interface
	dyn         dynamic.Interface
	podCreators []string
}

// NewServer creates an admission Server; dyn reads CocoonHibernation CRs,
// podCreators lists the usernames allowed to create cocoon pods.
func NewServer(client kubernetes.Interface, dyn dynamic.Interface, podCreators []string) *Server {
	return &Server{client: client, dyn: dyn, podCreators: podCreators}
}

// Routes returns the HTTP handler with all admission webhook routes registered.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mutate", admit(s.mutatePod))
	mux.HandleFunc("/validate", admit(s.validateWorkload))
	mux.HandleFunc("/validate-cocoonset", admit(s.validateCocoonSet))
	mux.HandleFunc("/validate-cocoonhibernation", admit(s.validateCocoonHibernation))
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}

func admit(handler commonadmission.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { commonadmission.Serve(w, r, 0, handler) }
}

// recordAllow and recordDeny pair the admission counter with the response so
// every handler exit records exactly one sample.
func recordAllow(handler, result, reason string) *admissionv1.AdmissionResponse {
	metrics.RecordAdmission(handler, result, reason)
	return commonadmission.Allow()
}

func recordDeny(handler, result, reason, msg string) *admissionv1.AdmissionResponse {
	metrics.RecordAdmission(handler, result, reason)
	return commonadmission.Deny(msg)
}
