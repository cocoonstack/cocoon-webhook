package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/projecteru2/core/log"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cocoonstack/cocoon-common/meta"
)

// jsonPatch represents a single RFC 6902 JSON Patch operation.
type jsonPatch struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value"`
}

func allowResponse() *admissionv1.AdmissionResponse {
	return &admissionv1.AdmissionResponse{Allowed: true}
}

func denyResponse(msg string) *admissionv1.AdmissionResponse {
	return &admissionv1.AdmissionResponse{
		Allowed: false,
		Result:  &metav1.Status{Message: msg, Reason: metav1.StatusReasonForbidden},
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func decodeAdmissionReview(r *http.Request) (*admissionv1.AdmissionReview, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	var review admissionv1.AdmissionReview
	if err := json.Unmarshal(body, &review); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &review, nil
}

func writeJSON(w http.ResponseWriter, v any) {
	out, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "encode response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out) //nolint:gosec // marshaled JSON API response, not rendered as HTML
}

func hasCocoonToleration(tolerations []corev1.Toleration) bool {
	return meta.HasCocoonToleration(tolerations)
}

func replicaCount(p *int32) int32 {
	if p != nil {
		return *p
	}
	return 1
}

// parseConfigMapField extracts the value for a given key from a
// comma-separated "key:value" string (e.g. "node:cocoon-pool,pod:xxx").
func parseConfigMapField(data, key string) string {
	prefix := key + ":"
	for part := range strings.SplitSeq(data, ",") {
		if val, ok := strings.CutPrefix(part, prefix); ok {
			return val
		}
	}
	return ""
}

func escapeJSONPointer(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")
	return s
}

func logWarnAllocateSlot(ctx context.Context, ns, deployName string, err error) {
	log.WithFunc("deriveVMName").Warnf(ctx, "allocateSlot %s/%s: %v, skipping slot allocation", ns, deployName, err)
}
