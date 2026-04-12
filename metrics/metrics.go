package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	metricNamespace = "cocoon"
	metricSubsystem = "webhook"

	labelHandler  = "handler"
	labelDecision = "decision"
	labelPool     = "pool"

	HandlerMutate            = "mutate"
	HandlerValidate          = "validate"
	HandlerValidateCocoonSet = "validate_cocoonset"
	DecisionAllow            = "allow"
	DecisionDeny             = "deny"
	DecisionError            = "error"
	DecisionAffinityFailed   = "affinity_failed"
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

func Register(reg prometheus.Registerer) {
	reg.MustRegister(admissionTotal, affinityReservations, affinityReleases)
}

func Handler() http.Handler {
	return promhttp.Handler()
}

func RecordAdmission(handler, decision string) {
	admissionTotal.WithLabelValues(handler, decision).Inc()
}

func RecordReservation(pool string) {
	affinityReservations.WithLabelValues(pool).Inc()
}

func RecordRelease(pool string) {
	affinityReleases.WithLabelValues(pool).Inc()
}
