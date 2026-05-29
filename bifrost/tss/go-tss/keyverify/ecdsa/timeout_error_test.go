// TEMP: keyverify — remove this entire file with the keyverify rollout.
package ecdsa

import (
	"errors"
	"strings"
	"testing"

	"gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/blame"
)

// TestTimeoutError_IsBlameErrTssTimeOut pins the contract that
// LiveVerifier.Verify and TssKeyVerify.VerifyKey return errors that satisfy
// errors.Is(err, blame.ErrTssTimeOut). The scheduler's isTimeoutErr dispatch
// (cmd/bifrost/main.go) checks exactly this — a regression that broke the
// wrap would mean keyverify timeouts get treated as culprit failures and
// the scheduler would write spurious .fail markers for offline peers.
func TestTimeoutError_IsBlameErrTssTimeOut(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{
			name: "empty timeout (no culprits, no reason)",
			err:  &TimeoutError{},
		},
		{
			name: "timeout with reason only",
			err:  &TimeoutError{Reason: "no message of this node was produced"},
		},
		{
			name: "timeout with culprits only",
			err:  &TimeoutError{Culprits: []string{"thorpub_a", "thorpub_b"}},
		},
		{
			name: "timeout with both reason and culprits",
			err:  &TimeoutError{Reason: "no peer activity", Culprits: []string{"thorpub_c"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.err, blame.ErrTssTimeOut) {
				t.Fatalf("errors.Is(%v, blame.ErrTssTimeOut) = false, want true", tc.err)
			}
		})
	}
}

// TestTimeoutError_Error verifies the formatted output is well-formed across
// the four field-combination cases (with/without culprits, with/without
// reason). Callers' log lines and tests both rely on the sentinel timeout
// substring being present.
func TestTimeoutError_Error(t *testing.T) {
	sentinel := blame.ErrTssTimeOut.Error()

	cases := []struct {
		name      string
		err       *TimeoutError
		wantParts []string
	}{
		{
			name:      "bare timeout",
			err:       &TimeoutError{},
			wantParts: []string{sentinel},
		},
		{
			name:      "reason only",
			err:       &TimeoutError{Reason: "no message produced"},
			wantParts: []string{sentinel, "no message produced"},
		},
		{
			name:      "culprits only",
			err:       &TimeoutError{Culprits: []string{"thorpub_a"}},
			wantParts: []string{sentinel, "non-participating peers", "thorpub_a"},
		},
		{
			name:      "both",
			err:       &TimeoutError{Reason: "no peer activity", Culprits: []string{"thorpub_b", "thorpub_c"}},
			wantParts: []string{sentinel, "no peer activity", "thorpub_b", "thorpub_c"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.err.Error()
			for _, want := range tc.wantParts {
				if !strings.Contains(got, want) {
					t.Errorf("Error() = %q, missing substring %q", got, want)
				}
			}
		})
	}
}

// TestTimeoutError_Unwrap exposes the wrapped sentinel directly. errors.As
// against *TimeoutError on the outer error is the path the scheduler uses to
// extract culprits — assert both directions work.
func TestTimeoutError_Unwrap(t *testing.T) {
	original := &TimeoutError{Culprits: []string{"x", "y"}}
	var asErr *TimeoutError
	if !errors.As(error(original), &asErr) {
		t.Fatalf("errors.As(*TimeoutError) failed")
	}
	if len(asErr.Culprits) != 2 || asErr.Culprits[0] != "x" || asErr.Culprits[1] != "y" {
		t.Fatalf("errors.As recovered Culprits=%v, want [x y]", asErr.Culprits)
	}

	if original.Unwrap() != blame.ErrTssTimeOut {
		t.Fatalf("Unwrap() returned %v, want blame.ErrTssTimeOut", original.Unwrap())
	}
}
