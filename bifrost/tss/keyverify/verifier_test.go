// TEMP: keyverify — remove this entire file with the keyverify rollout.
package keyverify

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gitlab.com/thorchain/thornode/v3/bifrost/p2p"
	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/messages"
	kvecdsa "gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/keyverify/ecdsa"
)

// recordingComm captures each SetSubscribe/CancelSubscribe call in order so a
// test can assert (1) which topics are subscribed, (2) that subscribe-before-
// sleep is preserved (subscribeCount at sleep-start time), and (3) that every
// subscribe has a matching cancel.
type recordingComm struct {
	mu         sync.Mutex
	subscribes []subscribeCall
	cancels    []cancelCall
	releases   []string
}

type subscribeCall struct {
	topic   messages.THORChainTSSMessageType
	msgID   string
	channel chan *p2p.Message
}

type cancelCall struct {
	topic messages.THORChainTSSMessageType
	msgID string
}

func (r *recordingComm) GetLocalPeerID() string { return "peer-local" }

func (r *recordingComm) SetSubscribe(topic messages.THORChainTSSMessageType, msgID string, channel chan *p2p.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subscribes = append(r.subscribes, subscribeCall{topic, msgID, channel})
}

func (r *recordingComm) CancelSubscribe(topic messages.THORChainTSSMessageType, msgID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancels = append(r.cancels, cancelCall{topic, msgID})
}

func (r *recordingComm) ReleaseStream(msgID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.releases = append(r.releases, msgID)
}

func (r *recordingComm) subscribeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.subscribes)
}

// fakeTssKeyVerify lets tests observe VerifyKey invocation and inject results.
type fakeTssKeyVerify struct {
	ch              chan *p2p.Message
	verifyKeyCalled bool
	verifyReq       kvecdsa.Request
	verifyResult    error
	// onVerify records observable state at the moment VerifyKey was called.
	onVerify func()
}

func (f *fakeTssKeyVerify) GetTssKeyVerifyChannels() chan *p2p.Message { return f.ch }
func (f *fakeTssKeyVerify) VerifyKey(_ context.Context, req kvecdsa.Request) error {
	if f.onVerify != nil {
		f.onVerify()
	}
	f.verifyKeyCalled = true
	f.verifyReq = req
	return f.verifyResult
}

// newLiveVerifierForTest builds a LiveVerifier wired to the given fake comm
// and tss-lib stub. Production wiring (NewLiveVerifier) builds the closure
// from internal state — bypass that here so we can inject the fakes.
func newLiveVerifierForTest(comm commSubscriber, tkv *fakeTssKeyVerify) *LiveVerifier {
	return &LiveVerifier{
		comm:            comm,
		localNodePubKey: "thorpub1-local",
		newTssKeyVerify: func(_ string) tssKeyVerifier { return tkv },
	}
}

// TestLiveVerifier_SubscribesAllFourTopicsBeforeSleep pins the load-bearing
// invariant called out in verifier.go's doc-comment: all four keyverify topic
// subscriptions land BEFORE startDelay so messages arriving during the stagger
// window aren't dropped. A regression that reorders subscribe after sleep, or
// drops the TSSKeyVerifyVerMsg subscription, would have to be caught by a
// production peer dropping a broadcast at exactly the wrong moment — this
// test would catch it deterministically.
func TestLiveVerifier_SubscribesAllFourTopicsBeforeSleep(t *testing.T) {
	rc := &recordingComm{}
	ch := make(chan *p2p.Message, 1)

	// Capture subscribe count at the moment VerifyKey starts. We sleep
	// LONG enough that any reordering (sleep then subscribe) would be obvious.
	var subscribesAtVerifyStart int
	tkv := &fakeTssKeyVerify{
		ch: ch,
		onVerify: func() {
			subscribesAtVerifyStart = rc.subscribeCount()
		},
	}

	lv := newLiveVerifierForTest(rc, tkv)

	pk := "thorpub1addwnpepqf90u7n3nr2jwsw4t2gzhzqfdlply8dlzv3mdj4dr22uvhe04azq5gac3gq"
	height := int64(12345)
	ctx := WithHeight(context.Background(), height)

	startDelay := 100 * time.Millisecond
	err := lv.Verify(ctx, pk, []string{pk, "thorpub1other"}, startDelay)
	if err != nil {
		t.Fatalf("Verify returned unexpected error: %v", err)
	}

	// (1) All 4 SetSubscribe calls landed before VerifyKey started — the
	// startDelay sleep happens between subscribe and VerifyKey.
	if subscribesAtVerifyStart != 4 {
		t.Fatalf("expected 4 subscribes BEFORE VerifyKey (sleep), got %d at VerifyKey start", subscribesAtVerifyStart)
	}

	// (2) Required topics, in the documented order.
	wantTopics := []messages.THORChainTSSMessageType{
		messages.TSSKeyVerifyMsg,
		messages.TSSKeyVerifyVerMsg,
		messages.TSSControlMsg,
		messages.TSSTaskDone,
	}
	if len(rc.subscribes) != len(wantTopics) {
		t.Fatalf("expected %d subscribes, got %d", len(wantTopics), len(rc.subscribes))
	}
	wantMsgID := "keyverify-" + pk + "-12345"
	for i, want := range wantTopics {
		got := rc.subscribes[i]
		if got.topic != want {
			t.Errorf("subscribe[%d] topic = %v, want %v", i, got.topic, want)
		}
		if got.msgID != wantMsgID {
			t.Errorf("subscribe[%d] msgID = %q, want %q", i, got.msgID, wantMsgID)
		}
		if got.channel != ch {
			t.Errorf("subscribe[%d] channel mismatch — all four topics must share the same channel", i)
		}
	}

	// (3) Every subscribe has a matching cancel by the time Verify returns.
	if len(rc.cancels) != len(wantTopics) {
		t.Fatalf("expected %d cancels, got %d — defer cleanup is incomplete", len(wantTopics), len(rc.cancels))
	}
	gotCancelTopics := map[messages.THORChainTSSMessageType]bool{}
	for _, c := range rc.cancels {
		if c.msgID != wantMsgID {
			t.Errorf("cancel msgID = %q, want %q", c.msgID, wantMsgID)
		}
		gotCancelTopics[c.topic] = true
	}
	for _, want := range wantTopics {
		if !gotCancelTopics[want] {
			t.Errorf("missing CancelSubscribe for topic %v", want)
		}
	}

	// (3b) The attempt's parked streams are released exactly once — without
	// this every attempt permanently leaks its libp2p streams.
	if len(rc.releases) != 1 || rc.releases[0] != wantMsgID {
		t.Errorf("ReleaseStream calls = %v, want exactly one with %q", rc.releases, wantMsgID)
	}

	// (4) VerifyKey was actually invoked with the expected request.
	if !tkv.verifyKeyCalled {
		t.Fatal("VerifyKey was never called")
	}
	if tkv.verifyReq.PoolPubKey != pk || tkv.verifyReq.BlockHeight != height {
		t.Errorf("VerifyKey request mismatch: %+v", tkv.verifyReq)
	}
}

// TestLiveVerifier_CtxCancelDuringStartDelayReturnsCtxErr pins the behavior
// that ctx cancellation during the stagger sleep returns ctx.Err() without
// running VerifyKey, but STILL cleans up all subscriptions (the defers fire).
func TestLiveVerifier_CtxCancelDuringStartDelayReturnsCtxErr(t *testing.T) {
	rc := &recordingComm{}
	tkv := &fakeTssKeyVerify{ch: make(chan *p2p.Message, 1)}
	lv := newLiveVerifierForTest(rc, tkv)

	ctx, cancel := context.WithCancel(WithHeight(context.Background(), 1))
	go func() {
		// Cancel during the sleep window.
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	pk := "thorpub1addwnpepqf90u7n3nr2jwsw4t2gzhzqfdlply8dlzv3mdj4dr22uvhe04azq5gac3gq"
	err := lv.Verify(ctx, pk, []string{pk}, 5*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if tkv.verifyKeyCalled {
		t.Fatal("VerifyKey must NOT run after ctx cancellation during startDelay")
	}
	if len(rc.cancels) != 4 {
		t.Fatalf("expected 4 cancels even on early return, got %d — defer cleanup broken", len(rc.cancels))
	}
}

// TestLiveVerifier_NoHeightInCtx pins that a missing height (zero value) still
// produces a valid msgID — the scheduler always sets WithHeight, but a future
// caller that forgets to should not break the msgID format silently.
func TestLiveVerifier_NoHeightInCtx(t *testing.T) {
	rc := &recordingComm{}
	tkv := &fakeTssKeyVerify{ch: make(chan *p2p.Message, 1)}
	lv := newLiveVerifierForTest(rc, tkv)

	pk := "thorpub1addwnpepqf90u7n3nr2jwsw4t2gzhzqfdlply8dlzv3mdj4dr22uvhe04azq5gac3gq"
	if err := lv.Verify(context.Background(), pk, []string{pk}, 0); err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	wantMsgID := "keyverify-" + pk + "-0"
	if len(rc.subscribes) == 0 || rc.subscribes[0].msgID != wantMsgID {
		t.Fatalf("expected msgID %q, got %q", wantMsgID, rc.subscribes[0].msgID)
	}
}
