// TEMP: keyverify — remove this entire file with the keyverify rollout.
package ecdsa

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// keyverifyRoundCount is the number of round messages each peer is expected
// to deliver in one attempt (KEYVERIFY1, 2a, 2b, 3).
const keyverifyRoundCount = 4

// CulpritDetail is the per-culprit enrichment attached to a keyverify
// timeout (design Part B). The accepted-vs-wire split is deliberate:
// "accepted" lags wire receipt by the broadcast hash-confirm threshold, so
// under confirm loss a peer can have LastWireRound > LastAcceptedRound —
// "their messages arrived but confirms didn't assemble" — which is a
// different failure than total silence (LastSeenAt zero).
type CulpritDetail struct {
	PubKey            string    `json:"pubkey"`
	LastAcceptedRound string    `json:"last_accepted_round"` // last round whose share was accepted, or "none"
	LastWireRound     string    `json:"last_wire_round"`     // last round seen at the wire level, or "none"
	LastSeenAt        time.Time `json:"last_seen_at"`        // zero if no wire message ever arrived
	SharesAccepted    int       `json:"shares_accepted"`
	SharesExpected    int       `json:"shares_expected"`
}

var (
	metricKeyverifyRetry = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tss_keyverify_retry_total",
		Help: "keyverify recovery-ticker actions performed, labeled by vault pubkey and action (unicast_resend|broadcast_resend|confirm_rebroadcast|share_rerequest)",
	}, []string{"pubkey", "action"})

	metricKeyverifyRecovered = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tss_keyverify_recovered_total",
		Help: "keyverify broadcast shares accepted after at least one recovery action, labeled by vault pubkey and round",
	}, []string{"pubkey", "round"})
)

func init() {
	prometheus.MustRegister(metricKeyverifyRetry, metricKeyverifyRecovered)
}
