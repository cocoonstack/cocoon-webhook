// Package admission implements the cocoon-webhook mutate and validate
// handlers (pods, workloads, and CocoonSet CRs).
package admission

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/projecteru2/core/log"
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
	mux.HandleFunc("/healthz", okHandler("ok"))
	mux.HandleFunc("/readyz", okHandler("ready"))
	return mux
}

func okHandler(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}
}

func admit(handler commonadmission.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { commonadmission.Serve(w, r, 0, handler) }
}

func recordAllow(handler, result, reason string) *admissionv1.AdmissionResponse {
	metrics.AdmissionTotal.WithLabelValues(handler, result, reason).Inc()
	return commonadmission.Allow()
}

func recordDeny(handler, result, reason, msg string) *admissionv1.AdmissionResponse {
	metrics.AdmissionTotal.WithLabelValues(handler, result, reason).Inc()
	return commonadmission.Deny(msg)
}

func denyf(ctx context.Context, logger *log.Fields, handler string, req *admissionv1.AdmissionRequest, msg string) *admissionv1.AdmissionResponse {
	logger.Warnf(ctx, "validate %s/%s DENY: %s", req.Namespace, req.Name, msg)
	return recordDeny(handler, metrics.ResultDeny, "", msg)
}

func decodeOrDeny(ctx context.Context, logger *log.Fields, handler, kind string, req *admissionv1.AdmissionRequest, out any) *admissionv1.AdmissionResponse {
	if err := json.Unmarshal(req.Object.Raw, out); err != nil {
		logger.Errorf(ctx, err, "decode %s %s/%s", strings.ToLower(kind), req.Namespace, req.Name)
		return recordDeny(handler, metrics.ResultError, metrics.ReasonDecode, fmt.Sprintf("decode %s: %v", kind, err))
	}
	return nil
}
