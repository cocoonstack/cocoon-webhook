// Package metrics exposes cocoon-webhook Prometheus counters.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	// Handler label values, one per admission endpoint.
	HandlerMutate            = "mutate"
	HandlerValidate          = "validate"
	HandlerValidateCocoonSet = "validate_cocoonset"

	// Result label values. allow/deny/error are real adjudications; skipped
	// marks a request the webhook passed through without adjudicating.
	ResultAllow   = "allow"
	ResultDeny    = "deny"
	ResultError   = "error"
	ResultSkipped = "skipped"

	// Reason label values qualifying a skipped/error result; "" for adjudications.
	ReasonOperation = "operation"
	ReasonKind      = "kind"
	ReasonDecode    = "decode"
	ReasonNoParent  = "no_parent"
	ReasonNotCocoon = "not_cocoon"
	ReasonNoChange  = "no_change"

	metricNamespace = "cocoon"
	metricSubsystem = "webhook"

	labelHandler = "handler"
	labelResult  = "result"
	labelReason  = "reason"
)

// AdmissionTotal counts admission outcomes by handler, result, and reason.
var AdmissionTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: metricNamespace,
		Subsystem: metricSubsystem,
		Name:      "admission_total",
		Help:      "Number of admission outcomes, by handler, result, and reason.",
	},
	[]string{labelHandler, labelResult, labelReason},
)

// Register registers all webhook metrics with the given registerer.
func Register(reg prometheus.Registerer) {
	reg.MustRegister(AdmissionTotal)
}

// Handler returns the Prometheus metrics HTTP handler.
func Handler() http.Handler {
	return promhttp.Handler()
}

// RecordAdmission increments the admission counter. reason qualifies a
// skipped/error result and is "" for a real allow/deny.
func RecordAdmission(handler, result, reason string) {
	AdmissionTotal.WithLabelValues(handler, result, reason).Inc()
}
