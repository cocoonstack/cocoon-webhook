// Package metrics exposes cocoon-webhook Prometheus counters.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	// HandlerMutate is the label value for the mutating admission handler.
	HandlerMutate = "mutate"
	// HandlerValidate is the label value for the validating admission handler.
	HandlerValidate = "validate"
	// HandlerValidateCocoonSet is the label value for CocoonSet validation.
	HandlerValidateCocoonSet = "validate_cocoonset"

	// DecisionAllow is the label value for an allowed admission decision.
	DecisionAllow = "allow"
	// DecisionDeny is the label value for a denied admission decision.
	DecisionDeny = "deny"
	// DecisionError is the label value for an errored admission decision.
	DecisionError = "error"

	metricNamespace = "cocoon"
	metricSubsystem = "webhook"

	labelHandler  = "handler"
	labelDecision = "decision"
)

var admissionTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: metricNamespace,
		Subsystem: metricSubsystem,
		Name:      "admission_total",
		Help:      "Number of admission decisions, by handler and decision.",
	},
	[]string{labelHandler, labelDecision},
)

// Register registers all webhook metrics with the given registerer.
func Register(reg prometheus.Registerer) {
	reg.MustRegister(admissionTotal)
}

// Handler returns the Prometheus metrics HTTP handler.
func Handler() http.Handler {
	return promhttp.Handler()
}

// RecordAdmission increments the admission counter for the given handler and decision.
func RecordAdmission(handler, decision string) {
	admissionTotal.WithLabelValues(handler, decision).Inc()
}
