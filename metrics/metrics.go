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

	// skipped marks a pass-through without adjudication; fail-closed failures are error.
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
