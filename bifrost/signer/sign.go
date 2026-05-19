package signer

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	sdktypes "github.com/cosmos/cosmos-sdk/types"

	"github.com/btcsuite/btcd/btcec"
	"github.com/cenkalti/backoff"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	tssp "gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/tss"

	"gitlab.com/thorchain/thornode/v3/app"
	"gitlab.com/thorchain/thornode/v3/bifrost/blockscanner"
	"gitlab.com/thorchain/thornode/v3/bifrost/metrics"
	"gitlab.com/thorchain/thornode/v3/bifrost/observer"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/shared/signing"
	chainclienttypes "gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/shared/types"
	"gitlab.com/thorchain/thornode/v3/bifrost/pubkeymanager"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
	"gitlab.com/thorchain/thornode/v3/bifrost/tss"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/config"
	"gitlab.com/thorchain/thornode/v3/constants"
	ttypes "gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

// Signer will pull the tx out from thorchain and then forward it to chain
type Signer struct {
	logger                zerolog.Logger
	cfg                   config.Bifrost
	wg                    *sync.WaitGroup
	thorchainBridge       thorclient.ThorchainBridge
	stopChan              chan struct{}
	blockScanner          *blockscanner.BlockScanner
	thorchainBlockScanner *ThorchainBlockScan
	chains                map[common.Chain]chainclients.ChainClient
	storage               SignerStorage
	m                     *metrics.Metrics
	errCounter            *prometheus.CounterVec
	tssKeygen             *tss.KeyGen
	tssServer             *tssp.TssServer
	pubkeyMgr             pubkeymanager.PubKeyValidator
	constantsProvider     *ConstantsProvider
	localPubKeyECDSA      common.PubKey
	localPubKeyEDDSA      common.PubKey
	tssKeysignMetricMgr   *metrics.TssKeysignMetricMgr
	observer              *observer.Observer
	pipeline              *pipeline
}

type pendingKeygenBroadcast struct {
	height               int64
	poolPk               common.PubKey
	secp256k1Signature   []byte
	blame                []ttypes.Blame
	input                common.PubKeys
	keygenType           ttypes.KeygenType
	chains               common.Chains
	keygenTime           int64
	poolPubKeyEddsa      common.PubKey
	poolPubKeyFrost      common.PubKey
	frostKeyshareHex     string
	observedMoneroHeight int64
}

type keygenBackups struct {
	keyshares      []byte
	keysharesEddsa []byte
	keysharesFrost []byte
}

var (
	encryptKeysharesFn    = tss.EncryptKeyshares
	encryptRawKeysharesFn = tss.EncryptRawKeyshares
)

type vaultLocker interface {
	GetVaultLock(vaultPubKey string) *sync.Mutex
}

type vaultLockKeyResolver interface {
	GetVaultLockKey(tx types.TxOutItem) string
}

func chainVaultLock(chain chainclients.ChainClient, vaultPubKey string) *sync.Mutex {
	lockable, ok := chain.(vaultLocker)
	if !ok {
		return nil
	}
	return lockable.GetVaultLock(vaultPubKey)
}

func chainVaultLockKey(chain chainclients.ChainClient, tx types.TxOutItem) string {
	resolver, ok := chain.(vaultLockKeyResolver)
	if !ok {
		return tx.VaultPubKey.String()
	}
	key := strings.TrimSpace(resolver.GetVaultLockKey(tx))
	if key == "" {
		return tx.VaultPubKey.String()
	}
	return key
}

// NewSigner create a new instance of signer
func NewSigner(cfg config.Bifrost,
	thorchainBridge thorclient.ThorchainBridge,
	thorKeys *thorclient.Keys,
	pubkeyMgr pubkeymanager.PubKeyValidator,
	tssServer *tssp.TssServer,
	chains map[common.Chain]chainclients.ChainClient,
	m *metrics.Metrics,
	tssKeysignMetricMgr *metrics.TssKeysignMetricMgr,
	obs *observer.Observer,
) (*Signer, error) {
	storage, err := NewSignerStore(cfg.Signer.SignerDbPath, cfg.Signer.LevelDB, thorchainBridge.GetConfig().SignerPasswd)
	if err != nil {
		return nil, fmt.Errorf("fail to create thorchain scan storage: %w", err)
	}
	if tssKeysignMetricMgr == nil {
		return nil, fmt.Errorf("fail to create signer , tss keysign metric manager is nil")
	}
	var na *ttypes.NodeAccount
	for i := 0; i < 300; i++ { // wait for 5 min before timing out
		var signerAddr sdktypes.AccAddress
		signerAddr, err = thorKeys.GetSignerInfo().GetAddress()
		if err != nil {
			return nil, fmt.Errorf("failed to get address from thorKeys signer: %w", err)
		}
		na, err = thorchainBridge.GetNodeAccount(signerAddr.String())
		if err != nil {
			return nil, fmt.Errorf("fail to get node account from thorchain,err:%w", err)
		}

		if !na.PubKeySet.Secp256k1.IsEmpty() {
			break
		}
		time.Sleep(constants.ThorchainBlockTime)
		log.Info().Msg("Waiting for node account to be registered...")
	}

	if na.PubKeySet.Secp256k1.IsEmpty() {
		return nil, fmt.Errorf("unable to find pubkey for this node account. exiting... ")
	}
	pubkeyMgr.AddNodePubKey(na.PubKeySet.Secp256k1, common.SigningAlgoSecp256k1)

	if !na.PubKeySet.Ed25519.IsEmpty() {
		pubkeyMgr.AddNodePubKey(na.PubKeySet.Ed25519, common.SigningAlgoEd25519)
	}

	cfg.Signer.BlockScanner.ChainID = common.THORChain // hard code to thorchain

	// Create pubkey manager and add our private key
	thorchainBlockScanner, err := NewThorchainBlockScan(cfg.Signer.BlockScanner, storage, thorchainBridge, m, pubkeyMgr)
	if err != nil {
		return nil, fmt.Errorf("fail to create thorchain block scan: %w", err)
	}

	blockScanner, err := blockscanner.NewBlockScanner(cfg.Signer.BlockScanner, storage, m, thorchainBridge, thorchainBlockScanner)
	if err != nil {
		return nil, fmt.Errorf("fail to create block scanner: %w", err)
	}

	kg, err := tss.NewTssKeyGen(thorKeys, tssServer, thorchainBridge)
	if err != nil {
		return nil, fmt.Errorf("fail to create Tss Key gen,err:%w", err)
	}
	constantProvider := NewConstantsProvider(thorchainBridge)
	return &Signer{
		logger:                log.With().Str("module", "signer").Logger(),
		cfg:                   cfg,
		wg:                    &sync.WaitGroup{},
		stopChan:              make(chan struct{}),
		blockScanner:          blockScanner,
		thorchainBlockScanner: thorchainBlockScanner,
		chains:                chains,
		m:                     m,
		storage:               storage,
		errCounter:            m.GetCounterVec(metrics.SignerError),
		pubkeyMgr:             pubkeyMgr,
		thorchainBridge:       thorchainBridge,
		tssKeygen:             kg,
		tssServer:             tssServer,
		constantsProvider:     constantProvider,
		localPubKeyECDSA:      na.PubKeySet.Secp256k1,
		localPubKeyEDDSA:      na.PubKeySet.Ed25519,
		tssKeysignMetricMgr:   tssKeysignMetricMgr,
		observer:              obs,
	}, nil
}

func (s *Signer) getChain(chainID common.Chain) (chainclients.ChainClient, error) {
	chain, ok := s.chains[chainID]
	if !ok {
		s.logger.Debug().Str("chain", chainID.String()).Msg("is not supported yet")
		return nil, errors.New("not supported")
	}
	return chain, nil
}

// Start signer process
func (s *Signer) Start() error {
	s.wg.Add(1)
	go s.processTxnOut(s.thorchainBlockScanner.GetTxOutMessages(), 1)

	s.wg.Add(1)
	go s.processKeygen(s.thorchainBlockScanner.GetKeygenMessages())

	s.wg.Add(1)
	go s.signTransactions()

	s.blockScanner.Start(nil, nil)
	return nil
}

func (s *Signer) shouldSign(tx types.TxOutItem) bool {
	return s.pubkeyMgr.HasPubKey(tx.VaultPubKey) ||
		s.pubkeyMgr.HasPubKey(tx.VaultPubKeyEddsa) ||
		s.pubkeyMgr.HasPubKey(tx.VaultPubKeyFrost)
}

func (s *Signer) shouldAutoObserve(tx types.TxOutItem) bool {
	// Monero outbounds need sign-time observation even when global auto-observe is disabled.
	// The chain scanner can later see the mined tx as an incoming change output, which is
	// insufficient to attribute the scheduled outbound on THORChain.
	return s.cfg.Signer.AutoObserve || tx.Chain.Equals(common.XMRChain)
}

// signTransactions - looks for work to do by getting a list of all unsigned
// transactions stored in the storage
func (s *Signer) signTransactions() {
	s.logger.Info().Msg("start to sign transactions")
	defer s.logger.Info().Msg("stop to sign transactions")
	defer s.wg.Done()
	for {
		select {
		case <-s.stopChan:
			return
		default:
			// When THORChain is catching up , bifrost might get stale data from thornode , thus it shall pause signing
			catchingUp, err := s.thorchainBridge.IsCatchingUp()
			if err != nil {
				s.logger.Error().Err(err).Msg("fail to get thorchain sync status")
				time.Sleep(constants.ThorchainBlockTime)
				break // this will break select
			}
			if !catchingUp {
				s.processTransactions()
			}
			time.Sleep(1 * time.Second)
		}
	}
}

func runWithContext(ctx context.Context, fn func() ([]byte, *types.TxInItem, error)) ([]byte, *types.TxInItem, error) {
	ch := make(chan error, 1)
	var checkpoint []byte
	var txIn *types.TxInItem
	go func() {
		var err error
		checkpoint, txIn, err = fn()
		ch <- err
	}()
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case err := <-ch:
		return checkpoint, txIn, err
	}
}

func (s *Signer) processTransactions() {
	signerConcurrency, err := s.thorchainBridge.GetMimir(constants.SignerConcurrency.String())
	if err != nil {
		s.logger.Error().Err(err).Msg("fail to get signer concurrency mimir")
		return
	}

	// default to 10 if unset
	if signerConcurrency <= 0 {
		signerConcurrency = 10
	}

	// if previously set to different concurrency, drain existing signings
	if s.pipeline != nil && s.pipeline.concurrency != signerConcurrency {
		s.pipeline.Wait()
		s.pipeline = nil
	}

	// if not set, or set to different concurrency, create new pipeline
	if s.pipeline == nil {
		s.pipeline, err = newPipeline(signerConcurrency)
		if err != nil {
			s.logger.Error().Err(err).Msg("fail to create new pipeline")
			return
		}
	}

	// process transactions
	s.pipeline.SpawnSignings(s, s.thorchainBridge)
}

// processTxnOut processes outbound TxOuts and save them to storage
func (s *Signer) processTxnOut(ch <-chan types.TxOut, idx int) {
	s.logger.Info().Int("idx", idx).Msg("start to process tx out")
	defer s.logger.Info().Int("idx", idx).Msg("stop to process tx out")
	defer s.wg.Done()
	for {
		select {
		case <-s.stopChan:
			return
		case txOut, more := <-ch:
			if !more {
				return
			}
			s.logger.Info().Msgf("Received a TxOut Array of %v from the Thorchain", txOut)
			items := make([]TxOutStoreItem, 0, len(txOut.TxArray))

			for i, tx := range txOut.TxArray {
				items = append(items, NewTxOutStoreItem(txOut.Height, tx.TxOutItem(txOut.Height), int64(i)))
			}
			if err := s.storage.Batch(items); err != nil {
				s.logger.Error().Err(err).Msg("fail to save tx out items to storage")
			}
		}
	}
}

func (s *Signer) processKeygen(ch <-chan ttypes.KeygenBlock) {
	s.logger.Info().Msg("start to process keygen")
	defer s.logger.Info().Msg("stop to process keygen")
	defer s.wg.Done()
	for {
		select {
		case <-s.stopChan:
			return
		case keygenBlock, more := <-ch:
			if !more {
				return
			}
			s.logger.Info().Interface("keygenBlock", keygenBlock).Msg("received a keygen block from thorchain")
			s.processKeygenBlock(keygenBlock)
		}
	}
}

func (s *Signer) scheduleKeygenRetry(keygenBlock ttypes.KeygenBlock) bool {
	churnRetryInterval, err := s.thorchainBridge.GetMimir(constants.ChurnRetryInterval.String())
	if err != nil {
		s.logger.Error().Err(err).Msg("fail to get churn retry mimir")
		return false
	}
	if churnRetryInterval <= 0 {
		churnRetryInterval = constants.NewConstantValue().GetInt64Value(constants.ChurnRetryInterval)
	}
	keygenRetryInterval, err := s.thorchainBridge.GetMimir(constants.KeygenRetryInterval.String())
	if err != nil {
		s.logger.Error().Err(err).Msg("fail to get keygen retries mimir")
		return false
	}
	if keygenRetryInterval <= 0 {
		return false
	}

	// sanity check the retry interval is at least 1.5x the timeout
	retryIntervalDuration := time.Duration(keygenRetryInterval) * constants.ThorchainBlockTime
	if retryIntervalDuration <= s.cfg.Signer.KeygenTimeout*3/2 {
		s.logger.Error().
			Stringer("retryInterval", retryIntervalDuration).
			Stringer("keygenTimeout", s.cfg.Signer.KeygenTimeout).
			Msg("retry interval too short")
		return false
	}

	height, err := s.thorchainBridge.GetBlockHeight()
	if err != nil {
		s.logger.Error().Err(err).Msg("fail to get last chain height")
		return false
	}

	// target retry height is the next keygen retry interval over the keygen block height
	targetRetryHeight := (keygenRetryInterval - ((height - keygenBlock.Height) % keygenRetryInterval)) + height

	// skip trying close to churn retry
	if targetRetryHeight > keygenBlock.Height+churnRetryInterval-keygenRetryInterval {
		return false
	}

	go func() {
		// every block, try to start processing again
		for {
			time.Sleep(constants.ThorchainBlockTime)
			currentHeight, err := s.thorchainBridge.GetBlockHeight()
			if err != nil {
				s.logger.Error().Err(err).Msg("fail to get last chain height")
			}
			if currentHeight >= targetRetryHeight {
				s.logger.Info().
					Interface("keygenBlock", keygenBlock).
					Int64("currentHeight", currentHeight).
					Msg("retrying keygen")
				s.processKeygenBlock(keygenBlock)
				return
			}
		}
	}()

	s.logger.Info().
		Interface("keygenBlock", keygenBlock).
		Int64("retryHeight", targetRetryHeight).
		Msg("scheduled keygen retry")

	return true
}

func singleKeygenBlock(height int64, keygen ttypes.Keygen) ttypes.KeygenBlock {
	return ttypes.KeygenBlock{
		Height:  height,
		Keygens: []ttypes.Keygen{keygen},
	}
}

func (s *Signer) processKeygenBlock(keygenBlock ttypes.KeygenBlock) {
	s.logger.Info().Interface("keygenBlock", keygenBlock).Msg("processing keygen block")
	pendingBroadcasts := make([]pendingKeygenBroadcast, 0, len(keygenBlock.Keygens))

	// NOTE: in practice there is only one keygen in the keygen block
	for _, keygenReq := range keygenBlock.Keygens {
		keygenStart := time.Now()
		keygenTime := time.Since(keygenStart).Milliseconds()
		keygenChains := s.keygenChainsForType(keygenReq.Type)
		var pubKey common.PubKeySet
		var blame []ttypes.Blame
		var frostPubKey common.PubKey
		var frostKeyshareHex string
		var observedMoneroHeight int64
		var err error
		var secp256k1Sig []byte

		switch keygenReq.Type {
		case ttypes.KeygenType_XMRMonovaultKeygen:
			pubKey.Ed25519, frostPubKey, frostKeyshareHex, observedMoneroHeight, err = s.runCustomKeygenHooks(
				keygenChains,
				keygenReq.GetMembers().Strings(),
				keygenBlock.Height,
				common.EmptyPubKey,
				common.EmptyPubKey,
			)
			if err != nil {
				s.errCounter.WithLabelValues("fail_custom_keygen_hook", "").Inc()
				s.logger.Error().Err(err).Interface("keygenBlock", keygenBlock).Msg("xmr monovault keygen hooks failed")
				if s.scheduleKeygenRetry(singleKeygenBlock(keygenBlock.Height, keygenReq)) {
					continue
				}
				s.logger.Error().Interface("keygen", keygenReq).Msg("done with keygen retries after xmr monovault keygen failure")
				continue
			}
			if pubKey.Ed25519.IsEmpty() {
				pubKey.Ed25519 = frostPubKey
			}
			if frostPubKey.IsEmpty() {
				frostPubKey = pubKey.Ed25519
			}
			pubKey.Secp256k1 = frostPubKey
		default:
			pubKey, blame, err = s.tssKeygen.GenerateNewKey(keygenBlock.Height, keygenReq.GetMembers())
			if len(blame) > 0 {
				for _, b := range blame {
					s.logger.Error().
						Str("reason", b.FailReason).
						Interface("nodes", b.BlameNodes).
						Msg("keygen blame")
				}
			}
			if err != nil {
				s.errCounter.WithLabelValues("fail_to_keygen_pubkey", "").Inc()
				s.logger.Error().Err(err).Msg("fail to generate new pubkey")
			}

			// re-enqueue the keygen block to retry if we failed to generate a key
			if pubKey.Secp256k1.IsEmpty() || pubKey.Ed25519.IsEmpty() {
				if s.scheduleKeygenRetry(singleKeygenBlock(keygenBlock.Height, keygenReq)) {
					continue
				}
				s.logger.Error().Interface("keygen", keygenReq).Msg("done with keygen retries")
				continue
			}

			// Give chain clients with custom keygen logic a chance to initialize chain-specific
			// signer state (for example Monero multisig address mapping) before keygen is finalized.
			pubKey.Ed25519, frostPubKey, frostKeyshareHex, observedMoneroHeight, err = s.runCustomKeygenHooks(
				keygenChains,
				keygenReq.GetMembers().Strings(),
				keygenBlock.Height,
				pubKey.Secp256k1,
				pubKey.Ed25519,
			)
			if err != nil {
				s.errCounter.WithLabelValues("fail_custom_keygen_hook", "").Inc()
				s.logger.Error().Err(err).Interface("keygenBlock", keygenBlock).Msg("custom keygen hooks failed")
				if s.scheduleKeygenRetry(singleKeygenBlock(keygenBlock.Height, keygenReq)) {
					continue
				}
				s.logger.Error().Interface("keygen", keygenReq).Msg("done with keygen retries after custom keygen hook failure")
				continue
			}

			// generate a verification signature to ensure we can sign with the new key
			secp256k1Sig = s.secp256k1VerificationSignature(pubKey.Secp256k1)
		}

		if pubKey.Secp256k1.IsEmpty() || pubKey.Ed25519.IsEmpty() {
			if s.scheduleKeygenRetry(singleKeygenBlock(keygenBlock.Height, keygenReq)) {
				continue
			}
			s.logger.Error().Interface("keygen", keygenReq).Msg("done with keygen retries after empty keygen result")
			continue
		}

		pendingBroadcasts = append(pendingBroadcasts, pendingKeygenBroadcast{
			height:               keygenBlock.Height,
			poolPk:               pubKey.Secp256k1,
			secp256k1Signature:   secp256k1Sig,
			blame:                blame,
			input:                keygenReq.GetMembers(),
			keygenType:           keygenReq.Type,
			chains:               keygenChains,
			keygenTime:           keygenTime,
			poolPubKeyEddsa:      pubKey.Ed25519,
			poolPubKeyFrost:      frostPubKey,
			frostKeyshareHex:     frostKeyshareHex,
			observedMoneroHeight: observedMoneroHeight,
		})
	}

	s.flushPendingKeygenBroadcasts(pendingBroadcasts)
}

func (s *Signer) flushPendingKeygenBroadcasts(pending []pendingKeygenBroadcast) {
	if len(pending) == 0 {
		return
	}

	msgs := make([]sdktypes.Msg, len(pending))
	for i, item := range pending {
		// Keep the final THORChain submit batched per block, but build the
		// individual keygen messages in-process one at a time. In practice there
		// are at most two messages here (regular Asgard + XMR monovault), and the
		// real backup/encryption work inside buildKeygenMsg has shown signs of
		// wedging some nodes when both builders run concurrently.
		msg, err := s.buildKeygenMsg(item.height, item.poolPk, item.secp256k1Signature, item.blame, item.input, item.keygenType, item.chains, item.keygenTime, item.poolPubKeyEddsa, item.poolPubKeyFrost, item.frostKeyshareHex, item.observedMoneroHeight)
		if err != nil {
			s.errCounter.WithLabelValues("fail_to_broadcast_keygen", "").Inc()
			s.logger.Error().Err(err).Str("keygen_type", item.keygenType.String()).Msg("fail to build keygen")
			return
		}
		msgs[i] = msg
	}

	txID, err := s.broadcastKeygenMsgs(pending[0].height, msgs...)
	if err != nil {
		s.errCounter.WithLabelValues("fail_to_broadcast_keygen", "").Inc()
		s.logger.Error().Err(err).Msg("fail to broadcast keygen")
		return
	}

	for _, item := range pending {
		s.logger.Info().
			Stringer("txid", txID).
			Int64("block", item.height).
			Str("keygen_type", item.keygenType.String()).
			Msg("sent keygen tx to thorchain")

		// Broadcast all keygen messages for the block together so the XMR
		// monovault vote cannot be stranded behind a separate THORChain tx path.
		if !item.poolPk.IsEmpty() {
			algo := common.SigningAlgoSecp256k1
			if item.keygenType == ttypes.KeygenType_XMRMonovaultKeygen {
				algo = common.SigningAlgoEd25519
			}
			s.pubkeyMgr.AddPubKey(item.poolPk, true, algo)
		}
		if !item.poolPubKeyEddsa.IsEmpty() {
			s.pubkeyMgr.AddPubKey(item.poolPubKeyEddsa, true, common.SigningAlgoEd25519)
		}
		for _, pk := range item.input {
			s.pubkeyMgr.AddPubKey(pk, false, common.SigningAlgoSecp256k1)
		}
	}
}

func (s *Signer) keygenChainsForType(keygenType ttypes.KeygenType) common.Chains {
	if keygenType == ttypes.KeygenType_XMRMonovaultKeygen {
		return common.Chains{common.THORChain, common.XMRChain}
	}
	chains := common.Chains{common.THORChain}
	for chain, chainCfg := range s.cfg.GetChains() {
		if chainCfg.OptToRetire || chainCfg.Disabled || chain == common.XMRChain {
			continue
		}
		chains = append(chains, chain)
	}
	return chains.Distinct()
}

func (s *Signer) runCustomKeygenHooks(
	chains common.Chains,
	keys []string,
	blockHeight int64,
	poolPubKey common.PubKey,
	poolPubKeyEddsa common.PubKey,
) (common.PubKey, common.PubKey, string, int64, error) {
	version := ""
	if v, err := s.thorchainBridge.GetThorchainVersion(); err != nil {
		s.logger.Debug().Err(err).Msg("fail to fetch thorchain version for custom keygen")
	} else {
		version = v.String()
	}

	var customHookErr error
	effectivePoolPubKeyEddsa := poolPubKeyEddsa
	var frostPoolPubKey common.PubKey
	var frostKeyshareHex string
	var observedMoneroHeight int64
	for chainID, chainClient := range s.chains {
		if !chains.Has(chainID) {
			continue
		}
		custom, ok := chainClient.(chainclients.CustomKeygenChainClient)
		if !ok || !custom.CustomKeygen() {
			continue
		}

		result, err := custom.Keygen(chainclienttypes.CustomKeygenRequest{
			Members:          keys,
			BlockHeight:      blockHeight,
			ThorchainVersion: version,
			PoolPubKey:       poolPubKey,
			PoolPubKeyEddsa:  effectivePoolPubKeyEddsa,
		})
		if err != nil {
			s.logger.Error().Err(err).Str("chain", chainID.String()).Msg("custom keygen failed")
			for _, b := range result.Blame {
				s.logger.Error().
					Str("chain", chainID.String()).
					Str("reason", b.FailReason).
					Interface("nodes", b.BlameNodes).
					Msg("custom keygen blame")
			}
			customHookErr = errors.Join(customHookErr, fmt.Errorf("custom keygen failed for %s: %w", chainID.String(), err))
			continue
		}
		if result.FrostKeyshareHex != "" {
			frostKeyshareHex = result.FrostKeyshareHex
		}
		if result.ObservedMoneroHeight > 0 {
			observedMoneroHeight = result.ObservedMoneroHeight
		}
		if !result.PoolPubKeyFrost.IsEmpty() {
			frostPoolPubKey = result.PoolPubKeyFrost
		}
		if chainID == common.XMRChain && !result.PoolPubKeyFrost.IsEmpty() {
			effectivePoolPubKeyEddsa = common.EmptyPubKey
		}
		if !result.PoolPubKeyEddsa.IsEmpty() && !result.PoolPubKeyEddsa.Equals(effectivePoolPubKeyEddsa) {
			effectivePoolPubKeyEddsa = result.PoolPubKeyEddsa
			s.logger.Info().
				Str("chain", chainID.String()).
				Str("pool_pubkey_eddsa", result.PoolPubKeyEddsa.String()).
				Str("pool_pubkey_frost", frostPoolPubKey.String()).
				Msg("custom keygen updated eddsa vault pubkey (FROST group key)")
		}
		if !result.Address.IsEmpty() {
			s.logger.Info().
				Str("chain", chainID.String()).
				Str("address", result.Address.String()).
				Msg("custom keygen produced chain address")
		}
	}
	return effectivePoolPubKeyEddsa, frostPoolPubKey, frostKeyshareHex, observedMoneroHeight, customHookErr
}

// secp256k1VerificationSignature will make a best effort to sign the public key with
// its own private key as a sanity check to ensure parties are able to sign. The
// signature will be included in the TssPool message if successful, and verified by
// THORNode before the keygen is accepted.
func (s *Signer) secp256k1VerificationSignature(pk common.PubKey) []byte {
	// create keysign instance
	ks, err := tss.NewKeySign(s.tssServer, s.thorchainBridge)
	if err != nil {
		s.logger.Error().Err(err).Msg("fail to create keysign for secp256k1 check signing")
		return nil
	}
	ks.Start()
	defer ks.Stop()

	// sign the public key with its own private key
	data := []byte(pk.String())
	sigBytes, _, err := ks.RemoteSign(data, common.SigningAlgoSecp256k1, pk.String())
	if err != nil {
		// this is expected in some cases if we were not in the signing party
		s.logger.Info().Err(err).Msg("fail secp256k1 check signing")
		return nil

	} else if sigBytes == nil {
		// This is expected in other cases when not in the signing party,
		// when RemoteSign's len(resp.R) and len(resp.S) are both nil.
		return nil
	}

	// build the signature
	if len(sigBytes) < 64 {
		s.logger.Error().Int("len", len(sigBytes)).Msg("unexpected sigBytes length")
		return nil
	}
	r := new(big.Int).SetBytes(sigBytes[:32])
	ss := new(big.Int).SetBytes(sigBytes[32:])
	signature := &btcec.Signature{R: r, S: ss}

	// verify the signature (thornode will also verify and reject if invalid)
	spk, err := pk.Secp256K1()
	if err != nil {
		s.logger.Error().Err(err).Msg("fail to get secp256k1 pubkey")
		return nil
	}
	if !signature.Verify(data, spk) {
		s.logger.Error().Msg("secp256k1 check signature verification failed")
	} else {
		s.logger.Info().Msg("secp256k1 check signature verified")
	}

	return sigBytes
}

func (s *Signer) buildKeygenMsg(height int64, poolPk common.PubKey, secp256k1Signature []byte, blame []ttypes.Blame, input common.PubKeys, keygenType ttypes.KeygenType, chains common.Chains, keygenTime int64, poolPubKeyEddsa common.PubKey, poolPubKeyFrost common.PubKey, frostKeyshareHex string, observedMoneroHeight int64) (sdktypes.Msg, error) {
	secpKeysharePath := ""
	eddsaKeysharePath := ""
	if s.cfg.Signer.BackupKeyshares && keygenType != ttypes.KeygenType_XMRMonovaultKeygen {
		if !poolPk.IsEmpty() {
			secpKeysharePath = filepath.Join(app.DefaultNodeHome, fmt.Sprintf("localstate-%s.json", poolPk))
		}
		if !poolPubKeyEddsa.IsEmpty() {
			eddsaKeysharePath = filepath.Join(app.DefaultNodeHome, fmt.Sprintf("localstate-%s.json", poolPubKeyEddsa))
		}
	}

	backups := s.buildKeygenBackups(secpKeysharePath, eddsaKeysharePath, frostKeyshareHex)
	keygenMsg, err := s.thorchainBridge.GetKeygenStdTx(poolPk, secp256k1Signature, backups.keyshares, blame, input, keygenType, chains, height, keygenTime, poolPubKeyEddsa, backups.keysharesEddsa, backups.keysharesFrost, poolPubKeyFrost, observedMoneroHeight)
	if err != nil {
		return nil, fmt.Errorf("fail to get keygen id: %w", err)
	}
	return keygenMsg, nil
}

func (s *Signer) sendKeygenToThorchain(height int64, poolPk common.PubKey, secp256k1Signature []byte, blame []ttypes.Blame, input common.PubKeys, keygenType ttypes.KeygenType, keygenTime int64, poolPubKeyEddsa common.PubKey, poolPubKeyFrost common.PubKey, frostKeyshareHex string, observedMoneroHeight int64) error {
	chains := s.keygenChainsForType(keygenType)
	keygenMsg, err := s.buildKeygenMsg(height, poolPk, secp256k1Signature, blame, input, keygenType, chains, keygenTime, poolPubKeyEddsa, poolPubKeyFrost, frostKeyshareHex, observedMoneroHeight)
	if err != nil {
		return err
	}

	txID, err := s.broadcastKeygenMsgs(height, keygenMsg)
	if err != nil {
		return err
	}

	s.logger.Info().
		Stringer("txid", txID).
		Int64("block", height).
		Str("keygen_type", keygenType.String()).
		Msg("sent keygen tx to thorchain")

	return nil
}

func (s *Signer) buildKeygenBackups(secpKeysharePath, eddsaKeysharePath, frostKeyshareHex string) keygenBackups {
	if !s.cfg.Signer.BackupKeyshares {
		return keygenBackups{}
	}

	type backupResult struct {
		kind string
		data []byte
		err  error
		path string
	}

	results := make(chan backupResult, 3)
	seedPhrase, seedErr := common.GetSignerSeedPhrase()
	if seedErr != nil {
		s.logger.Error().Err(seedErr).Msg("fail to get signer seed phrase")
		return keygenBackups{}
	}
	expected := 0

	if secpKeysharePath != "" {
		expected++
		path := secpKeysharePath
		go func() {
			data, err := encryptKeysharesFn(path, seedPhrase)
			results <- backupResult{kind: "secp256k1", data: data, err: err, path: path}
		}()
	}

	if eddsaKeysharePath != "" {
		if _, statErr := os.Stat(eddsaKeysharePath); statErr == nil {
			expected++
			path := eddsaKeysharePath
			go func() {
				data, err := encryptKeysharesFn(path, seedPhrase)
				results <- backupResult{kind: "eddsa", data: data, err: err, path: path}
			}()
		} else if !errors.Is(statErr, os.ErrNotExist) {
			s.logger.Error().Err(statErr).Str("path", eddsaKeysharePath).Msg("fail to stat eddsa keyshares")
		}
	}

	if frostKeyshareHex != "" {
		rawKey, decodeErr := hex.DecodeString(frostKeyshareHex)
		if decodeErr != nil {
			s.logger.Error().Err(decodeErr).Msg("fail to decode frost keyshare hex")
		} else {
			expected++
			go func(raw []byte) {
				data, err := encryptRawKeysharesFn(raw, seedPhrase)
				results <- backupResult{kind: "frost", data: data, err: err}
			}(rawKey)
		}
	}

	var backups keygenBackups
	for i := 0; i < expected; i++ {
		result := <-results
		if result.err != nil {
			switch result.kind {
			case "secp256k1":
				s.logger.Error().Err(result.err).Str("path", result.path).Msg("fail to encrypt secp256k1 keyshares")
			case "eddsa":
				s.logger.Error().Err(result.err).Str("path", result.path).Msg("fail to encrypt eddsa keyshares")
			case "frost":
				s.logger.Error().Err(result.err).Msg("fail to encrypt frost keyshare")
			}
			continue
		}
		switch result.kind {
		case "secp256k1":
			backups.keyshares = result.data
		case "eddsa":
			backups.keysharesEddsa = result.data
		case "frost":
			backups.keysharesFrost = result.data
		}
	}

	return backups
}

func (s *Signer) broadcastKeygenMsgs(height int64, msgs ...sdktypes.Msg) (common.TxID, error) {
	if len(msgs) == 0 {
		return common.BlankTxID, nil
	}
	strHeight := strconv.FormatInt(height, 10)
	var txID common.TxID

	bf := backoff.NewExponentialBackOff()
	bf.MaxElapsedTime = time.Minute
	err := backoff.Retry(func() error {
		var err error
		txID, err = s.thorchainBridge.Broadcast(msgs...)
		if err != nil {
			s.logger.Warn().Err(err).Msg("fail to send keygen tx to thorchain")
			s.errCounter.WithLabelValues("fail_to_send_to_thorchain", strHeight).Inc()
			return fmt.Errorf("fail to send the tx to thorchain: %w", err)
		}
		return nil
	}, bf)
	return txID, err
}

// signAndBroadcast will sign the tx and broadcast it to the corresponding chain. On
// SignTx error for the chain client, if we receive checkpoint bytes we also return them
// with the error so they can be set on the TxOutStoreItem and re-used on a subsequent
// retry to avoid double spend. The second returned value is an optional observation
// that should be submitted to THORChain.
func (s *Signer) signAndBroadcast(item TxOutStoreItem) ([]byte, *types.TxInItem, error) {
	height := item.Height
	tx := item.TxOutItem

	// set the checkpoint on the tx out item if it was stored
	if item.Checkpoint != nil {
		tx.Checkpoint = item.Checkpoint
	}

	blockHeight, err := s.thorchainBridge.GetBlockHeight()
	if err != nil {
		s.logger.Error().Err(err).Msgf("fail to get block height")
		return nil, nil, err
	}
	signingTransactionPeriod, err := s.constantsProvider.GetInt64Value(blockHeight, constants.SigningTransactionPeriod)
	s.logger.Debug().Msgf("signing transaction period:%d", signingTransactionPeriod)
	if err != nil {
		s.logger.Error().Err(err).Msgf("fail to get constant value for(%s)", constants.SigningTransactionPeriod)
		return nil, nil, err
	}

	// if in round 7 retry, discard outbound if over the max outbound attempts
	inactiveVaultRound7Retry := false
	if item.Round7Retry {
		mimirKey := "MAXOUTBOUNDATTEMPTS"
		var maxOutboundAttemptsMimir int64
		maxOutboundAttemptsMimir, err = s.thorchainBridge.GetMimir(mimirKey)
		if err != nil {
			s.logger.Err(err).Msgf("fail to get %s", mimirKey)
			return nil, nil, err
		}
		attempt := (blockHeight - height) / signingTransactionPeriod
		if attempt > maxOutboundAttemptsMimir {
			s.logger.Warn().
				Int64("outbound_height", height).
				Int64("current_height", blockHeight).
				Int64("attempt", attempt).
				Msg("round 7 retry outbound tx has reached max outbound attempts")
			return nil, nil, nil
		}

		// determine if the round 7 retry is for an inactive vault
		var vault ttypes.Vault
		vault, err = s.thorchainBridge.GetVault(item.TxOutItem.VaultPubKey.String())
		if err != nil {
			log.Err(err).
				Stringer("vault_pubkey", item.TxOutItem.VaultPubKey).
				Msg("failed to get tx out item vault")
			return nil, nil, err
		}
		inactiveVaultRound7Retry = vault.Status == ttypes.VaultStatus_InactiveVault
	}

	// if not in round 7 retry or the round 7 retry is on an inactive vault, discard
	// outbound if within configured blocks of reschedule
	if !item.Round7Retry || inactiveVaultRound7Retry {
		if blockHeight-signingTransactionPeriod > height-s.cfg.Signer.RescheduleBufferBlocks {
			s.logger.Error().Msgf("tx was created at block height(%d), now it is (%d), it is older than (%d) blocks, skip it", height, blockHeight, signingTransactionPeriod)
			return nil, nil, nil
		}
	}

	chain, err := s.getChain(tx.Chain)
	if err != nil {
		s.logger.Error().Err(err).Msgf("not supported %s", tx.Chain.String())
		return nil, nil, err
	}
	halted, global, err := signing.CheckSigningHalt(s.thorchainBridge, tx.Chain, blockHeight)
	if err != nil {
		s.logger.Err(err).Msg("fail to check signing halt")
		return nil, nil, err
	}
	if halted {
		if global {
			s.logger.Info().Msg("signing has been halted globally")
		} else {
			s.logger.Info().Msgf("signing for %s is halted", tx.Chain)
		}
		return nil, nil, nil
	}
	if !s.shouldSign(tx) {
		s.logger.Info().Str("signer_address", chain.GetAddress(tx.VaultPubKey)).Msg("different pool address, ignore")
		return nil, nil, nil
	}

	if len(tx.ToAddress) == 0 {
		s.logger.Info().Msg("To address is empty, THORNode don't know where to send the fund , ignore")
		return nil, nil, nil // return nil and discard item
	}

	// don't sign if the block scanner is unhealthy. This is because the
	// network may not be able to detect the outbound transaction, and
	// therefore reschedule the transaction to another signer. In a disaster
	// scenario, the network could broadcast a transaction several times,
	// bleeding funds.
	if !chain.IsBlockScannerHealthy() {
		return nil, nil, fmt.Errorf("the block scanner for chain %s is unhealthy, not signing transactions due to it", chain.GetChain())
	}

	start := time.Now()
	defer func() {
		if h := s.m.GetHistograms(metrics.SignAndBroadcastDuration(chain.GetChain())); h != nil {
			h.Observe(time.Since(start).Seconds())
		}
	}()

	if !tx.OutHash.IsEmpty() {
		s.logger.Info().Str("OutHash", tx.OutHash.String()).Msg("tx had been sent out before")
		return nil, nil, nil // return nil and discard item
	}

	// We get the keysign object from thorchain again to ensure it hasn't
	// been signed already, and we can skip. This helps us not get stuck on
	// a task that we'll never sign, because 2/3rds already has and will
	// never be available to sign again.
	txOut, err := s.thorchainBridge.GetKeysign(height, tx.VaultPubKey.String())
	if err != nil {
		s.logger.Error().Err(err).Msg("fail to get keysign items")
		return nil, nil, err
	}
	for _, txArray := range txOut.TxArray {
		if txArray.TxOutItem(item.TxOutItem.Height).Equals(tx) && !txArray.OutHash.IsEmpty() {
			// already been signed, we can skip it
			s.logger.Info().Str("tx_id", tx.OutHash.String()).Msgf("already signed. skipping...")
			return nil, nil, nil
		}
	}

	// If the chain client exposes a vault lock, hold it for sign+broadcast so
	// chain-specific consolidation routines cannot race normal outbounds.
	lockKey := chainVaultLockKey(chain, tx)
	if lock := chainVaultLock(chain, lockKey); lock != nil {
		lock.Lock()
		defer lock.Unlock()
	}

	// If SignedTx is set, we already signed and should only retry broadcast.
	var signedTx, checkpoint []byte
	var elapse time.Duration
	var observation *types.TxInItem
	if len(item.SignedTx) > 0 {
		s.logger.Info().Str("memo", tx.Memo).Msg("retrying broadcast of already signed tx")
		signedTx = item.SignedTx
		observation = item.Observation
	} else {
		tx.SigningID = item.Key()
		startKeySign := time.Now()
		signedTx, checkpoint, observation, err = chain.SignTx(tx, height)
		if err != nil {
			s.logger.Error().Err(err).Msg("fail to sign tx")
			return checkpoint, nil, err
		}
		elapse = time.Since(startKeySign)

		// store immediately after a successful sign so the signed payload is saved even if broadcast crashes
		item.SignedTx = signedTx
		if storeErr := s.storage.Set(item); storeErr != nil {
			s.logger.Error().Err(storeErr).Msg("fail to persist signed tx before broadcast")
		}
	}

	// looks like the transaction is already signed
	if len(signedTx) == 0 {
		s.logger.Warn().Msgf("signed transaction is empty")
		return nil, nil, nil
	}

	// broadcast the transaction
	hash, err := chain.BroadcastTx(tx, signedTx)
	if err != nil {
		s.logger.Error().Err(err).Str("memo", tx.Memo).Msg("fail to broadcast tx to chain")

		// store the signed tx for the next retry
		item.SignedTx = signedTx
		item.Observation = observation
		if storeErr := s.storage.Set(item); storeErr != nil {
			s.logger.Error().Err(storeErr).Msg("fail to update tx out store item with signed tx")
		}

		return nil, observation, err
	}
	s.logger.Info().
		Str("chain", chain.GetChain().String()).
		Str("txid", hash).
		Str("memo", tx.Memo).
		Msg("broadcasted tx to chain")

	if s.isTssKeysign(tx.VaultPubKey) || s.isTssKeysign(tx.VaultPubKeyEddsa) {
		s.tssKeysignMetricMgr.SetTssKeysignMetric(hash, elapse.Milliseconds())
	}

	return nil, observation, nil
}

func (s *Signer) isTssKeysign(pubKey common.PubKey) bool {
	return !s.localPubKeyECDSA.Equals(pubKey) && !s.localPubKeyEDDSA.Equals(pubKey)
}

// Stop the signer process
func (s *Signer) Stop() error {
	s.logger.Info().Msg("receive request to stop signer")
	defer s.logger.Info().Msg("signer stopped successfully")
	close(s.stopChan)
	s.wg.Wait()
	if err := s.m.Stop(); err != nil {
		s.logger.Error().Err(err).Msg("fail to stop metric server")
	}
	s.blockScanner.Stop()
	return s.storage.Close()
}

////////////////////////////////////////////////////////////////////////////////////////
// pipelineSigner Interface
////////////////////////////////////////////////////////////////////////////////////////

func (s *Signer) isStopped() bool {
	select {
	case <-s.stopChan:
		return true
	default:
		return false
	}
}

func (s *Signer) storageList() []TxOutStoreItem {
	return s.storage.List()
}

func (s *Signer) processTransaction(item TxOutStoreItem) {
	s.logger.Info().
		Str("chain", item.TxOutItem.Chain.String()).
		Int64("height", item.Height).
		Int("status", int(item.Status)).
		Interface("tx", item.TxOutItem).
		Msg("Signing transaction")

	ctx, cancel := context.WithTimeout(context.Background(), signingTimeoutForChain(item.TxOutItem.Chain))
	checkpoint, obs, err := runWithContext(ctx, func() ([]byte, *types.TxInItem, error) {
		return s.signAndBroadcast(item)
	})

	// Apply spent output refs from the signing observation before handling
	// errors. The observation is populated during FROST signing and contains
	// the real inputs consumed. Applying immediately ensures the sidecar
	// marks outputs spent even when broadcast fails (another signer may have
	// broadcast the same tx successfully).
	// Fall back to the stored observation from a prior signing attempt.
	obsForRefs := obs
	if obsForRefs == nil {
		obsForRefs = item.Observation
	}
	s.applySpentOutputRefsFromObs(item.TxOutItem, obsForRefs)

	if err != nil {
		// Always store the checkpoint of the built tx if we have one. This ensures the same
		// vault will never attempt multiple txs for an outbound, as further protection
		// against cases like https://gitlab.com/thorchain/thornode/-/merge_requests/4344.
		// In tradeoff, we accept a higher likelihood of temporary stuck outbounds.
		ksErr := tss.KeysignError{}
		isRound7 := errors.As(err, &ksErr) && ksErr.IsRound7()
		if checkpoint != nil || isRound7 {
			item.Checkpoint = checkpoint
			if isRound7 {
				s.logger.Error().Err(err).Interface("tx", item.TxOutItem).Msg("round 7 signing error")
				item.Round7Retry = true
			}
			if storeErr := s.storage.Set(item); storeErr != nil {
				s.logger.Error().Err(storeErr).Msg("fail to persist checkpoint after sign failure")
			}
		}

		s.logger.Error().Interface("tx", item.TxOutItem).Err(err).Msg("fail to sign and broadcast tx out store item")
		cancel()
		return
		// The 'item' for loop should not be items[0],
		// because problems which return 'nil, nil' should be skipped over instead of blocking others.
		// When signAndBroadcast returns an error (such as from a keysign timeout),
		// a 'return' and not a 'continue' should be used so that nodes can all restart the list,
		// for when the keysign failure was from a loss of list synchrony.
		// Otherwise, out-of-sync lists would cycle one timeout at a time, maybe never resynchronising.
	}
	cancel()

	// if enabled and the observation is non-nil, instant observe the outbound
	if s.shouldAutoObserve(item.TxOutItem) && obs != nil {
		s.observer.ObserveSigned(types.TxIn{
			Chain:                  item.TxOutItem.Chain,
			TxArray:                []*types.TxInItem{obs},
			MemPool:                true,
			Filtered:               true,
			ConfirmationRequired:   0,
			AllowFutureObservation: true,
		})
	}

	// We have a successful broadcast! Remove the item from our store
	if err = s.storage.Remove(item); err != nil {
		s.logger.Error().Err(err).Msg("fail to update tx out store item")
	}
}

func signingTimeoutForChain(chain common.Chain) time.Duration {
	if chain.Equals(common.XMRChain) {
		return 15 * time.Minute
	}
	// a single non-XMR keysign should not take longer than 5 minutes, regardless TSS or local
	return 5 * time.Minute
}

func (s *Signer) applySpentOutputRefsFromObs(tx types.TxOutItem, obs *types.TxInItem) {
	if obs == nil || len(obs.SpentOutputRefs) == 0 {
		return
	}
	chain, ok := s.chains[tx.Chain]
	if !ok {
		return
	}
	type spentRefApplier interface {
		ApplySpentOutputRefs(common.ObservedTx)
	}
	applier, ok := chain.(spentRefApplier)
	if !ok {
		return
	}
	obsTx := common.NewObservedTx(
		common.NewTx(
			common.TxID(obs.Tx),
			common.Address(obs.Sender),
			common.Address(obs.To),
			obs.Coins,
			obs.Gas,
			obs.Memo,
		),
		obs.BlockHeight,
		obs.ObservedVaultPubKey,
		obs.BlockHeight,
	)
	obsTx.SpentOutputRefs = common.OutputRefsFromPointers(obs.SpentOutputRefs)
	s.logger.Info().
		Str("tx_hash", obs.Tx).
		Int("refs", len(obs.SpentOutputRefs)).
		Msg("applying spent output refs from signing observation")
	applier.ApplySpentOutputRefs(obsTx)
}
