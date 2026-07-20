package monero

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
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
	// typicalVaultOutboundWeight is the conservative vault/FROST outbound tx
	// weight in Monero weight units used for THORChain network fee reporting.
	// Keep it over-reserved for now so migration txs that spend many UTXOs are
	// less likely to be blocked by fee spikes before the full MaxGas feedback and
	// reschedule path lands.
	typicalVaultOutboundWeight = 10000
)

// ErrReorgSupersessionUnsafe is a hard operator stop: the durable pending
// intent was not in a shape that can be safely replaced while preserving all
// orphan cleanup. The journal is deliberately left untouched for inspection.
var ErrReorgSupersessionUnsafe = errors.New("xmr pending reorg cannot be safely superseded")

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

	currentBlockHeight   *atomic.Int64
	daemonTipHeight      *atomic.Int64
	lastFee              *atomic.Uint64
	globalErrataQueue    chan<- stypes.ErrataBlock
	errataAckRequired    bool
	maxReorgRescan       int64
	scanMu               sync.Mutex
	reorgDeliveryPending bool

	// errataDeliveryTimeout bounds both the send to the observer and the wait
	// for its acknowledgement. Zero means the default; tests shorten it.
	errataDeliveryTimeout time.Duration

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
	s.errataAckRequired = false
}

// SetErrataQueueWithAck requires the observer to durably persist each errata
// record, complete its local on-deck cleanup, and accept the local errata
// attestation before reorg replay is considered delivered.
func (s *Scanner) SetErrataQueueWithAck(globalErrataQueue chan<- stypes.ErrataBlock) {
	s.globalErrataQueue = globalErrataQueue
	s.errataAckRequired = true
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
		_, err := common.NewAddress(to)
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
	// Avoid remote reads while Observer still owns the previous delivery. Check
	// again after taking the continuity lock below to close the race with a
	// delivery becoming pending during ChainBlock or FeeRate.
	s.scanMu.Lock()
	deliveryPending := s.reorgDeliveryPending
	s.scanMu.Unlock()
	if deliveryPending {
		return txIn, btypes.ErrUnavailableBlock
	}
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

	// ChainBlock and FeeRate are independent reads. Serialize only registration,
	// continuity, reorg, sync, and journal mutations. Keeping FetchTxs free of an
	// enclosing scanMu lock prevents this narrower lock from becoming recursive
	// when companion scanner changes are merged.
	s.scanMu.Lock()
	locked := true
	defer func() {
		if locked {
			s.scanMu.Unlock()
		}
	}()
	if s.reorgDeliveryPending {
		return txIn, btypes.ErrUnavailableBlock
	}

	// Registration may rewind the sidecar to an older vault birthday. Refresh
	// vaults and non-mutatingly preflight the authenticated sidecar cursor before
	// touching the reorg journal, emitting errata, or advancing local metadata.
	// Syncing the current height here would be wrong: processReorg may first need
	// to drive older canonical heights through SyncAt in order.
	if s.bridge != nil {
		if err = s.refreshAsgardLookup(fetchHeight); err != nil {
			s.logger.Err(err).Msg("fail to refresh asgard lookup, retrying next block")
			return txIn, btypes.ErrUnavailableBlock
		}
	}
	resumeDiscoveredReorg := false
	if s.reorgStore != nil {
		journal, journalErr := s.reorgStore.GetJournal()
		if journalErr != nil {
			return txIn, fmt.Errorf("load monero reorg intent before continuity preflight: %w", journalErr)
		}
		resumeDiscoveredReorg = journal != nil && journal.Phase == reorgPhaseDiscovered
	}
	if !resumeDiscoveredReorg {
		if err = s.sidecar.PreflightScannerHeight(fetchHeight, block.Hash, block.PreviousHash); err != nil {
			return txIn, s.mapScannerContinuityError(fetchHeight, err)
		}
	}

	if items, reorgErr := s.processReorg(block, chainHeight); reorgErr != nil {
		if errors.Is(reorgErr, btypes.ErrUnavailableBlock) {
			return txIn, btypes.ErrUnavailableBlock
		}
		if errors.Is(reorgErr, ErrReorgSupersessionUnsafe) {
			return txIn, &btypes.ScannerRecoveryRequiredError{
				CurrentHeight: fetchHeight,
				NextHeight:    fetchHeight,
				Reason:        reorgErr.Error() + "; journal retained for inspection, do not reset the cursor blindly",
			}
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
	if err = s.attachReorgCheckpoint(&txIn); err != nil {
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

	// Solvency reporting may register vaults through Client. Run it after the
	// scan critical section so registration can take scanMu without deadlocking.
	s.scanMu.Unlock()
	locked = false
	if s.solvencyReporter != nil {
		if solErr := s.solvencyReporter(fetchHeight); solErr != nil {
			s.logger.Err(solErr).Int64("height", fetchHeight).Msg("fail to report monero solvency")
		}
	}

	return txIn, nil
}

func (s *Scanner) attachReorgCheckpoint(txIn *stypes.TxIn) error {
	if s.reorgStore == nil || txIn == nil {
		return nil
	}
	journal, err := s.reorgStore.GetJournal()
	if err != nil {
		return err
	}
	if journal == nil || journal.Phase != reorgPhaseDelivered {
		return nil
	}
	if len(txIn.TxArray) > 0 {
		txIn.Persisted = make(chan error, 1)
	}
	s.reorgDeliveryPending = true
	txIn.AbortPersistence = func() {
		s.scanMu.Lock()
		s.reorgDeliveryPending = false
		s.scanMu.Unlock()
	}
	txIn.FinalizePersistence = func() error {
		s.scanMu.Lock()
		defer s.scanMu.Unlock()
		// The journal is synced and remains retryable if finalization fails.
		s.reorgDeliveryPending = false
		current, loadErr := s.reorgStore.GetJournal()
		if loadErr != nil {
			return loadErr
		}
		if current == nil {
			return nil
		}
		return s.reorgStore.CommitDelivery(current)
	}
	return nil
}

func (s *Scanner) fetchTransfersByHeight(fetchHeight, chainHeight int64, blockHash, parentHash string) ([]*stypes.TxInItem, error) {
	if s.sidecar == nil {
		return nil, fmt.Errorf("sidecar is unavailable")
	}
	s.pruneObservedOutgoing(fetchHeight)

	resp, err := s.sidecar.SyncAt(fetchHeight, chainHeight, blockHash, parentHash, s.maxReorgRescan)
	if err != nil {
		return nil, s.mapScannerContinuityError(fetchHeight, err)
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
		if _, addressErr := common.NewAddress(to); addressErr != nil {
			s.logger.Debug().Err(addressErr).Str("txid", transfer.TxID).Str("to", to).Msg("skip incoming transfer with invalid to address")
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

func (s *Scanner) mapScannerContinuityError(fetchHeight int64, err error) error {
	var rewind *sidecar.ScannerRewindRequiredError
	if errors.As(err, &rewind) {
		distance := fetchHeight - rewind.NextHeight
		if rewind.NextHeight <= 0 || rewind.NextHeight >= fetchHeight || distance > btypes.MaxCursorRewindBlocks {
			return &btypes.ScannerRecoveryRequiredError{
				CurrentHeight: fetchHeight,
				NextHeight:    rewind.NextHeight,
				Reason: fmt.Sprintf(
					"authenticated sidecar cursor discontinuity is outside the automatic 1..%d block recovery window (reset epoch %d); restore compatible sidecar state or deliberately reset the Bifrost cursor",
					btypes.MaxCursorRewindBlocks,
					rewind.ResetEpoch,
				),
			}
		}
		return &btypes.RewindRequiredError{NextHeight: rewind.NextHeight}
	}
	// The sidecar has durably scanned PAST this height. Unlike a rewind, no
	// amount of retrying repairs it, and rewinding forward to meet the sidecar
	// would skip the intervening blocks without observing them. Map it to the
	// terminal scanner error so BlockScanner stops for the operator instead of
	// re-fetching this height forever.
	var recovery *sidecar.ScannerRecoveryRequiredError
	if errors.As(err, &recovery) {
		return &btypes.ScannerRecoveryRequiredError{
			CurrentHeight: fetchHeight,
			NextHeight:    recovery.NextHeight,
			Reason: fmt.Sprintf(
				"authenticated sidecar cursor is ahead of the requested height (reset epoch %d); "+
					"the sidecar has already scanned past this block, so retrying cannot recover: "+
					"restore compatible sidecar state or deliberately reset the Bifrost cursor",
				recovery.ResetEpoch,
			),
		}
	}
	if isFrostScannerHashMismatchErr(err) {
		return btypes.ErrUnavailableBlock
	}
	return fmt.Errorf("fail to get monero transfers at %d: %w", fetchHeight, err)
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
	journal, err := s.reorgStore.GetJournal()
	if err != nil {
		return nil, err
	}
	if journal != nil {
		if journal.Phase == reorgPhaseDiscovered {
			journal, err = s.refreshDiscoveredReorgJournal(journal, block.Height)
			if err != nil {
				return nil, err
			}
			if journal == nil {
				return nil, nil
			}
			return s.replayReorgJournal(journal, chainHeight, block)
		}
		matches, matchErr := s.reorgJournalMatchesCanonical(journal, block)
		if matchErr != nil {
			return nil, matchErr
		}
		if !matches {
			// ReplayedMetas/ReplayedItems may be the only durable description of
			// an intermediate fork whose observations reached Observer before the
			// delivery checkpoint committed. Replacing this journal would make a
			// later fork's errata impossible to construct. Stop with the journal
			// intact; automatic supersession needs a durable observation-ACK phase
			// and must also include the current fetch-height metadata.
			return nil, fmt.Errorf(
				"%w: %s journal anchored at %d no longer matches canonical before delivery commit",
				ErrReorgSupersessionUnsafe,
				journal.Phase,
				journal.AnchorHeight,
			)
		}
		return s.resumeReorgDelivery(journal)
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

	journal, err = s.discoverReorg(previousHeight + 1)
	if err != nil {
		return nil, err
	}
	if journal == nil {
		return nil, nil
	}
	return s.replayReorgJournal(journal, chainHeight, block)
}

// refreshDiscoveredReorgJournal rebuilds a persisted intent against the
// current canonical chain before replay. A crash may leave the journal in the
// discovered phase while the daemon advances or reorganizes again.
func (s *Scanner) refreshDiscoveredReorgJournal(journal *reorgJournal, nextHeight int64) (*reorgJournal, error) {
	if journal == nil || journal.Phase != reorgPhaseDiscovered {
		return nil, fmt.Errorf("invalid discovered xmr reorg journal")
	}
	refreshed, err := s.buildReorgJournal(nextHeight)
	if err != nil {
		return nil, err
	}
	if refreshed == nil {
		if err = s.reorgStore.DeleteJournal(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if err = s.reorgStore.SetJournal(refreshed); err != nil {
		return nil, err
	}
	return refreshed, nil
}

func (s *Scanner) replayReorgJournal(journal *reorgJournal, chainHeight int64, nextBlock sidecar.ChainBlock) ([]*stypes.TxInItem, error) {
	if err := validateReorgReplayRange(journal, nextBlock.Height); err != nil {
		return nil, err
	}

	// The durable Discovered record is an intent only. Rewind the authenticated
	// sidecar cursor before scanning, changing phase, delivering errata, or
	// replacing metadata. Repeating this on every resume makes partial replay
	// and the crash window after intent persistence safe.
	if err := s.prepareSidecarReorgReplay(journal); err != nil {
		return nil, fmt.Errorf("prepare sidecar for monero reorg replay: %w", err)
	}

	reScannedItems := make([]*stypes.TxInItem, 0)
	metas := make([]*reorgBlockMeta, 0, len(journal.Heights))
	expectedHeight := journal.AnchorHeight + 1
	expectedPreviousHash := journal.AnchorHash
	if expectedHeight < 0 || strings.TrimSpace(expectedPreviousHash) == "" {
		return nil, fmt.Errorf("invalid xmr reorg journal anchor")
	}
	for _, height := range journal.Heights {
		if height != expectedHeight {
			return nil, fmt.Errorf("non-contiguous xmr reorg replay height %d, expected %d", height, expectedHeight)
		}
		reorgBlock, blockErr := s.sidecar.ChainBlock(height)
		if blockErr != nil {
			if errors.Is(blockErr, sidecar.ErrUnavailableBlock) {
				return nil, btypes.ErrUnavailableBlock
			}
			return nil, fmt.Errorf("fail to fetch monero block %d during reorg replay: %w", height, blockErr)
		}
		if reorgBlock.Height != height {
			return nil, &btypes.ScannerRecoveryRequiredError{
				CurrentHeight: nextBlock.Height,
				NextHeight:    journal.AnchorHeight + 1,
				Reason: fmt.Sprintf(
					"monero reorg replay requested height %d but the sidecar returned height %d",
					height, reorgBlock.Height,
				),
			}
		}
		if !strings.EqualFold(reorgBlock.PreviousHash, expectedPreviousHash) {
			s.logger.Warn().
				Int64("height", height).
				Str("expected_previous_hash", expectedPreviousHash).
				Str("actual_previous_hash", reorgBlock.PreviousHash).
				Msg("monero chain changed during reorg replay; rediscovering")
			return nil, btypes.ErrUnavailableBlock
		}

		items, transferErr := s.fetchTransfersByHeight(height, chainHeight, reorgBlock.Hash, reorgBlock.PreviousHash)
		if transferErr != nil {
			if errors.Is(transferErr, btypes.ErrUnavailableBlock) {
				return nil, btypes.ErrUnavailableBlock
			}
			return nil, fmt.Errorf("fail to fetch monero transfers at %d during reorg replay: %w", height, transferErr)
		}

		reScannedItems = append(reScannedItems, items...)
		metas = append(metas, blockMetaFromItems(reorgBlock, height, items))
		expectedHeight++
		expectedPreviousHash = reorgBlock.Hash
	}
	if nextBlock.Height != expectedHeight {
		return nil, &btypes.ScannerRecoveryRequiredError{
			CurrentHeight: nextBlock.Height,
			NextHeight:    journal.AnchorHeight + 1,
			Reason: fmt.Sprintf(
				"monero reorg replay ended at height %d but must join height %d",
				expectedHeight, nextBlock.Height,
			),
		}
	}
	if !strings.EqualFold(nextBlock.PreviousHash, expectedPreviousHash) {
		s.logger.Warn().
			Int64("height", nextBlock.Height).
			Str("expected_previous_hash", expectedPreviousHash).
			Str("actual_previous_hash", nextBlock.PreviousHash).
			Msg("monero chain changed at the reorg replay join; rediscovering")
		return nil, btypes.ErrUnavailableBlock
	}
	if err := s.reorgStore.CommitReplay(journal, metas, reScannedItems); err != nil {
		return nil, err
	}
	return s.resumeReorgDelivery(journal)
}

// validateReorgReplayRange separates a malformed durable replay intent from a
// live chain change. The former cannot repair itself by retrying the same scan
// height and must stop BlockScanner with an operator-recovery error.
func validateReorgReplayRange(journal *reorgJournal, nextBlockHeight int64) error {
	if journal == nil || journal.Phase != reorgPhaseDiscovered || len(journal.Heights) == 0 {
		return nil // prepareSidecarReorgReplay reports the general journal error.
	}

	targetNextHeight := journal.AnchorHeight + 1
	expectedHeight := targetNextHeight
	for _, height := range journal.Heights {
		if height != expectedHeight {
			return &btypes.ScannerRecoveryRequiredError{
				CurrentHeight: nextBlockHeight,
				NextHeight:    targetNextHeight,
				Reason: fmt.Sprintf(
					"stored monero reorg journal is non-contiguous at height %d (expected %d)",
					height, expectedHeight,
				),
			}
		}
		expectedHeight++
	}
	if expectedHeight != nextBlockHeight {
		return &btypes.ScannerRecoveryRequiredError{
			CurrentHeight: nextBlockHeight,
			NextHeight:    targetNextHeight,
			Reason: fmt.Sprintf(
				"stored monero reorg journal ends at height %d and cannot join scan height %d",
				expectedHeight-1, nextBlockHeight,
			),
		}
	}
	return nil
}

func (s *Scanner) prepareSidecarReorgReplay(journal *reorgJournal) error {
	if s.sidecar == nil {
		return fmt.Errorf("sidecar is unavailable")
	}
	if journal == nil || journal.Phase != reorgPhaseDiscovered || len(journal.Heights) == 0 {
		return fmt.Errorf("invalid discovered reorg intent")
	}
	if journal.AnchorHeight < 0 || strings.TrimSpace(journal.AnchorHash) == "" {
		return fmt.Errorf("invalid reorg anchor")
	}
	targetNextHeight := journal.AnchorHeight + 1
	if targetNextHeight <= 0 || journal.Heights[0] != targetNextHeight {
		return fmt.Errorf("reorg intent does not begin immediately after its anchor")
	}
	if s.maxReorgRescan <= 0 || int64(len(journal.Heights)) > s.maxReorgRescan {
		return fmt.Errorf("reorg intent exceeds configured replay window")
	}
	for i, height := range journal.Heights {
		if height != targetNextHeight+int64(i) {
			return fmt.Errorf("reorg intent has non-contiguous height %d", height)
		}
	}
	_, err := s.sidecar.RewindScannerForReorg(targetNextHeight, journal.AnchorHeight, journal.AnchorHash)
	return err
}

func (s *Scanner) discoverReorg(height int64) (*reorgJournal, error) {
	journal, err := s.buildReorgJournal(height)
	if err != nil || journal == nil {
		return journal, err
	}
	if err = s.reorgStore.SetJournal(journal); err != nil {
		return nil, err
	}
	return journal, nil
}

func (s *Scanner) buildReorgJournal(height int64) (*reorgJournal, error) {
	if s.reorgStore == nil {
		return nil, nil
	}

	rescanHeights := make([]int64, 0)
	errataRecords := make([]reorgErrataRecord, 0)
	foundCommonAncestor := false
	anchorHeight := int64(-1)
	anchorHash := ""
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
			if len(rescanHeights) > 0 {
				return nil, fmt.Errorf("missing xmr reorg metadata at height %d before common ancestor", i)
			}
			continue
		}

		block, err := s.sidecar.ChainBlock(i)
		if err != nil {
			return nil, fmt.Errorf("fail to fetch monero block %d during reorg check: %w", i, err)
		}
		if strings.EqualFold(blockMeta.BlockHash, block.Hash) {
			foundCommonAncestor = true
			anchorHeight = i
			anchorHash = block.Hash
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
		if len(errataTxs) > 0 {
			txIDs := make([]string, 0, len(errataTxs))
			for _, errataTx := range errataTxs {
				txIDs = append(txIDs, errataTx.TxID.String())
			}
			errataRecords = append(errataRecords, reorgErrataRecord{Height: i, TxIDs: txIDs})
		}

		rescanHeights = append(rescanHeights, i)
	}
	if len(rescanHeights) == 0 {
		return nil, nil
	}
	if !foundCommonAncestor {
		return nil, fmt.Errorf("xmr reorg exceeds configured replay window: no common ancestor found")
	}
	sort.Slice(rescanHeights, func(i, j int) bool { return rescanHeights[i] < rescanHeights[j] })
	sort.Slice(errataRecords, func(i, j int) bool { return errataRecords[i].Height < errataRecords[j].Height })

	// replayReorgJournal requires Heights to be exactly the contiguous range
	// (anchorHeight, height). A metadata gap ABOVE the anchor is skipped by the
	// walk above when it is seen before any mismatch, which silently truncates
	// the range: every subsequent replay then fails the contiguity check in
	// replayReorgJournal and returns ErrUnavailableBlock, stalling the scanner
	// on this height indefinitely and silently. Fail closed instead, so the
	// gap is reported rather than retried forever.
	if wantLen := height - 1 - anchorHeight; int64(len(rescanHeights)) != wantLen ||
		rescanHeights[0] != anchorHeight+1 ||
		rescanHeights[len(rescanHeights)-1] != height-1 {
		return nil, fmt.Errorf(
			"xmr reorg replay range is not contiguous: missing reorg metadata between "+
				"common ancestor %d and height %d, cannot replay safely",
			anchorHeight, height,
		)
	}

	journal := &reorgJournal{
		Version:      2,
		Phase:        reorgPhaseDiscovered,
		AnchorHeight: anchorHeight,
		AnchorHash:   anchorHash,
		Heights:      rescanHeights,
		Errata:       errataRecords,
	}
	return journal, nil
}

func (s *Scanner) reorgJournalMatchesCanonical(journal *reorgJournal, next sidecar.ChainBlock) (bool, error) {
	if journal == nil || len(journal.ReplayedMetas) != len(journal.Heights) || len(journal.ReplayedMetas) == 0 {
		return false, nil
	}
	expectedHeight := journal.AnchorHeight + 1
	expectedPreviousHash := journal.AnchorHash
	for _, meta := range journal.ReplayedMetas {
		if meta == nil || meta.Height != expectedHeight || !strings.EqualFold(meta.PreviousHash, expectedPreviousHash) {
			return false, nil
		}
		canonical, err := s.sidecar.ChainBlock(meta.Height)
		if err != nil {
			return false, fmt.Errorf("fail to revalidate monero reorg height %d: %w", meta.Height, err)
		}
		if !strings.EqualFold(canonical.Hash, meta.BlockHash) ||
			!strings.EqualFold(canonical.PreviousHash, meta.PreviousHash) {
			return false, nil
		}
		expectedHeight++
		expectedPreviousHash = meta.BlockHash
	}
	return next.Height == expectedHeight && strings.EqualFold(next.PreviousHash, expectedPreviousHash), nil
}

func (s *Scanner) resumeReorgDelivery(journal *reorgJournal) ([]*stypes.TxInItem, error) {
	if journal == nil {
		return nil, nil
	}
	items := append([]*stypes.TxInItem(nil), journal.ReplayedItems...)
	switch journal.Phase {
	case reorgPhaseReplayed:
		if err := s.deliverReorgErrata(journal.Errata); err != nil {
			return nil, err
		}
		journal.Phase = reorgPhaseDelivered
		if err := s.reorgStore.SetJournal(journal); err != nil {
			return nil, err
		}
		return items, nil
	case reorgPhaseDelivered:
		return items, nil
	default:
		return nil, fmt.Errorf("unsupported xmr reorg journal phase %q", journal.Phase)
	}
}

func (s *Scanner) deliverReorgErrata(records []reorgErrataRecord) error {
	for _, record := range records {
		txs := make([]stypes.ErrataTx, 0, len(record.TxIDs))
		for _, txID := range record.TxIDs {
			txs = append(txs, stypes.ErrataTx{TxID: common.TxID(txID), Chain: s.chain})
		}
		if len(txs) == 0 {
			continue
		}
		if s.globalErrataQueue == nil {
			return fmt.Errorf("monero errata queue is not configured")
		}
		block := stypes.ErrataBlock{
			Height:     record.Height,
			Txs:        txs,
			DeliveryID: fmt.Sprintf("xmr-reorg-%d-%s", record.Height, strings.Join(record.TxIDs, ",")),
		}
		if s.errataAckRequired {
			block.Persisted = make(chan error, 1)
		}
		// The queue is unbuffered and this runs with scanMu held, so an
		// observer that has already stopped consuming would otherwise block
		// the scanner forever and wedge BlockScanner.Stop(). Bound the send
		// like the acknowledgement below: the replay is redriven on the next
		// FetchTxs with the same DeliveryID, so a timeout is safe to retry.
		timeout := s.errataDeliveryTimeout
		if timeout <= 0 {
			timeout = 2 * constants.ThorchainBlockTime
		}
		select {
		case s.globalErrataQueue <- block:
		case <-time.After(timeout):
			return fmt.Errorf("timed out delivering monero errata to observer at height %d", record.Height)
		}
		if block.Persisted != nil {
			select {
			case err := <-block.Persisted:
				if err != nil {
					return fmt.Errorf("observer failed to durably process monero errata: %w", err)
				}
			case <-time.After(timeout):
				return fmt.Errorf("timed out waiting for observer to durably process monero errata")
			}
		}
	}
	return nil
}

func (s *Scanner) saveBlockMeta(block sidecar.ChainBlock, height int64, items []*stypes.TxInItem) error {
	if s.reorgStore == nil {
		return nil
	}
	if block.Hash == "" {
		s.logger.Debug().Int64("height", height).Msg("skip monero reorg block meta save: missing block hash")
		return nil
	}
	return s.reorgStore.Set(blockMetaFromItems(block, height, items))
}

func blockMetaFromItems(block sidecar.ChainBlock, height int64, items []*stypes.TxInItem) *reorgBlockMeta {
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

	return &reorgBlockMeta{
		Height:               height,
		BlockHash:            block.Hash,
		PreviousHash:         block.PreviousHash,
		CustomerTransactions: txIDs,
	}
}

func (s *Scanner) refreshAsgardLookup(nextScanHeight int64) error {
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
		if addErr := s.addFrostVaultToLookup(
			lookup,
			seenVaults,
			vault.PubKeyFrost,
			xmrVaultRegistrationBirthday(vault.VaultCreationHeightXmr, nextScanHeight),
		); addErr != nil {
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
		if err := s.addFrostVaultToLookup(
			lookup,
			seenVaults,
			vault.PubKeyFrost,
			xmrVaultRegistrationBirthday(vault.VaultCreationHeightXmr, nextScanHeight),
		); err != nil {
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
	registrationBirthday int64,
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
		registrationBirthday,
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

	item := stypes.NewTxInItem(
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
	)
	item.SpentOutputRefs = s.echoedSpentOutputRefs(transfer, sender, vaultPubKey)
	return item, true
}

// echoedSpentOutputRefs mirrors the sidecar-verified spent refs of an outgoing
// transfer into the observation, in sidecar order with SpendTxHash set to the
// observed tx, exactly as the signing path attests them. A non-signing
// observer can only see the outbound after a verified mark-spent import, so
// echoing keeps every validator's observation identical and lets signer and
// peer attestations pool into a single quorum variant. Refs that would not
// survive ObservedTx validation are dropped as a set rather than poisoning
// the whole observation.
func (s *Scanner) echoedSpentOutputRefs(transfer sidecar.Transfer, sender string, vaultPubKey common.PubKey) []*common.OutputRef {
	if len(transfer.SpentOutputRefs) == 0 {
		return nil
	}
	// Refs are only valid on outbound observations (spends from the vault);
	// thornode rejects inbound observed txs that carry them. Incoming
	// transfers never have refs, but do not rely on the sidecar for that.
	deriveChain := s.chain
	if deriveChain.IsEmpty() {
		deriveChain = common.XMRChain
	}
	vaultAddress, err := vaultPubKey.GetAddress(deriveChain)
	if err != nil || !common.AddressEquals(common.XMRChain, common.Address(sender), vaultAddress) {
		return nil
	}
	refs := make([]common.OutputRef, len(transfer.SpentOutputRefs))
	for i, ref := range transfer.SpentOutputRefs {
		refs[i] = common.OutputRef{
			TxHash:      ref.TxHash,
			OutputIndex: ref.OutputIndex,
			KeyImage:    ref.KeyImage,
			SpendTxHash: transfer.TxID,
		}
	}
	if err := common.ValidateSpentOutputRefs(common.XMRChain, common.TxID(transfer.TxID), refs); err != nil {
		s.logger.Warn().
			Err(err).
			Str("txid", transfer.TxID).
			Int("refs", len(refs)).
			Msg("skip echoing invalid xmr spent output refs from sidecar")
		return nil
	}
	out := make([]*common.OutputRef, len(refs))
	for i := range refs {
		out[i] = &refs[i]
	}
	return out
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
	var httpErr *sidecar.HTTPError
	return errors.As(err, &httpErr) &&
		httpErr.Status == http.StatusConflict &&
		httpErr.Code == "scanner_hash_mismatch"
}
