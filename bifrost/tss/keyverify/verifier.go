// TEMP: keyverify — remove this entire file with the keyverify rollout.
package keyverify

import (
	"context"
	"fmt"
	"time"

	tcrypto "github.com/cometbft/cometbft/crypto"

	"gitlab.com/thorchain/thornode/v3/bifrost/p2p"
	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/messages"
	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/storage"
	"gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/common"
	kvecdsa "gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/keyverify/ecdsa"
)

// MsgIDPrefix prefixes every keyverify attempt msgID (see Verify). The
// parked-stream sweeper in cmd/bifrost releases p2p streams by this prefix,
// so it must stay in sync with the msgID format below. Keygen/keysign msgIDs
// are hex hashes and can never collide with it.
const MsgIDPrefix = "keyverify-"

// Verifier runs one keyverify round for a single vault. Implementations are
// expected to load the local keyshare for pubKey, run tss-lib keyverify with
// the given member pubkeys, and return:
//   - nil on success
//   - blame.ErrTssTimeOut on timeout (peers offline)
//   - *btss.Error (errors.As) on culprit failure
//   - any other error for keyshare load / configuration problems
//
// startDelay defers the local party's protocol start by the given duration
// while still letting implementations subscribe to inbound topics immediately.
// This lets the scheduler stagger initiation across peers without delaying
// each peer's ability to receive broadcasts from peers that started earlier.
type Verifier interface {
	Verify(ctx context.Context, pubKey string, members []string, startDelay time.Duration) error
}

// commSubscriber is the subset of *p2p.Communication that LiveVerifier needs.
// Carved out as an interface so the production implementation can be swapped
// for a recording fake in unit tests; *p2p.Communication satisfies it
// structurally.
type commSubscriber interface {
	GetLocalPeerID() string
	SetSubscribe(topic messages.THORChainTSSMessageType, msgID string, channel chan *p2p.Message)
	CancelSubscribe(topic messages.THORChainTSSMessageType, msgID string)
	ReleaseStream(msgID string)
}

// LiveVerifier is the production Verifier that drives the go-tss keyverify wrapper.
type LiveVerifier struct {
	comm            commSubscriber
	broadcastChan   chan *messages.BroadcastMsgChan
	stateManager    storage.LocalStateManager
	conf            common.TssConfig
	localNodePubKey string
	privKey         tcrypto.PrivKey
	stopChan        chan struct{}
	// newTssKeyVerify is overridable for unit tests; production uses the
	// real go-tss wrapper. Wraps NewTssKeyVerify + VerifyKey so the test can
	// observe the (msgID, channel) handed to TssKeyVerify without running
	// the full protocol.
	newTssKeyVerify func(msgID string) tssKeyVerifier
}

// tssKeyVerifier is the small surface of *kvecdsa.TssKeyVerify that the
// LiveVerifier uses. Carved out so unit tests can stub the protocol-driving
// call without depending on the full keyverify state machine.
type tssKeyVerifier interface {
	GetTssKeyVerifyChannels() chan *p2p.Message
	VerifyKey(ctx context.Context, req kvecdsa.Request) error
}

func NewLiveVerifier(
	comm *p2p.Communication,
	broadcastChan chan *messages.BroadcastMsgChan,
	stateManager storage.LocalStateManager,
	conf common.TssConfig,
	localNodePubKey string,
	privKey tcrypto.PrivKey,
	stopChan chan struct{},
) *LiveVerifier {
	lv := &LiveVerifier{
		comm:            comm,
		broadcastChan:   broadcastChan,
		stateManager:    stateManager,
		conf:            conf,
		localNodePubKey: localNodePubKey,
		privKey:         privKey,
		stopChan:        stopChan,
	}
	lv.newTssKeyVerify = func(msgID string) tssKeyVerifier {
		return kvecdsa.NewTssKeyVerify(
			lv.comm.GetLocalPeerID(),
			lv.conf,
			lv.localNodePubKey,
			lv.broadcastChan,
			lv.stopChan,
			msgID,
			lv.stateManager,
			lv.privKey,
		)
	}
	return lv
}

// Verify implements Verifier. Pulls the current thornode height from ctx (via
// WithHeight) to form a deterministic msgID, builds a TssKeyVerify for one
// attempt, subscribes to the keyverify topics immediately so early broadcasts
// from peers that started before this node aren't missed, then waits the
// stagger startDelay before starting the local protocol.
//
// Subscribing before sleeping is load-bearing: the p2p stream handler does a
// blocking send to the subscriber channel (or queues in the per-stream
// goroutine), so messages that arrive during startDelay are preserved rather
// than dropped at the layer below.
func (lv *LiveVerifier) Verify(ctx context.Context, pubKey string, members []string, startDelay time.Duration) error {
	height := int64(0)
	if h, ok := ctx.Value(ctxKeyHeight{}).(int64); ok {
		height = h
	}
	msgID := fmt.Sprintf("%s%s-%d", MsgIDPrefix, pubKey, height)

	tkv := lv.newTssKeyVerify(msgID)

	// Release the per-attempt streams parked under this msgID once the
	// attempt ends. Every TSS message opens a new libp2p stream per peer on
	// the sender and parks one on the receiver (StreamMgr.AddStream), and
	// ReleaseStream is the only cleanup — keygen/keysign do the same
	// (tss/keygen.go, tss/keysign.go). Without this, every attempt
	// permanently leaks all its streams (quadratic in members via the
	// confirm fan-out). Declared first so it runs after the CancelSubscribe
	// defers below.
	defer lv.comm.ReleaseStream(msgID)

	// Subscribe inbound channels for this msgID across all topics keyverify uses.
	// Both TSSKeyVerifyMsg (round data) and TSSKeyVerifyVerMsg (broadcast hash
	// echo) are required — the routing wired in common/tss.go expects both.
	ch := tkv.GetTssKeyVerifyChannels()
	lv.comm.SetSubscribe(messages.TSSKeyVerifyMsg, msgID, ch)
	lv.comm.SetSubscribe(messages.TSSKeyVerifyVerMsg, msgID, ch)
	lv.comm.SetSubscribe(messages.TSSControlMsg, msgID, ch)
	lv.comm.SetSubscribe(messages.TSSTaskDone, msgID, ch)
	defer lv.comm.CancelSubscribe(messages.TSSKeyVerifyMsg, msgID)
	defer lv.comm.CancelSubscribe(messages.TSSKeyVerifyVerMsg, msgID)
	defer lv.comm.CancelSubscribe(messages.TSSControlMsg, msgID)
	defer lv.comm.CancelSubscribe(messages.TSSTaskDone, msgID)

	if startDelay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(startDelay):
		}
	}

	return tkv.VerifyKey(ctx, kvecdsa.Request{
		PoolPubKey:  pubKey,
		Members:     members,
		BlockHeight: height,
	})
}

// ctxKeyHeight is the context key for the current thornode height.
type ctxKeyHeight struct{}

// WithHeight attaches the current thornode height to ctx so LiveVerifier can
// use it in the msgID. The scheduler sets this on the per-attempt context.
func WithHeight(ctx context.Context, h int64) context.Context {
	return context.WithValue(ctx, ctxKeyHeight{}, h)
}

// Compile-time check that *LiveVerifier satisfies Verifier.
var (
	_ Verifier = (*LiveVerifier)(nil)
)
