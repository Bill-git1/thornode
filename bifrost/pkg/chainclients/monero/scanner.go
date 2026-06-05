package monero

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.uber.org/atomic"

	btypes "gitlab.com/thorchain/thornode/v3/bifrost/blockscanner/types"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/monero/sidecar"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient"
	stypes "gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
)

const (
	// Monero uses 1e12 atomic units and THORChain uses 1e8 decimals.
	moneroAtomicToThorchain = 10000

	// typicalVaultOutboundWeight is a conservative vault/FROST outbound tx
	// weight in Monero weight units used for THORChain network fee reporting.
	// We intentionally reserve more than a merely typical transfer so MaxGas
	// covers FROST vault outbounds without changing global gas accounting.
	typicalVaultOutboundWeight = 2500
)

// Scanner adapts monero daemon/wallet RPC to the generic block scanner fetcher interface.
type Scanner struct {
	logger          zerolog.Logger
	sidecar         *sidecar.Client
	bridge          thorclient.ThorchainBridge
	chain           common.Chain
	localNodePubKey common.PubKey
	vaultBootstrap  *vaultBootstrap
	reorgStore      *ReorgStore
	// solvencyReporter is invoked once per scanned block so that wallet vs
	// thornode vault accounting drift is reported to thornode promptly.
	// Without this hook, Client.ReportSolvency only fires from the
	// auto-unhalt SolvencyCheckRunner which is gated on the chain being
	// already halted — meaning vaults can drift indefinitely undetected.
	solvencyReporter func(int64) error

	currentBlockHeight *atomic.Int64
	daemonTipHeight    *atomic.Int64
	lastFee            *atomic.Uint64
	globalErrataQueue  chan<- stypes.ErrataBlock
	maxReorgRescan     int64

	asgardMu       sync.RWMutex
	asgardLookup   map[string]common.PubKey
	asgardUpdated  time.Time
	manualAddrByPK map[string]string
	manualPKByAddr map[string]common.PubKey

	observedOutgoingMu     sync.RWMutex
	observedOutgoingByTxID map[string]observedOutgoingTx

	mempoolMu   sync.Mutex
	mempoolSeen map[string]struct{}
}

type observedOutgoingTx struct {
	height int64
	vaults map[string]struct{}
}

func NewScanner(
	logger zerolog.Logger,
	sidecarClient *sidecar.Client,
	bridge thorclient.ThorchainBridge,
	chain common.Chain,
	localNodePubKey common.PubKey,
	maxReorgRescan int64,
) *Scanner {
	return &Scanner{
		logger:                 logger,
		sidecar:                sidecarClient,
		bridge:                 bridge,
		chain:                  chain,
		localNodePubKey:        localNodePubKey,
		currentBlockHeight:     atomic.NewInt64(0),
		daemonTipHeight:        atomic.NewInt64(0),
		lastFee:                atomic.NewUint64(0),
		maxReorgRescan:         maxReorgRescan,
		asgardLookup:           make(map[string]common.PubKey),
		manualAddrByPK:         make(map[string]string),
		manualPKByAddr:         make(map[string]common.PubKey),
		observedOutgoingByTxID: make(map[string]observedOutgoingTx),
		mempoolSeen:            make(map[string]struct{}),
	}
}

// SetSolvencyReporter wires the solvency reporter callback used by the
// per-block scan loop. Pass Client.ReportSolvency from the chain client.
func (s *Scanner) SetSolvencyReporter(fn func(int64) error) {
	s.solvencyReporter = fn
}

func (s *Scanner) SetManualVaultAddress(vaultPubKey common.PubKey, address string) {
	if vaultPubKey.IsEmpty() || strings.TrimSpace(address) == "" {
		return
	}
	derived, err := vaultPubKey.GetAddress(s.chain)
	if err != nil || derived.IsEmpty() {
		s.logger.Debug().Err(err).Str("vault_pubkey", vaultPubKey.String()).Msg("skip manual monero vault mapping: cannot derive address from pubkey")
		return
	}
	if !strings.EqualFold(strings.TrimSpace(derived.String()), strings.TrimSpace(address)) {
		s.logger.Warn().
			Str("vault_pubkey", vaultPubKey.String()).
			Str("provided_address", strings.TrimSpace(address)).
			Str("derived_address", derived.String()).
			Msg("rejecting manual monero vault mapping that does not match derived vault address")
		return
	}
	s.asgardMu.Lock()
	s.manualAddrByPK[vaultPubKey.String()] = derived.String()
	s.manualPKByAddr[derived.String()] = vaultPubKey
	s.asgardMu.Unlock()
}

func (s *Scanner) ManualVaultAddress(vaultPubKey common.PubKey) (string, bool) {
	if vaultPubKey.IsEmpty() {
		return "", false
	}
	s.asgardMu.RLock()
	addr, ok := s.manualAddrByPK[vaultPubKey.String()]
	s.asgardMu.RUnlock()
	return addr, ok
}

func (s *Scanner) CurrentHeight() int64 {
	if s.currentBlockHeight == nil {
		return 0
	}
	return s.currentBlockHeight.Load()
}

func (s *Scanner) SetErrataQueue(globalErrataQueue chan<- stypes.ErrataBlock) {
	s.globalErrataQueue = globalErrataQueue
}

func (s *Scanner) SetReorgStore(store *ReorgStore) {
	s.reorgStore = store
}

func (s *Scanner) SetVaultBootstrap(bootstrap *vaultBootstrap) {
	s.vaultBootstrap = bootstrap
}

// FetchMemPool emits only outbound mempool transfers with explicit sender metadata.
// Inbound mempool attribution is intentionally unsupported for Monero.
func (s *Scanner) FetchMemPool(height int64) (stypes.TxIn, error) {
	txIn := stypes.TxIn{
		Chain:   common.XMRChain,
		MemPool: true,
	}
	if s.sidecar == nil {
		return txIn, nil
	}

	resp, err := s.sidecar.Mempool()
	if err != nil {
		return txIn, fmt.Errorf("fail to get monero mempool transfers: %w", err)
	}
	if len(resp.Outgoing) == 0 {
		return txIn, nil
	}

	candidate := len(resp.Outgoing)
	emitted := 0
	deduped := 0
	skipped := 0

	for _, transfer := range resp.Outgoing {
		to := strings.TrimSpace(transfer.Address)
		if to == "" {
			skipped++
			continue
		}
		_, err = common.NewAddress(to)
		if err != nil {
			s.logger.Debug().Err(err).Str("txid", transfer.TxID).Str("to", to).Msg("skip mempool outgoing transfer with invalid to address")
			skipped++
			continue
		}

		sender, vaultPubKey := s.resolveOutgoingSender(transfer)
		if sender == "" || vaultPubKey.IsEmpty() {
			skipped++
			continue
		}
		item, ok := s.transferToTxInItem(transfer, sender, to, vaultPubKey, strings.TrimSpace(transfer.Note), height)
		if !ok {
			skipped++
			continue
		}
		if !s.tryMarkMempoolSeen(item.Tx) {
			deduped++
			continue
		}

		txIn.TxArray = append(txIn.TxArray, item)
		emitted++
	}

	if candidate > 0 {
		s.logger.Info().
			Int("candidate", candidate).
			Int("emitted", emitted).
			Int("deduped", deduped).
			Int("skipped", skipped).
			Msg("processed monero mempool outgoing transfers")
	}

	return txIn, nil
}

func (s *Scanner) FetchTxs(fetchHeight, chainHeight int64) (stypes.TxIn, error) {
	txIn := stypes.TxIn{Chain: common.XMRChain}
	if s.sidecar == nil {
		return txIn, fmt.Errorf("monero scanner requires sidecar for chain queries")
	}

	block, err := s.sidecar.ChainBlock(fetchHeight)
	if err != nil {
		if errors.Is(err, sidecar.ErrUnavailableBlock) {
			return txIn, btypes.ErrUnavailableBlock
		}
		return txIn, fmt.Errorf("fail to fetch monero block %d: %w", fetchHeight, err)
	}

	// Estimate vault outbound fee from monerod's per-weight fee rate x typical
	// vault tx weight. Avoid block-coinbase averaging when possible because it
	// mixes complex user txs and materially overestimates simple vault outbounds.
	if perWeight, perWeightErr := s.sidecar.FeeRate(); perWeightErr == nil && perWeight > 0 {
		feeAtomic := perWeight * typicalVaultOutboundWeight
		if feeAtomic > 0 {
			s.lastFee.Store(feeAtomic)
		}
	} else if feeAtomic := averageNetworkFeeFromBlock(block.Reward, len(block.TxHashes)); feeAtomic > 0 {
		s.lastFee.Store(feeAtomic)
	}

	if items, reorgErr := s.processReorg(block, chainHeight); reorgErr != nil {
		if errors.Is(reorgErr, btypes.ErrUnavailableBlock) {
			return txIn, btypes.ErrUnavailableBlock
		}
		return txIn, fmt.Errorf("fail to process monero reorg at %d: %w", fetchHeight, reorgErr)
	} else if len(items) > 0 {
		txIn.TxArray = append(txIn.TxArray, items...)
	}

	currentItems, err := s.fetchTransfersByHeight(fetchHeight, chainHeight, block.Hash, block.PreviousHash)
	if err != nil {
		return txIn, err
	}
	if len(currentItems) > 0 {
		txIn.TxArray = append(txIn.TxArray, currentItems...)
	}
	if len(txIn.TxArray) > 0 {
		txIDs := make([]string, 0, len(txIn.TxArray))
		for _, item := range txIn.TxArray {
			if item == nil || strings.TrimSpace(item.Tx) == "" {
				continue
			}
			txIDs = append(txIDs, item.Tx)
		}
		s.clearMempoolSeen(txIDs)
	}

	if err = s.saveBlockMeta(block, fetchHeight, currentItems); err != nil {
		return txIn, err
	}
	s.setCurrentHeight(fetchHeight)

	if s.reorgStore != nil && s.maxReorgRescan > 0 {
		pruneHeight := fetchHeight - s.maxReorgRescan - 1
		if pruneHeight >= 0 {
			if pruneErr := s.reorgStore.Delete(pruneHeight); pruneErr != nil {
				s.logger.Debug().Err(pruneErr).Int64("height", pruneHeight).Msg("fail to prune monero reorg block meta")
			}
		}
	}

	// Report solvency once per scanned block. Without this hook, the chain
	// client's ReportSolvency only fires from the auto-unhalt runner which
	// requires the chain to be already halted — leaving normal operation
	// blind to vault accounting drift. Match the XRP / Solana wiring pattern.
	if s.solvencyReporter != nil {
		if solErr := s.solvencyReporter(fetchHeight); solErr != nil {
			s.logger.Err(solErr).Int64("height", fetchHeight).Msg("fail to report monero solvency")
		}
	}

	return txIn, nil
}

func (s *Scanner) fetchTransfersByHeight(fetchHeight, chainHeight int64, blockHash, parentHash string) ([]*stypes.TxInItem, error) {
	if s.sidecar == nil || s.bridge == nil {
		return nil, nil
	}
	s.pruneObservedOutgoing(fetchHeight)
	if err := s.refreshAsgardLookup(); err != nil {
		s.logger.Err(err).Msg("fail to refresh asgard lookup, retrying next block")
		return nil, btypes.ErrUnavailableBlock
	}

	resp, err := s.sidecar.SyncAt(fetchHeight, chainHeight, blockHash, parentHash, s.maxReorgRescan)
	if err != nil {
		if isFrostScannerHashMismatchErr(err) {
			return nil, btypes.ErrUnavailableBlock
		}
		return nil, fmt.Errorf("fail to get monero transfers at %d: %w", fetchHeight, err)
	}
	if len(resp.Incoming)+len(resp.Outgoing) == 0 {
		return nil, nil
	}

	outgoingVaultsByTxID := s.outgoingVaultsByTxID(resp.Outgoing)
	s.rememberObservedOutgoingVaults(outgoingVaultsByTxID, fetchHeight)
	txItems := make([]*stypes.TxInItem, 0, len(resp.Incoming)+len(resp.Outgoing))
	for _, transfer := range resp.Incoming {
		to := strings.TrimSpace(transfer.Address)
		if to == "" {
			continue
		}
		_, err = common.NewAddress(to)
		if err != nil {
			s.logger.Debug().Err(err).Str("txid", transfer.TxID).Str("to", to).Msg("skip incoming transfer with invalid to address")
			continue
		}

		vaultPubKey, ok := s.resolveIncomingVault(transfer)
		if !ok {
			continue
		}
		if s.isSameVaultOutgoingTx(transfer.TxID, vaultPubKey, outgoingVaultsByTxID) {
			s.logger.Debug().
				Str("txid", transfer.TxID).
				Str("vault_pub_key", vaultPubKey.String()).
				Msg("skip incoming monero change output for known outgoing tx")
			continue
		}

		memoText := strings.TrimSpace(transfer.Note)

		item, ok := s.transferToTxInItem(transfer, unknownSenderAddress(transfer.TxID, to), to, vaultPubKey, memoText, fetchHeight)
		if !ok {
			continue
		}
		txItems = append(txItems, item)
	}

	for _, transfer := range resp.Outgoing {
		to := strings.TrimSpace(transfer.Address)
		if to == "" {
			continue
		}
		_, err = common.NewAddress(to)
		if err != nil {
			s.logger.Debug().Err(err).Str("txid", transfer.TxID).Str("to", to).Msg("skip outgoing transfer with invalid to address")
			continue
		}

		sender, vaultPubKey := s.resolveOutgoingSender(transfer)
		if sender == "" || vaultPubKey.IsEmpty() {
			continue
		}

		memoText := strings.TrimSpace(transfer.Note)

		item, ok := s.transferToTxInItem(transfer, sender, to, vaultPubKey, memoText, fetchHeight)
		if !ok {
			continue
		}
		txItems = append(txItems, item)
	}

	return s.dedupeTxInItemsByTxID(txItems), nil
}

func (s *Scanner) outgoingVaultsByTxID(outgoing []sidecar.Transfer) map[string]map[string]struct{} {
	if len(outgoing) == 0 {
		return nil
	}

	result := make(map[string]map[string]struct{}, len(outgoing))
	for _, transfer := range outgoing {
		txID := normalizeTxID(transfer.TxID)
		if txID == "" {
			continue
		}

		_, vaultPubKey := s.resolveOutgoingSender(transfer)
		if vaultPubKey.IsEmpty() {
			continue
		}

		vaults, ok := result[txID]
		if !ok {
			vaults = make(map[string]struct{})
			result[txID] = vaults
		}
		vaults[vaultPubKey.String()] = struct{}{}
	}
	return result
}

func (s *Scanner) isSameVaultOutgoingTx(
	txID string,
	vaultPubKey common.PubKey,
	outgoingVaultsByTxID map[string]map[string]struct{},
) bool {
	if vaultPubKey.IsEmpty() {
		return false
	}

	normalizedTxID := normalizeTxID(txID)
	if normalizedTxID == "" {
		return false
	}
	if len(outgoingVaultsByTxID) > 0 {
		if vaults, ok := outgoingVaultsByTxID[normalizedTxID]; ok {
			if _, ok := vaults[vaultPubKey.String()]; ok {
				return true
			}
		}
	}
	return s.hasObservedOutgoingVault(normalizedTxID, vaultPubKey)
}

func (s *Scanner) rememberObservedOutgoingVaults(outgoingVaultsByTxID map[string]map[string]struct{}, height int64) {
	if len(outgoingVaultsByTxID) == 0 {
		return
	}

	s.observedOutgoingMu.Lock()
	defer s.observedOutgoingMu.Unlock()

	for txID, vaults := range outgoingVaultsByTxID {
		if txID == "" || len(vaults) == 0 {
			continue
		}

		observed := s.observedOutgoingByTxID[txID]
		if observed.vaults == nil {
			observed.vaults = make(map[string]struct{}, len(vaults))
		}
		if height > observed.height {
			observed.height = height
		}
		for vault := range vaults {
			if strings.TrimSpace(vault) == "" {
				continue
			}
			observed.vaults[vault] = struct{}{}
		}
		s.observedOutgoingByTxID[txID] = observed
	}
}

func (s *Scanner) hasObservedOutgoingVault(txID string, vaultPubKey common.PubKey) bool {
	if txID == "" || vaultPubKey.IsEmpty() {
		return false
	}

	s.observedOutgoingMu.RLock()
	defer s.observedOutgoingMu.RUnlock()

	observed, ok := s.observedOutgoingByTxID[txID]
	if !ok {
		return false
	}
	_, ok = observed.vaults[vaultPubKey.String()]
	return ok
}

func (s *Scanner) pruneObservedOutgoing(currentHeight int64) {
	if currentHeight <= 0 {
		return
	}

	retention := s.maxReorgRescan * 4
	if retention < 1024 {
		retention = 1024
	}
	cutoff := currentHeight - retention
	if cutoff <= 0 {
		return
	}

	s.observedOutgoingMu.Lock()
	defer s.observedOutgoingMu.Unlock()

	for txID, observed := range s.observedOutgoingByTxID {
		if observed.height >= cutoff {
			continue
		}
		delete(s.observedOutgoingByTxID, txID)
	}
}

func (s *Scanner) resolveOutgoingSender(transfer sidecar.Transfer) (string, common.PubKey) {
	txID := strings.TrimSpace(transfer.TxID)
	explicitSender := strings.TrimSpace(transfer.FromAddress)
	explicitVault := strings.TrimSpace(transfer.VaultPubKey)

	if explicitSender == "" {
		s.logger.Debug().Str("txid", txID).Msg("skip outgoing transfer: missing explicit from_address")
		return "", common.EmptyPubKey
	}
	sender, err := common.NewAddress(explicitSender)
	if err != nil {
		s.logger.Debug().Err(err).Str("txid", txID).Str("from", explicitSender).Msg("skip outgoing transfer: invalid explicit from_address")
		return "", common.EmptyPubKey
	}
	if explicitVault == "" {
		s.logger.Debug().Str("txid", txID).Str("from", sender.String()).Msg("skip outgoing transfer: missing explicit vault_pub_key")
		return "", common.EmptyPubKey
	}
	vaultPubKey, err := common.NewPubKey(explicitVault)
	if err != nil || vaultPubKey.IsEmpty() {
		s.logger.Debug().Err(err).Str("txid", txID).Str("vault_pub_key", explicitVault).Msg("skip outgoing transfer: invalid explicit vault_pub_key")
		return "", common.EmptyPubKey
	}
	deriveChain := s.chain
	if deriveChain.IsEmpty() {
		deriveChain = common.XMRChain
	}
	derived, err := vaultPubKey.GetAddress(deriveChain)
	if err != nil || derived.IsEmpty() {
		s.logger.Debug().Err(err).Str("txid", txID).Str("vault_pub_key", vaultPubKey.String()).Msg("skip outgoing transfer: cannot derive sender address from vault_pub_key")
		return "", common.EmptyPubKey
	}
	if !strings.EqualFold(sender.String(), derived.String()) {
		s.logger.Warn().
			Str("txid", txID).
			Str("from_address", sender.String()).
			Str("vault_pub_key", vaultPubKey.String()).
			Str("derived_address", derived.String()).
			Msg("skip outgoing transfer: from_address does not match derived vault address")
		return "", common.EmptyPubKey
	}

	return sender.String(), vaultPubKey
}

func (s *Scanner) GetHeight() (int64, error) {
	if s.sidecar == nil {
		return 0, fmt.Errorf("monero scanner requires sidecar for chain queries")
	}

	height, err := s.sidecar.ChainHeight()
	if err != nil {
		return 0, err
	}
	s.setDaemonTipHeight(height)
	return height, nil
}

func (s *Scanner) setCurrentHeight(height int64) {
	if s.currentBlockHeight != nil {
		s.currentBlockHeight.Store(height)
	}
}

func (s *Scanner) setDaemonTipHeight(height int64) {
	if s.daemonTipHeight != nil {
		s.daemonTipHeight.Store(height)
	}
}

const defaultXMRFeeTotalThorchain = 20000 // ~0.0002 XMR typical mainnet fee

func totalFeeAtomicToThorchain(feeAtomic uint64) uint64 {
	return feeAtomic / moneroAtomicToThorchain
}

func feeRateFromTotalAtomic(totalAtomic uint64) uint64 {
	if totalAtomic == 0 {
		return 0
	}
	return (totalAtomic + typicalVaultOutboundWeight - 1) / typicalVaultOutboundWeight
}

func (s *Scanner) GetNetworkFee() (transactionSize, transactionFeeRate uint64) {
	totalAtomic := s.lastFee.Load()
	if totalAtomic == 0 {
		totalAtomic = defaultXMRFeeTotalThorchain * moneroAtomicToThorchain
	}
	return typicalVaultOutboundWeight, feeRateFromTotalAtomic(totalAtomic)
}

func averageNetworkFeeFromBlock(reward uint64, txCount int) uint64 {
	if txCount <= 0 || reward <= defaultCoinbaseValue {
		return 0
	}

	totalFeesAtomic := reward - defaultCoinbaseValue
	averageFeeAtomic := totalFeesAtomic / uint64(txCount)
	return averageFeeAtomic
}

func (s *Scanner) processReorg(block sidecar.ChainBlock, chainHeight int64) ([]*stypes.TxInItem, error) {
	if s.reorgStore == nil || block.Hash == "" || block.PreviousHash == "" {
		return nil, nil
	}
	previousHeight := block.Height - 1
	if block.Height <= 0 {
		previousHeight = s.CurrentHeight() - 1
	}
	if previousHeight < 0 {
		return nil, nil
	}

	prevBlockMeta, err := s.reorgStore.Get(previousHeight)
	if err != nil {
		return nil, err
	}
	if prevBlockMeta == nil || strings.EqualFold(prevBlockMeta.BlockHash, block.PreviousHash) {
		return nil, nil
	}

	s.logger.Warn().
		Int64("height", previousHeight+1).
		Int64("previous_height", previousHeight).
		Str("expected_previous_hash", prevBlockMeta.BlockHash).
		Str("actual_previous_hash", block.PreviousHash).
		Msg("monero reorg detected")

	reorgHeights, err := s.reConfirmTx(previousHeight + 1)
	if err != nil {
		return nil, err
	}
	if len(reorgHeights) == 0 {
		return nil, nil
	}

	reScannedItems := make([]*stypes.TxInItem, 0)
	for _, height := range reorgHeights {
		reorgBlock, blockErr := s.sidecar.ChainBlock(height)
		if blockErr != nil {
			if errors.Is(blockErr, sidecar.ErrUnavailableBlock) {
				return nil, btypes.ErrUnavailableBlock
			}
			s.logger.Error().Err(blockErr).Int64("height", height).Msg("fail to fetch monero block during reorg rescan")
			continue
		}

		items, transferErr := s.fetchTransfersByHeight(height, chainHeight, reorgBlock.Hash, reorgBlock.PreviousHash)
		if transferErr != nil {
			if errors.Is(transferErr, btypes.ErrUnavailableBlock) {
				return nil, btypes.ErrUnavailableBlock
			}
			s.logger.Error().Err(transferErr).Int64("height", height).Msg("fail to fetch monero transfers during reorg rescan")
			continue
		}

		reScannedItems = append(reScannedItems, items...)
		if saveErr := s.saveBlockMeta(reorgBlock, height, items); saveErr != nil {
			return nil, saveErr
		}
	}

	return reScannedItems, nil
}

func (s *Scanner) reConfirmTx(height int64) ([]int64, error) {
	if s.reorgStore == nil {
		return nil, nil
	}

	rescanHeights := make([]int64, 0)
	earliestHeight := height - s.maxReorgRescan
	if earliestHeight < 0 {
		earliestHeight = 0
	}

	for i := height - 1; i >= earliestHeight; i-- {
		blockMeta, err := s.reorgStore.Get(i)
		if err != nil {
			return nil, err
		}
		if blockMeta == nil {
			continue
		}

		block, err := s.sidecar.ChainBlock(i)
		if err != nil {
			return nil, fmt.Errorf("fail to fetch monero block %d during reorg check: %w", i, err)
		}
		if strings.EqualFold(blockMeta.BlockHash, block.Hash) {
			break
		}

		s.logger.Info().
			Int64("height", i).
			Str("recorded_hash", blockMeta.BlockHash).
			Str("canonical_hash", block.Hash).
			Msg("reconfirming monero block")

		errataTxs := make([]stypes.ErrataTx, 0)
		for _, tx := range blockMeta.CustomerTransactions {
			txExists, confirmErr := s.sidecar.TxExists(tx)
			if confirmErr != nil {
				return nil, fmt.Errorf("unable to confirm monero tx %s existence during reorg: %w", tx, confirmErr)
			}
			if txExists {
				continue
			}

			errataTxs = append(errataTxs, stypes.ErrataTx{
				TxID:  common.TxID(tx),
				Chain: s.chain,
			})
		}
		if len(errataTxs) > 0 && s.globalErrataQueue != nil {
			s.globalErrataQueue <- stypes.ErrataBlock{
				Height: i,
				Txs:    errataTxs,
			}
		}

		rescanHeights = append(rescanHeights, i)
		blockMeta.BlockHash = block.Hash
		blockMeta.PreviousHash = block.PreviousHash
		blockMeta.CustomerTransactions = nil
		if err = s.reorgStore.Set(blockMeta); err != nil {
			return nil, err
		}
	}

	return rescanHeights, nil
}

func (s *Scanner) saveBlockMeta(block sidecar.ChainBlock, height int64, items []*stypes.TxInItem) error {
	if s.reorgStore == nil {
		return nil
	}
	if block.Hash == "" {
		s.logger.Debug().Int64("height", height).Msg("skip monero reorg block meta save: missing block hash")
		return nil
	}
	txIDs := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item == nil || item.Tx == "" {
			continue
		}
		if _, ok := seen[item.Tx]; ok {
			continue
		}
		seen[item.Tx] = struct{}{}
		txIDs = append(txIDs, item.Tx)
	}

	return s.reorgStore.Set(&reorgBlockMeta{
		Height:               height,
		BlockHash:            block.Hash,
		PreviousHash:         block.PreviousHash,
		CustomerTransactions: txIDs,
	})
}

func (s *Scanner) refreshAsgardLookup() error {
	s.asgardMu.RLock()
	if time.Since(s.asgardUpdated) < constants.ThorchainBlockTime && len(s.asgardLookup) > 0 {
		s.asgardMu.RUnlock()
		return nil
	}
	s.asgardMu.RUnlock()

	vaults, err := s.bridge.GetAsgards()
	if err != nil {
		return fmt.Errorf("fail to get asgards: %w", err)
	}

	lookup := make(map[string]common.PubKey)
	seenVaults := make(map[string]struct{})
	for _, vault := range vaults {
		if addErr := s.addFrostVaultToLookup(lookup, seenVaults, vault.PubKeyFrost, vault.VaultCreationHeightXmr); addErr != nil {
			s.logger.Debug().Err(addErr).Str("vault_pubkey", vault.PubKey.String()).Msg("skip xmr vault during asgard lookup refresh")
			continue
		}
	}

	pubKeys, err := s.bridge.GetPubKeys()
	if err != nil {
		return fmt.Errorf("fail to get vault pubkeys: %w", err)
	}
	for _, pair := range pubKeys {
		if pair.Algo != common.SigningAlgoSecp256k1 || pair.PubKey.IsEmpty() {
			continue
		}
		vault, vaultErr := s.bridge.GetVault(pair.PubKey.String())
		if vaultErr != nil {
			s.logger.Debug().Err(vaultErr).Str("vault_pubkey", pair.PubKey.String()).Msg("skip xmr pubkey during asgard lookup refresh")
			continue
		}
		if err := s.addFrostVaultToLookup(lookup, seenVaults, vault.PubKeyFrost, vault.VaultCreationHeightXmr); err != nil {
			s.logger.Debug().Err(err).Str("vault_pubkey", vault.PubKey.String()).Msg("skip xmr vault during pubkey lookup refresh")
			continue
		}
	}

	s.asgardMu.Lock()
	s.asgardLookup = lookup
	s.asgardUpdated = time.Now()
	s.asgardMu.Unlock()

	return nil
}

func (s *Scanner) addFrostVaultToLookup(
	lookup map[string]common.PubKey,
	seenVaults map[string]struct{},
	observedVault common.PubKey,
	creationHeight int64,
) error {
	if observedVault.IsEmpty() {
		return nil
	}
	key := observedVault.String()
	if _, ok := seenVaults[key]; ok {
		return nil
	}
	seenVaults[key] = struct{}{}

	derived, addrErr := ensureVaultRegistered(
		s.logger,
		s.sidecar,
		s.chain,
		observedVault,
		s.vaultBootstrap,
		"bifrost_scanner",
		creationHeight,
	)
	if addrErr != nil {
		return fmt.Errorf("fail to register vault %s: %w", observedVault.String(), addrErr)
	}
	if derived.IsEmpty() {
		s.logger.Debug().Str("vault_pubkey", observedVault.String()).Msg("skip asgard vault with empty derived address")
		return nil
	}
	lookup[derived.String()] = observedVault
	return nil
}

func (s *Scanner) vaultPubKeyForAddress(addr string) (common.PubKey, bool) {
	s.asgardMu.RLock()
	defer s.asgardMu.RUnlock()
	pk, ok := s.asgardLookup[addr]
	return pk, ok
}

func (s *Scanner) resolveIncomingVault(transfer sidecar.Transfer) (common.PubKey, bool) {
	to := strings.TrimSpace(transfer.Address)
	if to == "" {
		return common.EmptyPubKey, false
	}

	mappedVault, mappedOK := s.vaultPubKeyForAddress(to)
	explicitVault := strings.TrimSpace(transfer.VaultPubKey)
	if explicitVault == "" {
		return mappedVault, mappedOK
	}

	vaultPubKey, err := common.NewPubKey(explicitVault)
	if err != nil || vaultPubKey.IsEmpty() {
		s.logger.Debug().Err(err).Str("txid", transfer.TxID).Str("vault_pub_key", explicitVault).Msg("skip incoming transfer: invalid explicit vault_pub_key")
		return common.EmptyPubKey, false
	}

	deriveChain := s.chain
	if deriveChain.IsEmpty() {
		deriveChain = common.XMRChain
	}
	derived, err := vaultPubKey.GetAddress(deriveChain)
	if err != nil || derived.IsEmpty() {
		s.logger.Debug().Err(err).Str("txid", transfer.TxID).Str("vault_pub_key", vaultPubKey.String()).Msg("skip incoming transfer: cannot derive explicit vault address")
		return common.EmptyPubKey, false
	}
	if !strings.EqualFold(derived.String(), to) {
		s.logger.Debug().
			Str("txid", transfer.TxID).
			Str("vault_pub_key", vaultPubKey.String()).
			Str("derived_address", derived.String()).
			Str("to", to).
			Msg("skip incoming transfer: explicit vault_pub_key address does not match transfer address")
		return common.EmptyPubKey, false
	}

	if !mappedOK {
		s.logger.Debug().
			Str("txid", transfer.TxID).
			Str("vault_pub_key", vaultPubKey.String()).
			Str("to", to).
			Msg("skip incoming transfer: explicit vault_pub_key is not a current asgard vault")
		return common.EmptyPubKey, false
	}
	if !mappedVault.Equals(vaultPubKey) {
		s.logger.Debug().
			Str("txid", transfer.TxID).
			Str("explicit_vault_pub_key", vaultPubKey.String()).
			Str("mapped_vault_pub_key", mappedVault.String()).
			Str("to", to).
			Msg("skip incoming transfer: explicit vault_pub_key does not match current asgard address attribution")
		return common.EmptyPubKey, false
	}

	return vaultPubKey, true
}

func (s *Scanner) transferToTxInItem(
	transfer sidecar.Transfer,
	sender string,
	to string,
	vaultPubKey common.PubKey,
	memo string,
	fallbackHeight int64,
) (*stypes.TxInItem, bool) {
	if transfer.TxID == "" || sender == "" || to == "" || vaultPubKey.IsEmpty() {
		return nil, false
	}

	amount := cosmos.NewUint(transfer.Amount / moneroAtomicToThorchain)
	if amount.IsZero() {
		return nil, false
	}

	fee := transfer.Fee / moneroAtomicToThorchain
	if fee == 0 {
		fee = totalFeeAtomicToThorchain(s.lastFee.Load())
	}
	if fee == 0 {
		fee = defaultXMRFeeTotalThorchain
	}

	height := int64(transfer.Height)
	if height <= 0 {
		height = fallbackHeight
	}

	return stypes.NewTxInItem(
		height,
		transfer.TxID,
		memo,
		sender,
		to,
		common.NewCoins(common.NewCoin(common.XMRAsset, amount)),
		common.Gas{common.NewCoin(common.XMRAsset, cosmos.NewUint(fee))},
		vaultPubKey,
		"",
		"",
		nil,
	), true
}

func (s *Scanner) dedupeTxInItemsByTxID(items []*stypes.TxInItem) []*stypes.TxInItem {
	if len(items) < 2 {
		return items
	}

	merged := make(map[string]*stypes.TxInItem, len(items))
	order := make([]string, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		observationKey := txInItemObservationKey(item)
		if observationKey == "" {
			continue
		}
		existing, ok := merged[observationKey]
		if !ok {
			merged[observationKey] = item
			order = append(order, observationKey)
			continue
		}

		if existing.BlockHeight != item.BlockHeight || existing.Memo != item.Memo {
			s.logger.Debug().
				Str("txid", item.Tx).
				Str("existing_sender", existing.Sender).
				Str("existing_to", existing.To).
				Str("duplicate_sender", item.Sender).
				Str("duplicate_to", item.To).
				Msg("merging duplicate monero transfer with same txid")
		}

		existing.Coins = existing.Coins.Add(item.Coins...)
		switch {
		case len(existing.Gas) == 0:
			existing.Gas = item.Gas
		case len(item.Gas) > 0 && item.Gas[0].Amount.GT(existing.Gas[0].Amount):
			existing.Gas = item.Gas
		}
		if existing.Memo == "" && item.Memo != "" {
			existing.Memo = item.Memo
		}
	}

	deduped := make([]*stypes.TxInItem, 0, len(order))
	for _, observationKey := range order {
		deduped = append(deduped, merged[observationKey])
	}
	return deduped
}

func txInItemObservationKey(item *stypes.TxInItem) string {
	if item == nil {
		return ""
	}

	txID := normalizeTxID(item.Tx)
	if txID == "" {
		return ""
	}

	return strings.Join([]string{
		txID,
		strings.TrimSpace(item.Sender),
		strings.TrimSpace(item.To),
		item.ObservedVaultPubKey.String(),
	}, "|")
}

func unknownSenderAddress(txID, to string) string {
	return common.XMRUnknownSenderAddress(txID, to)
}

func (s *Scanner) tryMarkMempoolSeen(txID string) bool {
	normalized := normalizeTxID(txID)
	if normalized == "" {
		return false
	}
	s.mempoolMu.Lock()
	defer s.mempoolMu.Unlock()
	if _, ok := s.mempoolSeen[normalized]; ok {
		return false
	}
	s.mempoolSeen[normalized] = struct{}{}
	return true
}

func (s *Scanner) clearMempoolSeen(txIDs []string) {
	if len(txIDs) == 0 {
		return
	}
	s.mempoolMu.Lock()
	defer s.mempoolMu.Unlock()
	for _, txID := range txIDs {
		normalized := normalizeTxID(txID)
		if normalized == "" {
			continue
		}
		delete(s.mempoolSeen, normalized)
	}
}

func normalizeTxID(txID string) string {
	return strings.ToLower(strings.TrimSpace(txID))
}

func IsUnavailableBlockErr(err error) bool {
	return errors.Is(err, sidecar.ErrUnavailableBlock)
}

func isFrostScannerHashMismatchErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "scanner sync hash mismatch")
}
