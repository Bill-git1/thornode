// TEMP: keyverify — remove this entire file with the keyverify rollout.
package common

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"sync"
	"testing"

	tcrypto "github.com/cometbft/cometbft/crypto"
	"github.com/cometbft/cometbft/crypto/secp256k1"
	btss "gitlab.com/thorchain/tss/tss-lib-private.git/v3/tss"

	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/conversion"
	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/messages"
)

func newRecoveryTestCommon(t *testing.T, privKey tcrypto.PrivKey) (*TssCommon, chan *messages.BroadcastMsgChan, []*btss.PartyID, *btss.PartyID) {
	t.Helper()
	conversion.SetupBech32Prefix()
	ch := make(chan *messages.BroadcastMsgChan, 64)
	tc := NewTssCommon("", ch, TssConfig{}, "kv-recovery-test", privKey, 1)
	localTestPubKeys := append([]string(nil), testPubKeys[:]...)
	partiesID, localPartyID, err := conversion.GetParties(localTestPubKeys, testPubKeys[0])
	if err != nil {
		t.Fatalf("GetParties: %v", err)
	}
	partyIDMap := conversion.SetupPartyIDMap(partiesID)
	if err := conversion.SetupIDMaps(partyIDMap, tc.PartyIDtoP2PID); err != nil {
		t.Fatalf("SetupIDMaps: %v", err)
	}
	tc.SetPartyInfo(&PartyInfo{PartyMap: new(sync.Map), PartyIDMap: partyIDMap})
	tc.P2PPeersLock.Lock()
	tc.P2PPeers = conversion.GetPeersID(tc.PartyIDtoP2PID, tc.GetLocalPeerID())
	tc.P2PPeersLock.Unlock()
	return tc, ch, partiesID, localPartyID
}

func drainBroadcastChan(ch chan *messages.BroadcastMsgChan) []*messages.BroadcastMsgChan {
	var out []*messages.BroadcastMsgChan
	for {
		select {
		case m := <-ch:
			out = append(out, m)
		default:
			return out
		}
	}
}

func TestKeyverifyRecovery_DisabledIsNoop(t *testing.T) {
	tc, ch, _, _ := newRecoveryTestCommon(t, secp256k1.GenPrivKey())
	// no EnableKeyverifyRecovery — keygen/keysign instances look like this
	stats := tc.RunKeyverifyRecovery(3)
	if stats != (KeyverifyRecoveryStats{}) {
		t.Fatalf("expected zero stats on disabled instance, got %+v", stats)
	}
	if msgs := drainBroadcastChan(ch); len(msgs) != 0 {
		t.Fatalf("expected no outbound messages, got %d", len(msgs))
	}
	if r := tc.GetKeyverifyWireReceipts(); r != nil {
		t.Fatalf("expected nil wire receipts on disabled instance, got %v", r)
	}
}

func TestKeyverifyRecovery_ResendsSentMessagesUpToCap(t *testing.T) {
	tc, ch, _, _ := newRecoveryTestCommon(t, secp256k1.GenPrivKey())
	tc.EnableKeyverifyRecovery(nil)

	uni := &messages.BroadcastMsgChan{WrappedMessage: messages.WrappedMessage{MsgID: "m", MessageType: messages.TSSKeyVerifyMsg}}
	bc := &messages.BroadcastMsgChan{WrappedMessage: messages.WrappedMessage{MsgID: "m", MessageType: messages.TSSKeyVerifyMsg}}
	tc.recordKeyverifySent(uni, false)
	tc.recordKeyverifySent(bc, true)

	const maxRetries = 2
	for i := 1; i <= maxRetries; i++ {
		stats := tc.RunKeyverifyRecovery(maxRetries)
		if stats.UnicastResent != 1 || stats.BroadcastResent != 1 {
			t.Fatalf("tick %d: stats = %+v, want 1 unicast + 1 broadcast", i, stats)
		}
		if msgs := drainBroadcastChan(ch); len(msgs) != 2 {
			t.Fatalf("tick %d: expected 2 re-sent messages, got %d", i, len(msgs))
		}
	}
	// cap reached — nothing more goes out
	stats := tc.RunKeyverifyRecovery(maxRetries)
	if stats.UnicastResent != 0 || stats.BroadcastResent != 0 {
		t.Fatalf("post-cap stats = %+v, want zero resends", stats)
	}
	if msgs := drainBroadcastChan(ch); len(msgs) != 0 {
		t.Fatalf("post-cap: expected no messages, got %d", len(msgs))
	}
}

func TestKeyverifyRecovery_ConfirmRebroadcastAndShareRerequest(t *testing.T) {
	tc, ch, partiesID, localPartyID := newRecoveryTestCommon(t, secp256k1.GenPrivKey())
	tc.EnableKeyverifyRecovery(nil)

	var owners []*btss.PartyID
	for _, p := range partiesID {
		if p.Id != localPartyID.Id {
			owners = append(owners, p)
		}
	}

	// item A: broadcast received but not yet accepted (awaiting confirms) →
	// expect our hash-confirm to be re-broadcast (TSSKeyVerifyVerMsg).
	wireMsg := &messages.WireMessage{
		Routing:   &btss.MessageRouting{From: owners[0], IsBroadcast: true},
		RoundInfo: messages.KEYVERIFY1,
		Message:   []byte("round1 payload"),
	}
	keyA := wireMsg.GetCacheKey()
	tc.updateLocalUnconfirmedMessages(keyA, NewLocalCacheItem(wireMsg, "hashA"))

	// item B: message never arrived, but two peers confirmed the same hash
	// (threshold for 4 parties is 2, getMsgHash needs threshold-1 = 1 match)
	// → expect a share re-request (TSSControlMsg).
	itemB := NewLocalCacheItem(nil, "")
	itemB.UpdateConfirmList(tc.PartyIDtoP2PID[owners[1].Id].String(), "hashB")
	itemB.UpdateConfirmList(tc.PartyIDtoP2PID[owners[2].Id].String(), "hashB")
	keyB := owners[0].Id + "-" + messages.KEYVERIFY2b
	tc.updateLocalUnconfirmedMessages(keyB, itemB)

	const maxRetries = 1
	stats := tc.RunKeyverifyRecovery(maxRetries)
	if stats.ConfirmsRebroadcast != 1 || stats.SharesRerequested != 1 {
		t.Fatalf("stats = %+v, want 1 confirm rebroadcast + 1 share rerequest", stats)
	}
	var gotVer, gotControl int
	for _, m := range drainBroadcastChan(ch) {
		switch m.WrappedMessage.MessageType {
		case messages.TSSKeyVerifyVerMsg:
			gotVer++
		case messages.TSSControlMsg:
			gotControl++
		default:
			t.Fatalf("unexpected outbound message type %v", m.WrappedMessage.MessageType)
		}
	}
	if gotVer != 1 || gotControl != 1 {
		t.Fatalf("outbound = %d ver + %d control, want 1 + 1", gotVer, gotControl)
	}

	// per-artifact cap respected on the next tick
	stats = tc.RunKeyverifyRecovery(maxRetries)
	if stats != (KeyverifyRecoveryStats{}) {
		t.Fatalf("post-cap stats = %+v, want zero", stats)
	}
	if msgs := drainBroadcastChan(ch); len(msgs) != 0 {
		t.Fatalf("post-cap: expected no messages, got %d", len(msgs))
	}
}

func TestKeyverifyRecovery_WireReceiptTracksLastRound(t *testing.T) {
	tc, _, _, _ := newRecoveryTestCommon(t, secp256k1.GenPrivKey())
	tc.EnableKeyverifyRecovery(nil)

	tc.recordKeyverifyWireReceipt("p1", messages.KEYVERIFY2b)
	tc.recordKeyverifyWireReceipt("p1", messages.KEYVERIFY1) // late/out-of-order — must not regress LastRound
	tc.recordKeyverifyWireReceipt("p1", "GarbageRound")      // sender-asserted junk — counted, but must not pollute LastRound
	tc.recordKeyverifyWireReceipt("p2", messages.KEYVERIFY1)
	tc.recordKeyverifyWireReceipt("p3", "GarbageRound") // junk as the only message — LastRound stays empty

	receipts := tc.GetKeyverifyWireReceipts()
	p1 := receipts["p1"]
	if p1.MsgsSeen != 3 || p1.LastRound != messages.KEYVERIFY2b || p1.LastSeenAt.IsZero() {
		t.Fatalf("p1 receipt = %+v, want 3 msgs, LastRound=%s, non-zero LastSeenAt", p1, messages.KEYVERIFY2b)
	}
	p2 := receipts["p2"]
	if p2.MsgsSeen != 1 || p2.LastRound != messages.KEYVERIFY1 {
		t.Fatalf("p2 receipt = %+v", p2)
	}
	p3 := receipts["p3"]
	if p3.MsgsSeen != 1 || p3.LastRound != "" || p3.LastSeenAt.IsZero() {
		t.Fatalf("p3 receipt = %+v, want 1 msg, empty LastRound, non-zero LastSeenAt", p3)
	}
}

func TestKeyverifyRecovery_RecoveredHookFiresOnlyForRetriedKeys(t *testing.T) {
	tc, _, _, _ := newRecoveryTestCommon(t, secp256k1.GenPrivKey())
	var fired []string
	tc.EnableKeyverifyRecovery(func(round string) { fired = append(fired, round) })

	tc.noteKeyverifyRecovered("k1", messages.KEYVERIFY1) // no recovery action targeted k1
	if len(fired) != 0 {
		t.Fatalf("hook fired without a prior recovery action: %v", fired)
	}
	tc.bumpKeyverifyAction("req:k1")
	tc.noteKeyverifyRecovered("k1", messages.KEYVERIFY1)
	if len(fired) != 1 || fired[0] != messages.KEYVERIFY1 {
		t.Fatalf("hook fired = %v, want [%s]", fired, messages.KEYVERIFY1)
	}

	// sender-asserted junk rounds are clamped to "other" so a malicious
	// member cannot mint unbounded metric label values
	tc.bumpKeyverifyAction("confirm:k2")
	tc.noteKeyverifyRecovered("k2", "GarbageRound")
	if len(fired) != 2 || fired[1] != "other" {
		t.Fatalf("hook fired = %v, want second entry clamped to \"other\"", fired)
	}
}

// TestKeyverifyRecovery_ConcurrentConfirmUpdates runs recovery ticks against
// concurrent confirm-list writes (the inbound goroutine, in production).
// Under -race this pins the snapshot fix: the ticker must never iterate the
// live ConfirmedList map or read Msg unsynchronized while the inbound side
// mutates them — pre-fix this was a fatal concurrent map iteration/write.
func TestKeyverifyRecovery_ConcurrentConfirmUpdates(t *testing.T) {
	tc, ch, partiesID, localPartyID := newRecoveryTestCommon(t, secp256k1.GenPrivKey())
	tc.EnableKeyverifyRecovery(nil)

	var owners []*btss.PartyID
	for _, p := range partiesID {
		if p.Id != localPartyID.Id {
			owners = append(owners, p)
		}
	}

	// one share-request candidate (Msg == nil) and one confirm candidate
	itemReq := NewLocalCacheItem(nil, "")
	tc.updateLocalUnconfirmedMessages(owners[0].Id+"-"+messages.KEYVERIFY2b, itemReq)
	wireMsg := &messages.WireMessage{
		Routing:   &btss.MessageRouting{From: owners[0], IsBroadcast: true},
		RoundInfo: messages.KEYVERIFY1,
		Message:   []byte("round1 payload"),
	}
	itemConfirm := NewLocalCacheItem(wireMsg, "hashA")
	tc.updateLocalUnconfirmedMessages(wireMsg.GetCacheKey(), itemConfirm)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			itemReq.UpdateConfirmList(tc.PartyIDtoP2PID[owners[1].Id].String(), "hashB")
			itemReq.UpdateConfirmList(tc.PartyIDtoP2PID[owners[2].Id].String(), "hashB")
			itemConfirm.UpdateConfirmList(tc.PartyIDtoP2PID[owners[1].Id].String(), "hashA")
		}
	}()
	for i := 0; i < 200; i++ {
		tc.RunKeyverifyRecovery(1 << 30) // uncapped so every tick exercises both paths
		drainBroadcastChan(ch)
	}
	<-done
}

// TestKeyverifyRecovery_DuplicateAcceptedBroadcastDropped pins the receiver
// guard: a re-received keyverify broadcast that was already accepted
// (roundMgr has its key) must be dropped without recreating a cache item —
// otherwise every recovery re-send would re-trigger an N-2 confirm fan-out.
// The same message under a keygen type must keep today's behavior.
func TestKeyverifyRecovery_DuplicateAcceptedBroadcastDropped(t *testing.T) {
	// private key matching testSenderPubKey (testPubKeys[1]) so the wire
	// signature verifies against the data owner's party key.
	priHexBytes, err := base64.StdEncoding.DecodeString(testBlamePrivKey)
	if err != nil {
		t.Fatal(err)
	}
	rawBytes, err := hex.DecodeString(string(priHexBytes))
	if err != nil {
		t.Fatal(err)
	}
	var privKey secp256k1.PrivKey = rawBytes[:32]

	tc, _, partiesID, _ := newRecoveryTestCommon(t, privKey)
	tc.EnableKeyverifyRecovery(nil)

	var owner *btss.PartyID
	for _, p := range partiesID {
		if pk, perr := conversion.PartyIDtoPubKey(p); perr == nil && pk == testSenderPubKey {
			owner = p
			break
		}
	}
	if owner == nil {
		t.Fatal("unable to locate owner party for testSenderPubKey")
	}

	payload := []byte("share bytes")
	var dataForSign bytes.Buffer
	dataForSign.Write(payload)
	dataForSign.WriteString(tc.msgID)
	sig, err := privKey.Sign(dataForSign.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	wireMsg := &messages.WireMessage{
		Routing:   &btss.MessageRouting{From: owner, IsBroadcast: true},
		RoundInfo: messages.KEYVERIFY1,
		Message:   payload,
		Sig:       sig,
	}
	key := wireMsg.GetCacheKey()

	// simulate prior acceptance
	tc.GetBlameMgr().GetRoundMgr().Set(key, wireMsg)

	if err := tc.processTSSMsg(wireMsg, messages.TSSKeyVerifyMsg, false); err != nil {
		t.Fatalf("processTSSMsg: %v", err)
	}
	if item := tc.TryGetLocalCacheItem(key); item != nil {
		t.Fatal("duplicate accepted keyverify broadcast recreated a cache item — guard did not fire")
	}
	// wire receipt still counts the arrival (receipt is recorded pre-guard)
	if r := tc.GetKeyverifyWireReceipts(); r[owner.Id].MsgsSeen != 1 {
		t.Fatalf("expected wire receipt for owner despite dup drop, got %+v", r[owner.Id])
	}

	// control: keygen messages are not gated — cache item is recreated
	if err := tc.processTSSMsg(wireMsg, messages.TSSKeyGenMsg, false); err != nil {
		t.Fatalf("processTSSMsg (keygen control): %v", err)
	}
	if item := tc.TryGetLocalCacheItem(key); item == nil {
		t.Fatal("keygen-type message unexpectedly dropped — guard must be keyverify-scoped")
	}
}
