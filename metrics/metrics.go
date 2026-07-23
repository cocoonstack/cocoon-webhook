// Package metrics exposes cocoon-webhook Prometheus counters.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

const (
	HandlerMutate              = "mutate"
	HandlerValidate            = "validate"
	HandlerValidateCocoonSet   = "validate_cocoonset"
	HandlerValidateHibernation = "validate_cocoonhibernation"

	// Result label values. skipped marks a request passed through without
	// adjudicating (incl. fail-open decode); fail-closed failures are error.
	ResultAllow   = "allow"
	ResultDeny    = "deny"
	ResultError   = "error"
	ResultSkipped = "skipped"

	// Reason label values qualifying skipped/error; "" only for allow/deny.
	ReasonOperation   = "operation"
	ReasonKind        = "kind"
	ReasonDecode      = "decode"
	ReasonNoParent    = "no_parent"
	ReasonNotCocoon   = "not_cocoon"
	ReasonNoChange    = "no_change"
	ReasonParentFetch = "parent_fetch"
	ReasonList        = "list"

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

// RecordAdmission increments the admission counter. reason qualifies a
// skipped/error result and is "" for a real allow/deny.
func RecordAdmission(handler, result, reason string) {
	AdmissionTotal.WithLabelValues(handler, result, reason).Inc()
}
