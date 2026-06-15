// TEMP: keyverify — remove this entire file with the keyverify rollout.
package ecdsa

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	tcrypto "github.com/cometbft/cometbft/crypto/ed25519"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/rs/zerolog"
	bkg "gitlab.com/thorchain/tss/tss-lib-private.git/v3/ecdsa/keygen"
	btss "gitlab.com/thorchain/tss/tss-lib-private.git/v3/tss"

	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/conversion"
	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/messages"
	"gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/blame"
	"gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/common"
)

// stubLastMsg is a minimal btss.Message that only implements Type(). Production
// code in the timeout branch only calls lastMsg.Type(); everything else is no-op.
type stubLastMsg struct {
	roundType string
}

func (s *stubLastMsg) Type() string                                     { return s.roundType }
func (s *stubLastMsg) GetTo() []*btss.PartyID                           { return nil }
func (s *stubLastMsg) GetFrom() *btss.PartyID                           { return nil }
func (s *stubLastMsg) IsBroadcast() bool                                { return true }
func (s *stubLastMsg) IsToOldCommittee() bool                           { return false }
func (s *stubLastMsg) IsToOldAndNewCommittees() bool                    { return false }
func (s *stubLastMsg) WireBytes() ([]byte, *btss.MessageRouting, error) { return nil, nil, nil }
func (s *stubLastMsg) WireMsg() *btss.MessageWrapper                    { return nil }
func (s *stubLastMsg) String() string                                   { return "stub:" + s.roundType }

// newProcessVerifyHarness builds a minimal TssKeyVerify for unit-level
// exercising of processVerify. It does NOT exercise the full Keyverify
// protocol — only the channel/timeout dispatch inside processVerify. Real
// end-to-end coverage lives in tss_keyverify_test.go's 4-party suite.
func newProcessVerifyHarness(t *testing.T, keyGenTimeout time.Duration) *TssKeyVerify {
	t.Helper()
	priv := tcrypto.GenPrivKey()
	// KeyverifyRoundTimeout is what processVerify reads for its inner round
	// wait; mirror the caller's keyGenTimeout into it so existing fast
	// (50ms) and slow (30s) timeout-path tests keep their previous schedules.
	conf := common.TssConfig{
		PartyTimeout:          keyGenTimeout,
		KeyGenTimeout:         keyGenTimeout,
		KeySignTimeout:        keyGenTimeout,
		PreParamTimeout:       keyGenTimeout,
		KeyverifyRoundTimeout: keyGenTimeout,
		EnableMonitor:         false,
	}
	tssCommon := common.NewTssCommon(
		"local-peer",
		make(chan *messages.BroadcastMsgChan, 1),
		conf,
		"unit-test-msgID",
		priv,
		1,
	)
	return &TssKeyVerify{
		logger:          zerolog.Nop(),
		localNodePubKey: "local-pub",
		tssCommonStruct: tssCommon,
		stopChan:        make(chan struct{}),
		commStopChan:    make(chan struct{}),
	}
}

// TestProcessVerify_TimeoutWithoutMessageReturnsTimeoutError exercises the
// processVerify branch where the timeout fires before the local party has
// produced any message (lastMsg == nil). Production hits this when every
// peer is offline — no inbound activity ever sets blameMgr.LastMsg. Pins:
//   - the return type is *TimeoutError (so cmd/bifrost extracts no culprits)
//   - errors.Is(err, blame.ErrTssTimeOut) holds (scheduler dispatch contract)
//   - the Reason field calls out "no message of this node was produced"
func TestProcessVerify_TimeoutWithoutMessageReturnsTimeoutError(t *testing.T) {
	tkv := newProcessVerifyHarness(t, 50*time.Millisecond)

	// Outbound/error channels never receive anything — timeout fires first.
	outCh := make(chan btss.Message)
	endCh := make(chan error)
	errChan := make(chan struct{})

	start := time.Now()
	err := tkv.processVerify(context.Background(), errChan, outCh, endCh, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("processVerify returned nil, expected timeout error")
	}
	// Confirm timeout actually fired (not some other branch).
	if elapsed < 40*time.Millisecond {
		t.Errorf("processVerify returned in %s — too fast, did not actually wait for timeout", elapsed)
	}

	var tErr *TimeoutError
	if !errors.As(err, &tErr) {
		t.Fatalf("expected *TimeoutError, got %T: %v", err, err)
	}
	if !errors.Is(err, blame.ErrTssTimeOut) {
		t.Fatalf("errors.Is(err, blame.ErrTssTimeOut) = false, want true (scheduler dispatch would misclassify this)")
	}
	if tErr.Reason != "no message of this node was produced" {
		t.Errorf("unexpected Reason: %q", tErr.Reason)
	}
	if len(tErr.Culprits) != 0 {
		t.Errorf("expected empty culprits for no-message timeout, got %v", tErr.Culprits)
	}
}

// TestProcessVerify_TimeoutWithCulpritsReturnsCulprits exercises the
// lastMsg != nil branch of the timeout case (tss_keyverify.go:198-215),
// which is what writes meaningful .fail markers in production when some
// peers responded but others timed out. Pins:
//   - GetThreshold(len(P2PPeers)+1) computes correctly for 4 parties
//   - GetBroadcastBlame returns blame nodes (diff peers vs. standby)
//   - the len(blameNodesBroadcast) > 0 && <= threshold guard fires
//   - SetBlame is called on the blame manager (FailReason propagates)
//   - the returned *TimeoutError carries the culprit pubkeys
//   - errors.Is(err, blame.ErrTssTimeOut) holds
func TestProcessVerify_TimeoutWithCulpritsReturnsCulprits(t *testing.T) {
	conversion.SetupBech32Prefix()

	tkv := newProcessVerifyHarness(t, 50*time.Millisecond)

	// Build 4-party setup mirroring blame/policy_test.go SetUpTest:
	// localTestPubKeys[0] = local; standby = party with id sorted-position 1;
	// blames = parties with id sorted-positions 2 and 3.
	localTestPubKeys := append([]string{}, testPubKeys...)
	sort.Strings(localTestPubKeys)
	partiesID, _, err := conversion.GetParties(localTestPubKeys, testPubKeys[0])
	if err != nil {
		t.Fatalf("GetParties: %v", err)
	}
	partyIDMap := conversion.SetupPartyIDMap(partiesID)

	bm := tkv.tssCommonStruct.GetBlameMgr()
	if err := conversion.SetupIDMaps(partyIDMap, bm.PartyIDtoP2PID); err != nil {
		t.Fatalf("SetupIDMaps: %v", err)
	}
	// SetPartyInfo needs at least one Party in the map (it picks one to derive
	// localPartyID). Build a real keygen LocalParty so blame manager has a
	// valid local id (testPubKeys[0]'s sorted position).
	outCh := make(chan btss.Message, len(partiesID))
	endCh := make(chan *bkg.LocalPartySaveData, len(partiesID))
	peerCtx := btss.NewPeerContext(partiesID)
	var localParty *btss.PartyID
	for _, p := range partiesID {
		if pk, perr := conversion.PartyIDtoPubKey(p); perr == nil && pk == testPubKeys[0] {
			localParty = p
			break
		}
	}
	if localParty == nil {
		t.Fatal("unable to locate local partyID for testPubKeys[0]")
	}
	params := btss.NewParameters(btss.S256(), peerCtx, localParty, len(partiesID), 3)
	keygenParty := bkg.NewLocalParty(params, outCh, endCh)
	partyMap := new(sync.Map)
	partyMap.Store("", keygenParty)
	bm.SetPartyInfo(partyMap, partyIDMap)

	// Pick one non-local partyID to mark as standby (i.e. responded this round).
	// The remaining two non-local parties will be the blamed peers.
	const roundName = "KEYVERIFY1"
	var standbyParty *btss.PartyID
	var expectedCulpritPubKeys []string
	for _, p := range partiesID {
		if p.Id == localParty.Id {
			continue
		}
		if standbyParty == nil {
			standbyParty = p
			continue
		}
		pk, _ := conversion.PartyIDtoPubKey(p)
		expectedCulpritPubKeys = append(expectedCulpritPubKeys, pk)
	}
	sort.Strings(expectedCulpritPubKeys)

	// Mark standbyParty as responded by inserting a WireMessage whose
	// RoundInfo matches roundName and whose Routing.From is the standby party.
	bm.GetRoundMgr().Set("key1", &messages.WireMessage{
		Routing:   &btss.MessageRouting{From: standbyParty},
		RoundInfo: roundName,
	})
	// Set the last-msg stub so processVerify enters the lastMsg != nil branch
	// with a Type() that matches our standby RoundInfo.
	bm.SetLastMsg(&stubLastMsg{roundType: roundName})

	// P2PPeers must be non-empty so GetThreshold succeeds and the
	// len(blameNodesBroadcast) <= threshold guard can pass. Use the 3 non-local
	// peer IDs so threshold = ceil(4 * 2/3) - 1 = 2; blameNodes will be 2 of
	// them; 2 <= 2 satisfies the guard.
	var p2pPeers []peer.ID
	for partyID, p := range bm.PartyIDtoP2PID {
		if partyID == localParty.Id {
			continue
		}
		p2pPeers = append(p2pPeers, p)
	}
	tkv.tssCommonStruct.P2PPeersLock.Lock()
	tkv.tssCommonStruct.P2PPeers = p2pPeers
	tkv.tssCommonStruct.P2PPeersLock.Unlock()

	outChDrive := make(chan btss.Message)
	endChDrive := make(chan error)
	errChan := make(chan struct{})

	start := time.Now()
	err = tkv.processVerify(context.Background(), errChan, outChDrive, endChDrive, partyIDMap)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("processVerify returned nil, expected timeout error")
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("processVerify returned in %s — too fast, did not actually wait for timeout", elapsed)
	}
	var tErr *TimeoutError
	if !errors.As(err, &tErr) {
		t.Fatalf("expected *TimeoutError, got %T: %v", err, err)
	}
	if !errors.Is(err, blame.ErrTssTimeOut) {
		t.Fatalf("errors.Is(err, blame.ErrTssTimeOut) = false; scheduler would misclassify this as a non-timeout error")
	}
	if len(tErr.Culprits) == 0 {
		t.Fatal("expected non-empty culprits from timeout-with-lastMsg branch — this is the .fail marker payload in production")
	}
	gotCulprits := append([]string{}, tErr.Culprits...)
	sort.Strings(gotCulprits)
	if len(gotCulprits) != len(expectedCulpritPubKeys) {
		t.Fatalf("culprit count mismatch: got %v, want %v", gotCulprits, expectedCulpritPubKeys)
	}
	for i := range gotCulprits {
		if gotCulprits[i] != expectedCulpritPubKeys[i] {
			t.Errorf("culprit[%d] = %q, want %q", i, gotCulprits[i], expectedCulpritPubKeys[i])
		}
	}
	// SetBlame should have populated the blame manager's Blame.BlameNodes.
	if got := bm.GetBlame(); got == nil || len(got.BlameNodes) != len(expectedCulpritPubKeys) {
		t.Errorf("blame manager Blame.BlameNodes not populated; got %+v, want %d entries", got, len(expectedCulpritPubKeys))
	}
	// Part B enrichment: LocalRound carries the reporter's last produced
	// round, and Details is parallel to Culprits with "none" placeholders
	// (no shares were accepted and no wire receipts recorded in this harness).
	if tErr.LocalRound != roundName {
		t.Errorf("LocalRound = %q, want %q", tErr.LocalRound, roundName)
	}
	if len(tErr.Details) != len(tErr.Culprits) {
		t.Fatalf("Details length %d != Culprits length %d", len(tErr.Details), len(tErr.Culprits))
	}
	for i, d := range tErr.Details {
		if d.PubKey != tErr.Culprits[i] {
			t.Errorf("Details[%d].PubKey = %q, want %q (parallel order)", i, d.PubKey, tErr.Culprits[i])
		}
		if d.LastAcceptedRound != "none" || d.LastWireRound != "none" {
			t.Errorf("Details[%d] rounds = (%q,%q), want (none,none)", i, d.LastAcceptedRound, d.LastWireRound)
		}
		if d.SharesExpected != keyverifyRoundCount {
			t.Errorf("Details[%d].SharesExpected = %d, want %d", i, d.SharesExpected, keyverifyRoundCount)
		}
	}
}

// TestProcessVerify_CtxCancellationReturnsCtxErr asserts the second
// timeout-shutdown path: when the scheduler cancels the per-attempt ctx
// (PerVaultTimeout, or process shutdown), processVerify returns an error
// that wraps ctx.Err() — not a TimeoutError. This keeps shutdown errors
// from being misclassified as protocol timeouts in metrics.
func TestProcessVerify_CtxCancellationReturnsCtxErr(t *testing.T) {
	// Long internal timeout so only ctx cancellation can fire.
	tkv := newProcessVerifyHarness(t, 30*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	outCh := make(chan btss.Message)
	endCh := make(chan error)
	errChan := make(chan struct{})

	err := tkv.processVerify(ctx, errChan, outCh, endCh, nil)
	if err == nil {
		t.Fatal("processVerify returned nil, expected ctx cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected errors.Is(err, context.Canceled), got %v", err)
	}
}
