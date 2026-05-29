// TEMP: keyverify — remove this entire file with the keyverify rollout.
package ecdsa

import (
	"fmt"
	"strings"

	"gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/blame"
)

// TimeoutError reports a keyverify timeout enriched with the peers the blame
// manager identified as non-participating (when available) plus an optional
// reason describing where in the protocol the timeout fired.
//
// Always wraps blame.ErrTssTimeOut via Unwrap so existing
// errors.Is(err, blame.ErrTssTimeOut) checks continue to fire — the
// production scheduler's isTimeoutErr dispatch depends on this contract.
type TimeoutError struct {
	Culprits []string // thorpubs of peers identified as non-participating; may be empty
	Reason   string   // optional human-readable detail
}

func (e *TimeoutError) Error() string {
	parts := []string{blame.ErrTssTimeOut.Error()}
	if e.Reason != "" {
		parts = append(parts, e.Reason)
	}
	if len(e.Culprits) > 0 {
		parts = append(parts, fmt.Sprintf("non-participating peers: %v", e.Culprits))
	}
	return strings.Join(parts, ": ")
}

func (e *TimeoutError) Unwrap() error { return blame.ErrTssTimeOut }
