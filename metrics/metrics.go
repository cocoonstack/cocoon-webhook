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
	labelPool     = "pool"
)

var (
	admissionTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "admission_total",
			Help:      "Number of admission decisions, by handler and decision.",
		},
		[]string{labelHandler, labelDecision},
	)

	affinityReservations = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "affinity_reservations_total",
			Help:      "Number of successful affinity reservations, by pool.",
		},
		[]string{labelPool},
	)

	affinityReleases = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "affinity_releases_total",
			Help:      "Number of orphan reservations released by the reaper, by pool.",
		},
		[]string{labelPool},
	)
)

// Register registers all webhook metrics with the given registerer.
func Register(reg prometheus.Registerer) {
	reg.MustRegister(admissionTotal, affinityReservations, affinityReleases)
}

// Handler returns the Prometheus metrics HTTP handler.
func Handler() http.Handler {
	return promhttp.Handler()
}

// RecordAdmission increments the admission counter for the given handler and decision.
func RecordAdmission(handler, decision string) {
	admissionTotal.WithLabelValues(handler, decision).Inc()
}

// RecordReservation increments the reservation counter for the given pool.
// Registered and exposed via /metrics, but not yet incremented by any caller;
// wiring for the affinity reservation path is TBD.
func RecordReservation(pool string) {
	affinityReservations.WithLabelValues(pool).Inc()
}

// RecordRelease increments the release counter for the given pool.
func RecordRelease(pool string) {
	affinityReleases.WithLabelValues(pool).Inc()
}
