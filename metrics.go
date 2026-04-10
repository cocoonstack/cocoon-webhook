package main

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metric labels and names. Kept as constants so handlers and tests
// agree on the wire shape.
const (
	metricNamespace = "cocoon"
	metricSubsystem = "webhook"

	labelHandler  = "handler"
	labelDecision = "decision"
	labelPool     = "pool"

	HandlerMutate          = "mutate"
	HandlerValidate        = "validate"
	HandlerCocoonSetValid  = "validate_cocoonset"
	DecisionAllow          = "allow"
	DecisionDeny           = "deny"
	DecisionError          = "error"
	DecisionAffinityFailed = "affinity_failed"
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

// RegisterMetrics installs the collectors against the supplied
// registry. Tests pass an isolated registry; production code calls
// this once from main with prometheus.DefaultRegisterer.
func RegisterMetrics(reg prometheus.Registerer) {
	reg.MustRegister(admissionTotal, affinityReservations, affinityReleases)
}

// metricsHandler returns the HTTP handler that exposes the
// prometheus collectors. It is mounted on its own listener so the
// admission TLS port stays focused on AdmissionReview traffic.
func metricsHandler() http.Handler {
	return promhttp.Handler()
}

// recordAdmission increments the admission counter.
func recordAdmission(handler, decision string) {
	admissionTotal.WithLabelValues(handler, decision).Inc()
}

// recordReservation increments the per-pool reservation counter.
func recordReservation(pool string) {
	affinityReservations.WithLabelValues(pool).Inc()
}

// recordRelease increments the per-pool release counter.
func recordRelease(pool string) {
	affinityReleases.WithLabelValues(pool).Inc()
}
