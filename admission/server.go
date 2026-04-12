package admission

import (
	"net/http"

	"k8s.io/client-go/kubernetes"

	commonadmission "github.com/cocoonstack/cocoon-common/k8s/admission"
	"github.com/cocoonstack/cocoon-webhook/affinity"
)

type Server struct {
	store  affinity.Store
	client kubernetes.Interface
}

func NewServer(store affinity.Store, client kubernetes.Interface) *Server {
	return &Server{store: store, client: client}
}

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
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}

func (s *Server) handleMutate(w http.ResponseWriter, r *http.Request) {
	commonadmission.Serve(w, r, 0, s.mutatePod)
}

func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	commonadmission.Serve(w, r, 0, s.validateWorkload)
}

func (s *Server) handleValidateCocoonSet(w http.ResponseWriter, r *http.Request) {
	commonadmission.Serve(w, r, 0, s.validateCocoonSet)
}
