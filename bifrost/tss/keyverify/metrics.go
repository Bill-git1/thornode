// TEMP: keyverify — remove this entire file with the keyverify rollout.
package keyverify

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	metricAttempts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tss_keyverify_attempts_total",
		Help: "tss-lib keyverify attempts started, labeled by vault pubkey",
	}, []string{"pubkey"})

	metricSuccess = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tss_keyverify_success_total",
		Help: "tss-lib keyverify successful completions, labeled by vault pubkey",
	}, []string{"pubkey"})

	metricFailure = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tss_keyverify_failure_total",
		Help: "tss-lib keyverify failed attempts, labeled by vault pubkey and kind (timeout|culprit|keyshare_missing|other|panic)",
	}, []string{"pubkey", "kind"})

	metricCulprit = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tss_keyverify_culprit_total",
		Help: "tss-lib keyverify culprit blame counts, labeled by vault pubkey and culprit pubkey",
	}, []string{"pubkey", "culprit_pubkey"})

	metricDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "tss_keyverify_duration_seconds",
		Help:    "tss-lib keyverify attempt durations, labeled by vault pubkey and outcome",
		Buckets: []float64{.1, .5, 1, 2.5, 5, 10, 20, 30, 45, 60, 90, 120},
	}, []string{"pubkey", "outcome"})
)

func init() {
	prometheus.MustRegister(
		metricAttempts,
		metricSuccess,
		metricFailure,
		metricCulprit,
		metricDuration,
	)
}
