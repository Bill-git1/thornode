package observer

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"gitlab.com/thorchain/thornode/v3/bifrost/metrics"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients"
	"gitlab.com/thorchain/thornode/v3/bifrost/pubkeymanager"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/config"
	"gitlab.com/thorchain/thornode/v3/constants"
	stypes "gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

// signedTxOutCacheSize is the number of signed tx out observations to keep in memory
// to prevent duplicate observations. Based on historical data at the time of writing,
// the peak of Thorchain's L1 swaps was 10k per day.
const signedTxOutCacheSize = 10_000

// deckRefreshTime is the time to wait before reconciling txIn status.
const deckRefreshTime = 1 * time.Second

// Observer observer service
type Observer struct {
	logger                zerolog.Logger
	chains                map[common.Chain]chainclients.ChainClient
	stopChan              chan struct{}
	pubkeyMgr             *pubkeymanager.PubKeyManager
	onDeck                []*types.TxIn
	lock                  *sync.Mutex
	globalTxsQueue        chan types.TxIn
	globalErrataQueue     chan types.ErrataBlock
	globalSolvencyQueue   chan types.Solvency
	globalNetworkFeeQueue chan common.NetworkFee
	m                     *metrics.Metrics
	errCounter            *prometheus.CounterVec
	thorchainBridge       thorclient.ThorchainBridge
	storage               *ObserverStorage
	tssKeysignMetricMgr   *metrics.TssKeysignMetricMgr

	// signedTxOutCache is a cache to keep track of observations for outbounds which were
	// manually observed after completion of signing and should be filtered from future
	// mempool and block observations.
	signedTxOutCache  *lru.Cache
	attestationGossip *AttestationGossip
}

// NewObserver create a new instance of Observer for chain
func NewObserver(pubkeyMgr *pubkeymanager.PubKeyManager,
	chains map[common.Chain]chainclients.ChainClient,
	thorchainBridge thorclient.ThorchainBridge,
	m *metrics.Metrics, dataPath string,
	tssKeysignMetricMgr *metrics.TssKeysignMetricMgr,
	attestationGossip *AttestationGossip,
) (*Observer, error) {
	logger := log.Logger.With().Str("module", "observer").Logger()

	cfg := config.GetBifrost()

	storage, err := NewObserverStorage(dataPath, cfg.ObserverLevelDB)
	if err != nil {
		return nil, fmt.Errorf("failed to create observer storage: %w", err)
	}
	if tssKeysignMetricMgr == nil {
		return nil, fmt.Errorf("tss keysign manager is nil")
	}

	signedTxOutCache, err := lru.New(signedTxOutCacheSize)
	if err != nil {
		return nil, fmt.Errorf("failed to create signed tx out cache: %w", err)
	}

	return &Observer{
		logger:                logger,
		chains:                chains,
		stopChan:              make(chan struct{}),
		m:                     m,
		pubkeyMgr:             pubkeyMgr,
		lock:                  &sync.Mutex{},
		globalTxsQueue:        make(chan types.TxIn),
		globalErrataQueue:     make(chan types.ErrataBlock),
		globalSolvencyQueue:   make(chan types.Solvency),
		globalNetworkFeeQueue: make(chan common.NetworkFee),
		errCounter:            m.GetCounterVec(metrics.ObserverError),
		thorchainBridge:       thorchainBridge,
		storage:               storage,
		tssKeysignMetricMgr:   tssKeysignMetricMgr,
		signedTxOutCache:      signedTxOutCache,
		attestationGossip:     attestationGossip,
	}, nil
}

func (o *Observer) getChain(chainID common.Chain) (chainclients.ChainClient, error) {
	chain, ok := o.chains[chainID]
	if !ok {
		o.logger.Debug().Str("chain", chainID.String()).Msg("is not supported yet")
		return nil, errors.New("not supported")
	}
	return chain, nil
}

func (o *Observer) Start(ctx context.Context) error {
	o.restoreDeck()
	for _, chain := range o.chains {
		chain.Start(o.globalTxsQueue, o.globalErrataQueue, o.globalSolvencyQueue, o.globalNetworkFeeQueue)
	}
	go o.processTxIns()
	go o.processErrataTx(ctx)
	go o.processSolvencyQueue(ctx)
	go o.processNetworkFeeQueue(ctx)
	go o.deck()
	go o.attestationGossip.Start(ctx)
	return nil
}

// ObserveSigned is called when a tx is signed by the signer and returns an observation that should be immediately submitted.
// Observations passed to this method with 'allowFutureObservation' false will be cached in memory and skipped if they are later observed in the mempool or block.
func (o *Observer) ObserveSigned(txIn types.TxIn) {
	if !txIn.AllowFutureObservation {
		// add all transaction ids to the signed tx out cache
		for _, tx := range txIn.TxArray {
			o.signedTxOutCache.Add(tx.Tx, nil)
		}
	}

	o.globalTxsQueue <- txIn
}

func (o *Observer) restoreDeck() {
	onDeckTxs, err := o.storage.GetOnDeckTxs()
	if err != nil {
		o.logger.Error().Err(err).Msg("fail to restore ondeck txs")
	}
	o.lock.Lock()
	defer o.lock.Unlock()
	o.onDeck = onDeckTxs
}

func (o *Observer) deck() {
	for {
		select {
		case <-o.stopChan:
			o.sendDeck()
			return
		case <-time.After(deckRefreshTime):
			o.sendDeck()
		}
	}
}

// handleObservedTxCommitted will be called when an observed tx has been committed to thorchain,
// notified via AttestationGossip's grpc subscription to thornode..
func (o *Observer) handleObservedTxCommitted(tx common.ObservedTx) {
	madeChanges := false

	isFinal := tx.IsFinal()

	o.lock.Lock()
	defer o.lock.Unlock()
DeckLoop:
	for i, deck := range o.onDeck {
		if deck.Chain != tx.Tx.Chain {
			// if the chain of the observed tx in the deck is not the same as the chain of the committed observed tx, skip it.
			continue
		}

		for j, txInItem := range deck.TxArray {
			txInItemID, err := common.NewTxID(txInItem.Tx)
			if err != nil {
				o.logger.Error().Err(err).Msg("fail to parse tx id")
				continue
			}
			if tx.Tx.ID.Equals(txInItemID) {
				o.logger.Debug().Msgf("tx found in deck %s - %s", tx.Tx.Chain, tx.Tx.ID)
				if isFinal {
					o.logger.Debug().Msgf("tx final %s - %s removing from tx array", tx.Tx.Chain, tx.Tx.ID)
					// if the tx is in the tx array, and it is final, remove it from the tx array.
					deck.TxArray = append(deck.TxArray[:j], deck.TxArray[j+1:]...)
					if len(deck.TxArray) == 0 {
						o.logger.Debug().Msgf("deck is empty, removing from ondeck")

						// if the deck is empty after removing, remove it from ondeck.
						o.onDeck = append(o.onDeck[:i], o.onDeck[i+1:]...)
					}
				} else {
					// if the tx is not final, set tx.CommittedUnFinalised to true to indicate that it has been committed to thorchain but not finalised yet.
					deck.TxArray[j].CommittedUnFinalised = true
				}

				chain, err := o.getChain(deck.Chain)
				if err != nil {
					o.logger.Error().Err(err).Msg("chain not found")
				} else {
					chain.OnObservedTxIn(*txInItem, txInItem.BlockHeight)
				}

				madeChanges = true
				break DeckLoop
			}
		}
	}

	if !madeChanges {
		o.logger.Debug().Msgf("no changes made to ondeck, size: %d", len(o.onDeck))

		// do not save the onDeck to storage if no changes were made.
		return
	}

	o.logger.Debug().Msgf("new deck size after handle: %d", len(o.onDeck))

	// if the deck is not empty, save it back to key value store
	if err := o.storage.SetOnDeckTxs(o.onDeck); err != nil {
		o.logger.Error().Err(err).Msg("fail to save ondeck tx to key value store")
	}
}

func (o *Observer) sendDeck() {
	// check if node is active
	nodeStatus, err := o.thorchainBridge.FetchNodeStatus()
	if err != nil {
		o.logger.Error().Err(err).Msg("failed to get node status")
		return
	}
	if nodeStatus != stypes.NodeStatus_Active {
		o.logger.Warn().Msg("node is not active, will not handle tx in")
		return
	}

	// fetch and update active validator count on attestation gossip so it can calculate quorum
	activeVals, err := o.thorchainBridge.FetchActiveNodes()
	if err != nil {
		o.logger.Error().Err(err).Msg("failed to get active node count")
		return
	}
	o.attestationGossip.setActiveValidators(activeVals)

	o.lock.Lock()
	defer o.lock.Unlock()

	for _, deck := range o.onDeck {
		chainClient, err := o.getChain(deck.Chain)
		if err != nil {
			o.logger.Error().Err(err).Msg("fail to retrieve chain client")
			continue
		}

		final := chainClient.ConfirmationCountReady(*deck)
		o.sendToQuorumChecker(deck, final)
	}
}

func (o *Observer) sendToQuorumChecker(deck *types.TxIn, finalised bool) {
	txs, err := o.getThorchainTxIns(deck, finalised)
	if err != nil {
		o.logger.Error().Err(err).Msg("fail to convert txin to thorchain txins")
		return
	}

	if len(txs) == 0 {
		// no tx to send
		return
	}

	inbound, outbound, err := o.thorchainBridge.GetInboundOutbound(txs)
	if err != nil {
		o.logger.Error().Err(err).Msg("fail to get inbound and outbound txs")
		return
	}

	for _, tx := range inbound {
		if err := o.attestationGossip.AttestObservedTx(context.Background(), &tx, true); err != nil {
			o.logger.Err(err).Msg("fail to send inbound tx to thorchain")
		}
	}

	for _, tx := range outbound {
		if err := o.attestationGossip.AttestObservedTx(context.Background(), &tx, false); err != nil {
			o.logger.Err(err).Msg("fail to send outbound tx to thorchain")
		}
	}
}

func (o *Observer) processTxIns() {
	for {
		select {
		case <-o.stopChan:
			return
		case txIn := <-o.globalTxsQueue:
			o.processObservedTx(txIn)
		}
	}
}

// processObservedTx will process the observed tx, and either add it to the
// onDeck queue, or merge it with an existing tx in the onDeck queue.
func (o *Observer) processObservedTx(txIn types.TxIn) {
	o.lock.Lock()
	defer o.lock.Unlock()
	found := false
	for i, in := range o.onDeck {
		if in.Chain != txIn.Chain {
			continue
		}
		if in.MemPool != txIn.MemPool {
			continue
		}
		if in.AllowFutureObservation != txIn.AllowFutureObservation {
			continue
		}
		if len(in.TxArray) > 0 && len(txIn.TxArray) > 0 {
			if in.TxArray[0].BlockHeight != txIn.TxArray[0].BlockHeight {
				continue
			}
		}
		if !txIn.Filtered {
			// onDeck txs are already filtered, so we can safely assume we are merging non-filtered txs into filtered txs
			txIn.TxArray = o.filterObservations(txIn.Chain, txIn.TxArray, txIn.MemPool)
			if len(txIn.TxArray) == 0 {
				o.logger.Debug().Msgf("txin is empty after filtering for existing, ignore it")
				return
			}
		}
		o.onDeck[i].TxArray = append(o.onDeck[i].TxArray, txIn.TxArray...)
		found = true
		break
	}
	if !found {
		if !txIn.Filtered {
			txIn.TxArray = o.filterObservations(txIn.Chain, txIn.TxArray, txIn.MemPool)
			if len(txIn.TxArray) == 0 {
				o.logger.Debug().Msgf("txin is empty after filtering for new, ignore it")
				return
			}
			txIn.ConfirmationRequired = o.chains[txIn.Chain].GetConfirmationCount(txIn)
			txIn.Filtered = true
		}
		// only append filtered txs
		o.onDeck = append(o.onDeck, &txIn)
	}
	if err := o.storage.SetOnDeckTxs(o.onDeck); err != nil {
		o.logger.Err(err).Msg("fail to save ondeck tx")
	}
}

func (o *Observer) filterObservations(chain common.Chain, items []*types.TxInItem, memPool bool) (txs []*types.TxInItem) {
	for _, txInItem := range items {
		// NOTE: the following could result in the same tx being added
		// twice, which is expected. We want to make sure we generate both
		// a inbound and outbound txn, if we both apply.

		isInternal := false
		// check if the from address is a valid pool
		if ok, cpi := o.pubkeyMgr.IsValidPoolAddress(txInItem.Sender, chain); ok {
			txInItem.ObservedVaultPubKey = cpi.PubKey
			isInternal = true

			// skip the outbound observation if we signed and manually observed
			if !o.signedTxOutCache.Contains(txInItem.Tx) {
				txs = append(txs, txInItem)
			}
		}
		// check if the to address is a valid pool address
		// for inbound message , if it is still in mempool , it will be ignored unless it is internal transaction
		// internal tx means both from & to addresses belongs to the network. for example migrate/consolidate
		if ok, cpi := o.pubkeyMgr.IsValidPoolAddress(txInItem.To, chain); ok && (!memPool || isInternal) {
			txInItem.ObservedVaultPubKey = cpi.PubKey
			txs = append(txs, txInItem)
		}
	}
	return
}

func (o *Observer) processErrataTx(ctx context.Context) {
	for {
		select {
		case <-o.stopChan:
			return
		case errataBlock, more := <-o.globalErrataQueue:
			if !more {
				return
			}
			// filter
			o.filterErrataTx(errataBlock)
			o.logger.Info().Msgf("Received a errata block %+v from the Thorchain", errataBlock.Height)
			for _, errataTx := range errataBlock.Txs {
				if err := o.attestationGossip.AttestErrata(ctx, common.ErrataTx{
					Chain: errataTx.Chain,
					Id:    errataTx.TxID,
				}); err != nil {
					o.errCounter.WithLabelValues("fail_to_broadcast_errata_tx", "").Inc()
					o.logger.Error().Err(err).Msg("fail to broadcast errata tx")
				}
			}
		}
	}
}

// filterErrataTx with confirmation counting logic in place, all inbound tx to asgard will be parked and waiting for confirmation count to reach
// the target threshold before it get forward to THORChain,  it is possible that when a re-org happened on BTC / ETH
// the transaction that has been re-org out ,still in bifrost memory waiting for confirmation, as such, it should be
// removed from ondeck tx queue, and not forward it to THORChain
func (o *Observer) filterErrataTx(block types.ErrataBlock) {
	o.lock.Lock()
	defer o.lock.Unlock()
	for _, tx := range block.Txs {
		for deckIdx, txIn := range o.onDeck {
			idx := -1
			for i, item := range txIn.TxArray {
				if item.Tx == tx.TxID.String() {
					idx = i
					break
				}
			}
			if idx != -1 {
				o.logger.Info().Msgf("drop tx (%s) from ondeck memory due to errata", tx.TxID)
				o.onDeck[deckIdx].TxArray = append(txIn.TxArray[:idx], txIn.TxArray[idx+1:]...) // nolint
			}
		}
	}
}

// getSaversMemo returns an add or withdraw memo for a Savers Vault
// If the tx is not a valid savers tx, an empty string will be returned
// Savers tx criteria:
// - Inbound amount must be gas asset
// - Inbound amount must be greater than the Dust Threshold of the tx chain (see chain.DustThreshold())
func (o *Observer) getSaversMemo(chain common.Chain, tx *types.TxInItem) string {
	// Savers txs should have one Coin input
	if len(tx.Coins) != 1 {
		return ""
	}

	txAmt := tx.Coins[0].Amount
	dustThreshold := chain.DustThreshold()

	// Below dust threshold, ignore
	if txAmt.LT(dustThreshold) {
		return ""
	}

	asset := tx.Coins[0].Asset
	synthAsset := asset.GetSyntheticAsset()
	bps := txAmt.Sub(dustThreshold)

	switch {
	case bps.IsZero():
		// Amount is too low, ignore
		return ""
	case bps.LTE(cosmos.NewUint(10_000)):
		// Amount is within or includes dustThreshold + 10_000, generate withdraw memo
		return fmt.Sprintf("-:%s:%s", synthAsset.String(), bps.String())
	default:
		// Amount is above dustThreshold + 10_000, generate add memo
		return fmt.Sprintf("+:%s", synthAsset.String())
	}
}

// getThorchainTxIns convert to the type thorchain expected
// maybe in later THORNode can just refactor this to use the type in thorchain
func (o *Observer) getThorchainTxIns(txIn *types.TxIn, finalized bool) (common.ObservedTxs, error) {
	obsTxs := make(common.ObservedTxs, 0, len(txIn.TxArray))
	o.logger.Debug().Msgf("len %d", len(txIn.TxArray))
	for _, item := range txIn.TxArray {
		if item.CommittedUnFinalised && !finalized {
			// we have already committed this tx in the un-finalized state,
			// and the tx is not yet final, so we should not send it again.
			continue
		}
		if item.Coins.IsEmpty() {
			o.logger.Info().Msgf("item(%+v) , coins are empty , so ignore", item)
			continue
		}
		if len([]byte(item.Memo)) > constants.MaxMemoSize {
			o.logger.Info().Msgf("tx (%s) memo (%s) too long", item.Tx, item.Memo)
			continue
		}

		// If memo is empty, see if it is a memo-less savers add or withdraw
		if strings.EqualFold(item.Memo, "") {
			memo := o.getSaversMemo(txIn.Chain, item)
			if !strings.EqualFold(memo, "") {
				o.logger.Info().Str("memo", memo).Str("txId", item.Tx).Msg("created savers memo")
				item.Memo = memo
			}
		}

		if len(item.To) == 0 {
			o.logger.Info().Msgf("tx (%s) to address is empty,ignore it", item.Tx)
			continue
		}
		o.logger.Debug().Str("tx-hash", item.Tx).Msg("txInItem")
		blockHeight := strconv.FormatInt(item.BlockHeight, 10)
		txID, err := common.NewTxID(item.Tx)
		if err != nil {
			o.errCounter.WithLabelValues("fail_to_parse_tx_hash", blockHeight).Inc()
			o.logger.Err(err).Msgf("fail to parse tx hash, %s is invalid", item.Tx)
			continue
		}
		sender, err := common.NewAddress(item.Sender)
		if err != nil {
			o.errCounter.WithLabelValues("fail_to_parse_sender", item.Sender).Inc()
			// log the error , and ignore the transaction, since the address is not valid
			o.logger.Err(err).Msgf("fail to parse sender, %s is invalid sender address", item.Sender)
			continue
		}

		to, err := common.NewAddress(item.To)
		if err != nil {
			o.errCounter.WithLabelValues("fail_to_parse_to", item.To).Inc()
			o.logger.Err(err).Msgf("fail to parse to, %s is invalid to address", item.To)
			continue
		}

		o.logger.Debug().Msgf("pool pubkey %s", item.ObservedVaultPubKey)
		chainAddr, err := item.ObservedVaultPubKey.GetAddress(txIn.Chain)
		o.logger.Debug().Msgf("%s address %s", txIn.Chain.String(), chainAddr)
		if err != nil {
			o.errCounter.WithLabelValues("fail to parse observed pool address", item.ObservedVaultPubKey.String()).Inc()
			o.logger.Err(err).Msgf("fail to parse observed pool address: %s", item.ObservedVaultPubKey.String())
			continue
		}
		height := item.BlockHeight
		if finalized {
			height += txIn.ConfirmationRequired
		}
		// Strip out any empty Coin from Coins and Gas, as even one empty Coin will make a MsgObservedTxIn for instance fail validation.
		tx := common.NewTx(txID, sender, to, item.Coins.NoneEmpty(), item.Gas.NoneEmpty(), item.Memo)
		obsTx := common.NewObservedTx(tx, height, item.ObservedVaultPubKey, item.BlockHeight+txIn.ConfirmationRequired)
		obsTx.KeysignMs = o.tssKeysignMetricMgr.GetTssKeysignMetric(item.Tx)
		obsTx.Aggregator = item.Aggregator
		obsTx.AggregatorTarget = item.AggregatorTarget
		obsTx.AggregatorTargetLimit = item.AggregatorTargetLimit
		obsTxs = append(obsTxs, obsTx)
	}
	return obsTxs, nil
}

func (o *Observer) processSolvencyQueue(ctx context.Context) {
	for {
		select {
		case <-o.stopChan:
			return
		case solvencyItem, more := <-o.globalSolvencyQueue:
			if !more {
				return
			}
			if solvencyItem.Chain.IsEmpty() || solvencyItem.Coins.IsEmpty() || solvencyItem.PubKey.IsEmpty() {
				continue
			}
			o.logger.Debug().Msgf("solvency:%+v", solvencyItem)
			if err := o.attestationGossip.AttestSolvency(ctx, common.Solvency{
				Chain:  solvencyItem.Chain,
				Height: solvencyItem.Height,
				PubKey: solvencyItem.PubKey,
				Coins:  solvencyItem.Coins,
			}); err != nil {
				o.errCounter.WithLabelValues("fail_to_broadcast_solvency", "").Inc()
				o.logger.Error().Err(err).Msg("fail to broadcast solvency tx")
			}
		}
	}
}

func (o *Observer) processNetworkFeeQueue(ctx context.Context) {
	for {
		select {
		case <-o.stopChan:
			return
		case networkFee := <-o.globalNetworkFeeQueue:
			if err := networkFee.Valid(); err != nil {
				o.logger.Error().Err(err).Msgf("invalid network fee - %s", networkFee.String())
				continue
			}
			if err := o.attestationGossip.AttestNetworkFee(ctx, networkFee); err != nil {
				o.logger.Err(err).Msg("fail to send network fee to thorchain")
			}
		}
	}
}

// Stop the observer
func (o *Observer) Stop() error {
	o.logger.Debug().Msg("request to stop observer")
	defer o.logger.Debug().Msg("observer stopped")

	for _, chain := range o.chains {
		chain.Stop()
	}

	close(o.stopChan)
	if err := o.pubkeyMgr.Stop(); err != nil {
		o.logger.Error().Err(err).Msg("fail to stop pool address manager")
	}
	if err := o.storage.Close(); err != nil {
		o.logger.Err(err).Msg("fail to close observer storage")
	}
	return o.m.Stop()
}
