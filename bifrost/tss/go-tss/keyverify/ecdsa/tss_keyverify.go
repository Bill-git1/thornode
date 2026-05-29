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
// processVerify and returns ctx.Err(). The scheduler attaches PerVaultTimeout
// to ctx; processVerify's internal tssConf.KeyGenTimeout still bounds the
// per-round wait independently of ctx.
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

	var wg sync.WaitGroup
	wg.Add(2)
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

	verifyErr := t.processVerify(ctx, errChan, outCh, endCh)
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

func (t *TssKeyVerify) processVerify(
	ctx context.Context,
	errChan chan struct{},
	outCh <-chan btss.Message,
	endCh <-chan error,
) error {
	defer t.logger.Debug().Msg("finished keyverify process")
	t.logger.Debug().Msg("start to read messages from local party")
	tssConf := t.tssCommonStruct.GetConf()
	blameMgr := t.tssCommonStruct.GetBlameMgr()
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

		case <-time.After(tssConf.KeyGenTimeout):
			t.logger.Error().Msgf("fail to complete keyverify within %s", tssConf.KeyGenTimeout)
			lastMsg := blameMgr.GetLastMsg()
			failReason := blameMgr.GetBlame().FailReason
			if failReason == "" {
				failReason = blame.TssTimeout
			}
			if lastMsg == nil {
				t.logger.Error().Msg("fail to start the keyverify, no message of this node was produced")
				return &TimeoutError{Reason: "no message of this node was produced"}
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
			return &TimeoutError{Culprits: culprits}

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
