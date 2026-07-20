package p2p

import (
	"fmt"
	"testing"

	"github.com/libp2p/go-libp2p-core/peer"

	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/messages"
)

// maxFrostParty is the largest vault the FROST budget comments are sized
// against ("the current maximum-size vault"). The constants live in this
// package and are not validated against a party of this size anywhere else;
// the existing budget tests use 2-3 peers with toy 8/12-byte limits, which
// cannot surface whether a real ceremony fits.
const maxFrostParty = 128

func TestFrostVerificationMessagesPerPeer(t *testing.T) {
	t.Parallel()

	maxInt := int(^uint(0) >> 1)
	tests := []struct {
		name      string
		partySize int
		rounds    int
		want      uint32
	}{
		{name: "invalid party", partySize: 0, rounds: 3, want: FrostMaxMessagesPerPeer},
		{name: "invalid rounds", partySize: 100, rounds: 0, want: FrostMaxMessagesPerPeer},
		{name: "small party keeps baseline", partySize: 10, rounds: 3, want: FrostMaxMessagesPerPeer},
		{name: "67 member keysign", partySize: 67, rounds: 2, want: 150},
		{name: "100 member keygen", partySize: 100, rounds: 3, want: 316},
		{name: "full keysign", partySize: maxFrostParty, rounds: 2, want: 272},
		{name: "full keygen", partySize: maxFrostParty, rounds: 3, want: 400},
		{name: "overflow saturates", partySize: maxInt, rounds: maxInt, want: ^uint32(0)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := FrostVerificationMessagesPerPeer(tc.partySize, tc.rounds); got != tc.want {
				t.Fatalf("FrostVerificationMessagesPerPeer(%d, %d) = %d, want %d", tc.partySize, tc.rounds, got, tc.want)
			}
		})
	}
}

// Keysign's two verification rounds and keygen's three rounds share one
// TSSFrostVerifyMsg subscription. Exercise the real stream preflight at the
// maximum modeled party size so a count or byte budget regression cannot make
// honest ceremony completion depend on message ordering.
func TestFrostFullPartyVerificationRoundsFitPreflightBudgets(t *testing.T) {
	remotePeers := make([]peer.ID, maxFrostParty-1)
	for i := range remotePeers {
		remotePeers[i] = peer.ID(fmt.Sprintf("frost-verify-member-%03d", i))
	}

	tests := []struct {
		name   string
		rounds int
	}{
		{name: "keysign two rounds", rounds: 2},
		{name: "keygen three rounds", rounds: 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msgID := fmt.Sprintf("full-party-verify-%d", tc.rounds)
			limit := FrostVerificationMessagesPerPeer(maxFrostParty, tc.rounds)
			comm := newTSSPreflightCommunication(
				messages.TSSFrostVerifyMsg,
				msgID,
				remotePeers,
				FrostMaxSessionPayload,
				limit,
			)
			prefix := tssFramePrefix(t, messages.TSSFrostVerifyMsg, msgID)
			// Includes comfortable overhead for the outer JSON/base64 wrapper and
			// both application signatures in a signed verification echo.
			const verificationFrameBytes = uint32(1280)

			for _, remote := range remotePeers {
				for frame := uint32(0); frame < limit; frame++ {
					if _, _, err := comm.preflightTSSFrame(remote, verificationFrameBytes, prefix); err != nil {
						t.Fatalf("peer %s frame %d/%d rejected in %d-round ceremony: %v", remote, frame+1, limit, tc.rounds, err)
					}
				}
			}

			sub := comm.getSubscriber(messages.TSSFrostVerifyMsg, msgID)
			if sub == nil {
				t.Fatal("expected verification subscription")
			}
			if sub.sessionBytes >= FrostMaxSessionBytes {
				t.Fatalf("full ceremony exhausted byte budget: used %d of %d", sub.sessionBytes, FrostMaxSessionBytes)
			}
			if _, _, err := comm.preflightTSSFrame(remotePeers[0], verificationFrameBytes, prefix); err == nil {
				t.Fatalf("frame %d must be rejected after peer reaches the %d-round verification limit", limit+1, tc.rounds)
			}
		})
	}
}

// An honest, full-size party must be able to complete a ceremony within
// FrostMaxSessionBytes using the REAL constants. This is the coverage that was
// missing: it validates that the session budget is large enough for the party
// size the per-peer and per-message limits assume, so honest load is never
// starved by the aggregate cap.
func TestFrostFullPartyCompletesWithinSessionBudget(t *testing.T) {
	remotePeers := make([]peer.ID, maxFrostParty-1) // 127 remote members
	for i := range remotePeers {
		remotePeers[i] = peer.ID(fmt.Sprintf("frost-member-%03d", i))
	}

	ms := NewMessageIDSubscriber()
	ms.SubscribeRestrictedWithBudgets(
		"frost-session",
		make(chan *Message, 1),
		remotePeers,
		FrostMaxSessionPayload,
		FrostMaxMessagesPerPeer,
		FrostMaxBytesPerPeer,
		FrostMaxSessionBytes,
	)
	sub := ms.GetSubscriber("frost-session")
	if sub == nil {
		t.Fatal("expected restricted subscription")
	}

	// A generous model of one message type's honest traffic: each member sends
	// several rounds worth of fat frames. Real FROST rounds send ~1 frame per
	// peer per type; 6 x 8 KiB per peer is comfortable headroom.
	const framesPerPeer = 6
	const frameBytes = 8 * 1024

	for _, id := range remotePeers {
		for f := 0; f < framesPerPeer; f++ {
			if !sub.accepts(id, frameBytes) {
				t.Fatalf("honest full-size party frame rejected for %s at frame %d; "+
					"session budget is too small for %d members (spent %d of %d bytes)",
					id, f, len(remotePeers), sub.sessionBytes, FrostMaxSessionBytes)
			}
		}
	}

	// Sanity: honest load must leave real headroom, not squeak in at the edge.
	used := sub.sessionBytes
	if used >= FrostMaxSessionBytes {
		t.Fatalf("honest party consumed the whole session budget: %d of %d", used, FrostMaxSessionBytes)
	}
	t.Logf("honest %d-member ceremony used %d of %d session bytes (%.1f%%)",
		len(remotePeers), used, FrostMaxSessionBytes, 100*float64(used)/float64(FrostMaxSessionBytes))
}

// Documents the known, accepted limitation: because FrostMaxBytesPerPeer is a
// fixed constant rather than FrostMaxSessionBytes/len(party), a small number of
// authorized members can spend the entire session budget and starve the rest.
// This is tolerated because the attacker must already be an authorized party
// member, who can fail the ceremony by other means (bad share, refusing to
// sign) and is attributable via equivocation evidence. If this test's count
// ever changes, the fairness characteristics of the constants changed with it.
func TestFrostPerPeerCapIsAMemoryBackstopNotFairness(t *testing.T) {
	starvers := int(FrostMaxSessionBytes / FrostMaxBytesPerPeer) // 8
	party := make([]peer.ID, starvers+1)
	for i := range party {
		party[i] = peer.ID(fmt.Sprintf("member-%d", i))
	}

	ms := NewMessageIDSubscriber()
	ms.SubscribeRestrictedWithBudgets(
		"frost-session",
		make(chan *Message, 1),
		party,
		FrostMaxSessionPayload,
		FrostMaxMessagesPerPeer,
		FrostMaxBytesPerPeer,
		FrostMaxSessionBytes,
	)
	sub := ms.GetSubscriber("frost-session")

	// Each of the first `starvers` members spends its full per-peer cap. The
	// per-peer cap does not scale with party size, so this exhausts the whole
	// session budget.
	const chunk = int(FrostMaxSessionPayload) // 1 MiB frames
	for i := 0; i < starvers; i++ {
		spent := uint64(0)
		for spent < FrostMaxBytesPerPeer {
			if !sub.accepts(party[i], chunk) {
				break
			}
			spent += uint64(chunk)
		}
	}

	if sub.sessionBytes != FrostMaxSessionBytes {
		t.Fatalf("expected %d authorized members to exhaust the session budget; spent %d of %d",
			starvers, sub.sessionBytes, FrostMaxSessionBytes)
	}
	// The honest (starvers+1)-th member is now starved despite being well under
	// its own per-peer allowance. This is the documented tradeoff.
	if sub.accepts(party[starvers], 1) {
		t.Fatal("expected the honest late member to be starved once the session budget is spent")
	}
}
