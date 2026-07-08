//go:build mocknet
// +build mocknet

package tss

import (
	"strings"
	"testing"
	"time"

	"gitlab.com/thorchain/thornode/v3/bifrost/p2p"
	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/conversion"
	tcommon "gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/common"
)

// TestFrostKeygenRound3TestTimeoutOverrideShrinksOnlyRound3Wait covers the
// fault-injection hook used by the xmr-keygen-jail simulation: with
// XMR_FROST_KEYGEN_TEST_ROUND3_TIMEOUT_MS set, the round-3 wait times out on
// the override instead of the coordinator timeout, and the blame is a normal
// round-3 timeout blame. Unset or invalid values must be a no-op (covered by
// every other test in this file running without the variable).
func TestFrostKeygenRound3TestTimeoutOverrideShrinksOnlyRound3Wait(t *testing.T) {
	ensureBech32Prefix()
	t.Setenv("XMR_FROST_KEYGEN_TEST_ROUND3_TIMEOUT_MS", "50")

	members := []string{testPubKeys[0], testPubKeys[1]}
	_, participantIndexMap, signerByNormalized, err := buildParticipantIndex(members)
	if err != nil {
		t.Fatalf("build participant index: %v", err)
	}
	activeSet, err := buildActiveSet(members, signerByNormalized)
	if err != nil {
		t.Fatalf("build active set: %v", err)
	}
	peerIDs, err := conversion.GetPeerIDsFromPubKeys(members)
	if err != nil {
		t.Fatalf("peer ids: %v", err)
	}

	// Coordinator timeout is far beyond the test budget; only the override can
	// end the round-3 wait this quickly.
	coord := newTestFrostKeygenCoordinator(testPubKeys[0], "msg-id", make(chan *p2p.Message, 2), time.Hour)
	start := time.Now()
	_, _, _, failResp := coord.exchangeRound3(activeSet, participantIndexMap, peerIDs, FrostDKGRound3Payload{
		SenderPubKey:     testPubKeys[0],
		ParticipantIndex: participantIndexMap[normalizePubKey(testPubKeys[0])],
		Address:          "48addr",
		GroupKey:         strings.Repeat("ab", 32),
	})
	elapsed := time.Since(start)

	if failResp == nil {
		t.Fatalf("expected round3 timeout failure under test override")
	}
	if failResp.Status != tcommon.Fail {
		t.Fatalf("expected fail status, got %d", failResp.Status)
	}
	if failResp.Blame.Round != frostDKGRound3 {
		t.Fatalf("expected blame round %s, got %s", frostDKGRound3, failResp.Blame.Round)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("round3 wait ignored the test override, took %s", elapsed)
	}
}
