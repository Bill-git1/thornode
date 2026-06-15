// TEMP: keyverify — remove this entire file with the keyverify rollout.
//
// TssKeyVerify mirrors TssKeyGen (bifrost/tss/go-tss/keygen/ecdsa/tss_keygen.go).
// It drives the tss-lib keyverify subprotocol over the existing
// p2p.Communication + TssCommon plumbing.
package ecdsa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	tcrypto "github.com/cometbft/cometbft/crypto"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	bkg "gitlab.com/thorchain/tss/tss-lib-private.git/v3/ecdsa/keygen"
	kv "gitlab.com/thorchain/tss/tss-lib-private.git/v3/ecdsa/keyverify"
	"gitlab.com/thorchain/tss/tss-lib-private.git/v3/tss"
	btss "gitlab.com/thorchain/tss/tss-lib-private.git/v3/tss"

	"gitlab.com/thorchain/thornode/v3/bifrost/p2p"
	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/conversion"
	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/messages"
	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/storage"
	"gitlab.com/thorchain/thornode/v3/bifrost/tss/curvelock"
	"gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/blame"
	"gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/common"
)

// DefaultKeyverifyRoundTimeout is the fallback inner per-round wait used by
// processVerify when TssConfig.KeyverifyRoundTimeout is zero. Sized for
// operators running bifrost behind proxy/WireGuard hairpins, where 4-of-N TSS
// coordination over the public-internet hop was observed to exceed the prior
// 60s budget at the per-round level. Must remain strictly less than the
// scheduler's PerVaultTimeout (in bifrost/tss/keyverify) so the inner timeout
// fires first and the protocol can attribute the failure to specific culprits
// — otherwise an outer ctx cancellation preempts the per-peer blame path.
const DefaultKeyverifyRoundTimeout = 120 * time.Second

// DefaultKeyverifyRetryInterval / DefaultKeyverifyMaxRetries are the
// fallback recovery-ticker settings used when the corresponding TssConfig
// fields are zero. 3 retries x 5s sit comfortably inside the 120s round
// budget. See DESIGN_retry_and_blame_visibility.md Part A.
const (
	DefaultKeyverifyRetryInterval = 5 * time.Second
	DefaultKeyverifyMaxRetries    = 3
)

type TssKeyVerify struct {
	logger          zerolog.Logger
	localNodePubKey string
	tssCommonStruct *common.TssCommon
	stopChan        chan struct{}
	stateManager    storage.LocalStateManager
	commStopChan    chan struct{}
}

func NewTssKeyVerify(
	localP2PID string,
	conf common.TssConfig,
	localNodePubKey string,
	broadcastChan chan *messages.BroadcastMsgChan,
	stopChan chan struct{},
	msgID string,
	stateManager storage.LocalStateManager,
	privateKey tcrypto.PrivKey,
) *TssKeyVerify {
	return &TssKeyVerify{
		logger:          log.With().Str("module", "keyverify").Str("msgID", msgID).Logger(),
		localNodePubKey: localNodePubKey,
		tssCommonStruct: common.NewTssCommon(localP2PID, broadcastChan, conf, msgID, privateKey, 1),
		stopChan:        stopChan,
		stateManager:    stateManager,
		commStopChan:    make(chan struct{}),
	}
}

func (t *TssKeyVerify) GetTssKeyVerifyChannels() chan *p2p.Message {
	return t.tssCommonStruct.TssMsg
}

func (t *TssKeyVerify) GetTssCommonStruct() *common.TssCommon {
	return t.tssCommonStruct
}

// VerifyKey loads the local keyshare for req.PoolPubKey and runs one round of
// tss-lib keyverify with all req.Members. Returns nil on success,
// blame.ErrTssTimeOut on timeout, or *btss.Error on culprit failure.
//
// ctx bounds the entire attempt — cancellation propagates through
// processVerify and returns ctx.Err(). The scheduler attaches its per-vault
// timeout to ctx; processVerify's internal round timeout (from
// TssConfig.KeyverifyRoundTimeout, falling back to DefaultKeyverifyRoundTimeout)
// still bounds the per-round wait independently of ctx.
func (t *TssKeyVerify) VerifyKey(ctx context.Context, req Request) error {
	partiesID, localPartyID, err := conversion.GetParties(req.Members, t.localNodePubKey)
	if err != nil {
		return fmt.Errorf("fail to get keyverify parties: %w", err)
	}
	threshold, err := conversion.GetThreshold(len(partiesID))
	if err != nil {
		return err
	}
	peerCtx := btss.NewPeerContext(partiesID)
	params := btss.NewParameters(tss.S256(), peerCtx, localPartyID, len(partiesID), threshold)
	// Abort if either Paillier-Blum (Π-mod) modulus proofs or Π-fac
	// (factor-existence) proofs have been disabled — a keyshare re-attested
	// without them provides no real safety guarantee.
	if params.NoProofMod() {
		return errors.New("keyverify aborted: Paillier-Blum (ModProof) verification is disabled")
	}
	if params.NoProofFac() {
		return errors.New("keyverify aborted: Π-fac (factor-existence) proof verification is disabled")
	}

	state, err := t.stateManager.GetLocalState(req.PoolPubKey)
	if err != nil {
		return fmt.Errorf("load keyshare for %s: %w", req.PoolPubKey, err)
	}
	// Pin the global curve to secp256k1 under a shared mutex so an EdDSA load
	// running in parallel cannot flip tss-lib's global mid-unmarshal. Same
	// pattern as keysign/ecdsa/tss_keysign.go.
	var save bkg.LocalPartySaveData
	err = curvelock.WithCurve(tss.S256(), func() error {
		return json.Unmarshal(state.LocalData, &save)
	})
	if err != nil {
		return fmt.Errorf("unmarshal keyshare for %s: %w", req.PoolPubKey, err)
	}

	bufferSize := len(partiesID) * 10
	outCh := make(chan btss.Message, bufferSize)
	endCh := make(chan error, bufferSize)
	errChan := make(chan struct{})

	blameMgr := t.tssCommonStruct.GetBlameMgr()
	party := kv.NewLocalParty(params, &save, outCh, endCh)
	partyIDMap := conversion.SetupPartyIDMap(partiesID)
	if err := conversion.SetupIDMaps(partyIDMap, t.tssCommonStruct.PartyIDtoP2PID); err != nil {
		return fmt.Errorf("setup p2p id map: %w", err)
	}
	if err := conversion.SetupIDMaps(partyIDMap, blameMgr.PartyIDtoP2PID); err != nil {
		return fmt.Errorf("setup blame p2p id map: %w", err)
	}
	partyMap := new(sync.Map)
	// we never run multi keyverify, so the moniker is set to default empty value
	partyMap.Store("", party)
	t.tssCommonStruct.SetPartyInfo(&common.PartyInfo{PartyMap: partyMap, PartyIDMap: partyIDMap})
	blameMgr.SetPartyInfo(partyMap, partyIDMap)
	t.tssCommonStruct.P2PPeersLock.Lock()
	t.tssCommonStruct.P2PPeers = conversion.GetPeersID(t.tssCommonStruct.PartyIDtoP2PID, t.tssCommonStruct.GetLocalPeerID())
	t.tssCommonStruct.P2PPeersLock.Unlock()

	// Recovery ticker (design Part A): enabled before any message is sent so
	// sendBulkMsg retains outbound payloads from the first round on. The
	// recovered hook backs tss_keyverify_recovered_total.
	t.tssCommonStruct.EnableKeyverifyRecovery(func(round string) {
		metricKeyverifyRecovered.WithLabelValues(req.PoolPubKey, round).Inc()
	})

	var wg sync.WaitGroup
	wg.Add(3)
	t.logger.Debug().Msg(">>>>>>>>>>>>>.keyverify party starting")
	go func() {
		defer wg.Done()
		defer t.logger.Debug().Msg(">>>>>>>>>>>>>.keyverify party finished")
		if err := party.Start(); err != nil {
			t.logger.Error().Err(err).Msg("fail to start keyverify party")
			close(errChan)
		}
	}()
	go t.tssCommonStruct.ProcessInboundMessages(t.commStopChan, &wg)
	go t.runRecoveryTicker(&wg, req.PoolPubKey)

	verifyErr := t.processVerify(ctx, errChan, outCh, endCh, partyIDMap)
	if verifyErr != nil {
		close(t.commStopChan)
	} else {
		select {
		case <-time.After(5 * time.Second):
			close(t.commStopChan)
		case <-t.tssCommonStruct.GetTaskDone():
			close(t.commStopChan)
		}
	}
	wg.Wait()
	return verifyErr
}

// runRecoveryTicker drives the per-attempt recovery actions (re-send dropped
// unicasts/broadcasts, re-broadcast confirms, re-request missing shares) at
// KeyverifyRetryInterval until the attempt tears down via commStopChan.
func (t *TssKeyVerify) runRecoveryTicker(wg *sync.WaitGroup, poolPubKey string) {
	defer wg.Done()
	conf := t.tssCommonStruct.GetConf()
	interval := conf.KeyverifyRetryInterval
	if interval <= 0 {
		interval = DefaultKeyverifyRetryInterval
	}
	maxRetries := conf.KeyverifyMaxRetries
	if maxRetries <= 0 {
		maxRetries = DefaultKeyverifyMaxRetries
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-t.commStopChan:
			return
		case <-ticker.C:
			stats := t.tssCommonStruct.RunKeyverifyRecovery(maxRetries)
			for action, n := range map[string]int{
				"unicast_resend":      stats.UnicastResent,
				"broadcast_resend":    stats.BroadcastResent,
				"confirm_rebroadcast": stats.ConfirmsRebroadcast,
				"share_rerequest":     stats.SharesRerequested,
			} {
				if n > 0 {
					metricKeyverifyRetry.WithLabelValues(poolPubKey, action).Add(float64(n))
				}
			}
		}
	}
}

// buildCulpritDetails enriches the timeout culprit list with per-peer
// accepted-share and wire-receipt data (design Part B).
func (t *TssKeyVerify) buildCulpritDetails(nodes []blame.Node, partyIDMap map[string]*btss.PartyID) []CulpritDetail {
	pubkeyToPartyID := make(map[string]string, len(partyIDMap))
	for id, p := range partyIDMap {
		pk, err := conversion.PartyIDtoPubKey(p)
		if err != nil {
			t.logger.Warn().Err(err).Str("party_id", id).Msg("culprit details: party id to pubkey")
			continue
		}
		pubkeyToPartyID[pk] = id
	}
	accepted := t.tssCommonStruct.GetBlameMgr().GetAcceptedSharesSnapshot()
	wire := t.tssCommonStruct.GetKeyverifyWireReceipts()

	details := make([]CulpritDetail, 0, len(nodes))
	for _, n := range nodes {
		d := CulpritDetail{
			PubKey:            n.Pubkey,
			LastAcceptedRound: "none",
			LastWireRound:     "none",
			SharesExpected:    keyverifyRoundCount,
		}
		if id, ok := pubkeyToPartyID[n.Pubkey]; ok {
			bestIdx := -1
			for round, ids := range accepted {
				for _, el := range ids {
					if el != id {
						continue
					}
					d.SharesAccepted++
					if round.Index > bestIdx {
						bestIdx = round.Index
						d.LastAcceptedRound = round.RoundMsg
					}
					break
				}
			}
			if w, ok := wire[id]; ok && w.LastRound != "" {
				d.LastWireRound = w.LastRound
				d.LastSeenAt = w.LastSeenAt
			}
		}
		details = append(details, d)
	}
	return details
}

func (t *TssKeyVerify) processVerify(
	ctx context.Context,
	errChan chan struct{},
	outCh <-chan btss.Message,
	endCh <-chan error,
	partyIDMap map[string]*btss.PartyID,
) error {
	defer t.logger.Debug().Msg("finished keyverify process")
	t.logger.Debug().Msg("start to read messages from local party")
	blameMgr := t.tssCommonStruct.GetBlameMgr()
	roundTimeout := t.tssCommonStruct.GetConf().KeyverifyRoundTimeout
	if roundTimeout <= 0 {
		roundTimeout = DefaultKeyverifyRoundTimeout
	}
	for {
		select {
		case <-errChan:
			t.logger.Error().Msg("keyverify failed")
			return errors.New("error channel closed; fail to start local keyverify party")

		case <-t.stopChan:
			return errors.New("received exit signal")

		case <-ctx.Done():
			t.logger.Error().Err(ctx.Err()).Msg("keyverify cancelled by context")
			return fmt.Errorf("keyverify cancelled: %w", ctx.Err())

		case <-time.After(roundTimeout):
			t.logger.Error().Msgf("fail to complete keyverify within %s", roundTimeout)
			lastMsg := blameMgr.GetLastMsg()
			failReason := blameMgr.GetBlame().FailReason
			if failReason == "" {
				failReason = blame.TssTimeout
			}
			if lastMsg == nil {
				t.logger.Error().Msg("fail to start the keyverify, no message of this node was produced")
				return &TimeoutError{Reason: "no message of this node was produced", LocalRound: "none"}
			}
			t.tssCommonStruct.P2PPeersLock.RLock()
			threshold, terr := conversion.GetThreshold(len(t.tssCommonStruct.P2PPeers) + 1)
			t.tssCommonStruct.P2PPeersLock.RUnlock()
			if terr != nil {
				t.logger.Error().Err(terr).Msg("error in get the threshold to generate blame")
			}
			blameNodesBroadcast, berr := blameMgr.GetBroadcastBlame(lastMsg.Type())
			if berr != nil {
				t.logger.Error().Err(berr).Msg("error in get broadcast blame")
			}
			if len(blameNodesBroadcast) > 0 && len(blameNodesBroadcast) <= threshold {
				blameMgr.GetBlame().SetBlame(failReason, blameNodesBroadcast, false, lastMsg.Type())
			}
			culprits := make([]string, 0, len(blameNodesBroadcast))
			for _, n := range blameNodesBroadcast {
				culprits = append(culprits, n.Pubkey)
			}
			return &TimeoutError{
				Culprits:   culprits,
				Details:    t.buildCulpritDetails(blameNodesBroadcast, partyIDMap),
				LocalRound: lastMsg.Type(),
			}

		case msg := <-outCh:
			t.logger.Debug().Msgf(">>>>>>>>>>msg: %s", msg.String())
			blameMgr.SetLastMsg(msg)
			if err := t.tssCommonStruct.ProcessOutCh(msg, messages.TSSKeyVerifyMsg); err != nil {
				t.logger.Error().Err(err).Msg("fail to process the message")
				return fmt.Errorf("process out: %w", err)
			}

		case res := <-endCh:
			if res == nil {
				if err := t.tssCommonStruct.NotifyTaskDone(); err != nil {
					t.logger.Error().Err(err).Msg("fail to broadcast the keyverify done")
				}
				return nil
			}
			return res
		}
	}
}
