package monero

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blang/semver"
	"github.com/btcsuite/btcutil/bech32"
	"github.com/cosmos/cosmos-sdk/crypto/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/hashicorp/go-multierror"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.uber.org/atomic"

	"gitlab.com/thorchain/thornode/v3/app"
	"gitlab.com/thorchain/thornode/v3/bifrost/blockscanner"
	"gitlab.com/thorchain/thornode/v3/bifrost/metrics"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/monero/sidecar"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/shared/runners"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/shared/signercache"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/shared/signing"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/shared/types"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/shared/utxo"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient"
	stypes "gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
	bifrosttss "gitlab.com/thorchain/thornode/v3/bifrost/tss"
	gotssblame "gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/blame"
	gotsscommon "gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/common"
	tssp "gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/tss"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/config"
	"gitlab.com/thorchain/thornode/v3/constants"
	memo "gitlab.com/thorchain/thornode/v3/x/thorchain/memo"
	ttypes "gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

const (
	maxAsgardAddresses = 100

	mimirXMRConsolidationEnabled             = "XMRConsolidationEnabled"
	mimirXMRConsolidationCheckIntervalBlocks = "XMRConsolidationCheckIntervalBlocks"
	mimirXMRConsolidationCooldownBlocks      = "XMRConsolidationCooldownBlocks"
	mimirXMRConsolidationMinUnlockedOutputs  = "XMRConsolidationMinUnlockedOutputs"
	mimirXMRConsolidationTargetOutputs       = "XMRConsolidationTargetOutputs"
	mimirXMRConsolidationMinUnlockedAtomic   = "XMRConsolidationMinUnlockedAtomic"
	mimirXMRConsolidationReserveBps          = "XMRConsolidationReserveBps"
	mimirXMRConsolidationChurnLookahead      = "XMRConsolidationChurnLookaheadBlocks"
	mimirXMRConsolidationMaxRetries          = "XMRConsolidationMaxRetries"
	mimirXMRConsolidationMaxInputs           = "XMRConsolidationMaxInputs"
	mimirXMRConsolidationMaxLockBps          = "XMRConsolidationMaxLockBps"

	defaultXMRConsolidationCheckIntervalBlocks = int64(10)
	defaultXMRConsolidationCooldownBlocks      = int64(30)
	defaultXMRConsolidationMinUnlockedOutputs  = int64(40)
	defaultXMRConsolidationTargetOutputs       = int64(15)
	defaultXMRConsolidationMinUnlockedAtomic   = uint64(1_000_000_000_000) // 1 XMR (piconero)
	defaultXMRConsolidationReserveBps          = int64(50)                 // 0.50%
	defaultXMRConsolidationChurnLookahead      = int64(720)
	defaultXMRConsolidationMaxRetries          = int64(3)
	defaultXMRConsolidationMaxInputs           = int64(8)
	defaultXMRConsolidationMaxLockBps          = int64(1000)        // 10.00%
	defaultXMRConsolidationMinReserveAtomic    = uint64(50_000_000) // 0.00005 XMR (piconero)
	defaultXMRSpentRefsRetryMaxAttempts        = 60                 // bounded peer-import retry window after the first failed attempt
)

var ErrSidecarNotConfigured = errors.New("xmr sidecar requires signer_endpoint or api_host configuration")

type signedTxPayload struct {
	TxHash          string                `json:"tx_hash"`
	TxMetadata      string                `json:"tx_metadata"`
	FeeAtomic       uint64                `json:"fee_atomic"`
	MsgID           string                `json:"msg_id,omitempty"`
	Eventuality     tssp.FrostEventuality `json:"eventuality,omitempty"`
	Broadcasted     bool                  `json:"broadcasted,omitempty"`
	SpentOutputRefs []sidecar.SpentRef    `json:"spent_output_refs,omitempty"`
}

type signCheckpoint struct {
	SpentOutputRefs []sidecar.SpentRef `json:"spent_output_refs"`
}

type validatedSignRequest struct {
	vaultPubKey  common.PubKey
	fromAddress  string
	toAddress    string
	amountAtomic uint64
}

type consolidationSettings struct {
	Enabled             bool
	CheckIntervalBlocks int64
	CooldownBlocks      int64
	MinUnlockedOutputs  int64
	TargetOutputs       int64
	MinUnlockedAtomic   uint64
	ReserveBps          int64
	ChurnLookahead      int64
	MaxRetries          int64
	MaxInputs           int64
	MaxLockBps          int64
}

type consolidationVaultState struct {
	LastAttemptHeight int64
	LastSuccessHeight int64
	LastTxHash        string
	ConsecutiveFails  int64
}

// Client implements the chain client interface for Monero.
type Client struct {
	logger zerolog.Logger
	cfg    config.BifrostChainConfiguration
	chain  common.Chain

	thorchainBridge     thorclient.ThorchainBridge
	localNodePubKey     common.PubKey
	vaultBootstrap      *vaultBootstrap
	storage             *blockscanner.BlockScannerStorage
	blockScanner        *blockscanner.BlockScanner
	scanner             *Scanner
	sidecar             *sidecar.Client
	tssServer           tssp.Server
	signerCacheManager  *signercache.CacheManager
	vaultAddressStore   *VaultAddressStore
	vaultAddressMap     map[string]common.Address
	vaultAddrToKey      map[string]string
	vaultAddressMu      sync.RWMutex
	globalSolvencyQueue chan stypes.Solvency

	asgardMu                sync.RWMutex
	asgardAddresses         []common.Address
	lastAsgard              time.Time
	consolidationInitOnce   sync.Once
	consolidationSettingsMu sync.RWMutex
	consolidationSettings   consolidationSettings
	lastConsolidationConfig time.Time
	hasConsolidationConfig  bool
	vaultLockMu             sync.Mutex
	vaultLocks              map[string]*sync.Mutex
	emptyVaultLock          sync.Mutex
	consolidationStateMu    sync.Mutex
	consolidationState      map[string]consolidationVaultState
	consolidateInProgress   *atomic.Bool
	lastConsolidationHeight *atomic.Int64
	lastSolvencyCheckHeight *atomic.Int64
	wg                      *sync.WaitGroup
	stopchan                chan struct{}

	spentRefsRetryInterval    time.Duration
	spentRefsRetryMaxAttempts int
}

// NewClient creates a monero chain client backed by the shared block scanner.
func NewClient(
	thorKeys *thorclient.Keys,
	cfg config.BifrostChainConfiguration,
	tssServer *tssp.TssServer,
	bridge thorclient.ThorchainBridge,
	m *metrics.Metrics,
) (types.ChainClient, error) {
	if !cfg.ChainID.Equals(common.XMRChain) {
		return nil, fmt.Errorf("monero client requires chain id %s, got %s", common.XMRChain, cfg.ChainID)
	}
	if bridge == nil {
		return nil, errors.New("thorchain bridge is nil")
	}
	if m == nil {
		return nil, errors.New("metrics instance is nil")
	}
	if tssServer == nil {
		return nil, errors.New("tss server is nil")
	}
	logger := log.With().Str("module", cfg.ChainID.String()).Logger()
	sc, err := sidecar.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("fail to create sidecar client: %w", err)
	}
	if cleared, clearErr := sc.ClearSessions(); clearErr != nil {
		logger.Warn().Err(clearErr).Msg("fail to clear sidecar sessions on bifrost startup; sidecar may carry stale state")
	} else if cleared > 0 {
		logger.Info().Int("cleared", cleared).Msg("cleared stale sidecar sessions on bifrost startup")
	}

	localNodePubKey := common.EmptyPubKey
	if thorKeys != nil {
		if privKey, keyErr := thorKeys.GetPrivateKey(); keyErr != nil {
			logger.Debug().Err(keyErr).Msg("fail to get local node private key for monero vault mapping")
		} else if tmPubKey, convErr := codec.ToCmtPubKeyInterface(privKey.PubKey()); convErr != nil {
			logger.Debug().Err(convErr).Msg("fail to convert local node pubkey for monero vault mapping")
		} else if pk, pubErr := common.NewPubKeyFromCrypto(tmPubKey); pubErr != nil {
			logger.Debug().Err(pubErr).Msg("fail to encode local node pubkey for monero vault mapping")
		} else {
			localNodePubKey = pk
		}
	}

	vaultBootstrap, bootstrapErr := deriveVaultBootstrap(thorKeys)
	if bootstrapErr != nil {
		logger.Debug().Err(bootstrapErr).Msg("fail to derive local monero frost vault bootstrap material")
	}
	scanner := NewScanner(logger, sc, bridge, cfg.ChainID, localNodePubKey, cfg.BlockScanner.MaxReorgRescanBlocks)
	scanner.SetVaultBootstrap(vaultBootstrap)

	c := &Client{
		logger:                    logger,
		cfg:                       cfg,
		chain:                     cfg.ChainID,
		thorchainBridge:           bridge,
		localNodePubKey:           localNodePubKey,
		vaultBootstrap:            vaultBootstrap,
		scanner:                   scanner,
		sidecar:                   sc,
		tssServer:                 tssServer,
		asgardAddresses:           make([]common.Address, 0),
		vaultLocks:                make(map[string]*sync.Mutex),
		consolidationState:        make(map[string]consolidationVaultState),
		consolidateInProgress:     atomic.NewBool(false),
		lastConsolidationHeight:   atomic.NewInt64(0),
		signerCacheManager:        nil,
		vaultAddressMap:           make(map[string]common.Address),
		vaultAddrToKey:            make(map[string]string),
		lastSolvencyCheckHeight:   atomic.NewInt64(0),
		wg:                        &sync.WaitGroup{},
		stopchan:                  make(chan struct{}),
		spentRefsRetryInterval:    constants.ThorchainBlockTime,
		spentRefsRetryMaxAttempts: defaultXMRSpentRefsRetryMaxAttempts,
	}

	var path string
	if len(c.cfg.BlockScanner.DBPath) > 0 {
		path = fmt.Sprintf("%s/%s", c.cfg.BlockScanner.DBPath, c.cfg.BlockScanner.ChainID)
	}

	c.storage, err = blockscanner.NewBlockScannerStorage(path, c.cfg.ScannerLevelDB)
	if err != nil {
		return nil, fmt.Errorf("fail to create scanner storage: %w", err)
	}

	c.blockScanner, err = blockscanner.NewBlockScanner(c.cfg.BlockScanner, c.storage, m, c.thorchainBridge, c.scanner)
	if err != nil {
		return nil, fmt.Errorf("fail to create block scanner: %w", err)
	}

	c.signerCacheManager, err = signercache.NewSignerCacheManager(c.storage.GetInternalDb())
	if err != nil {
		return nil, fmt.Errorf("fail to create signer cache manager: %w", err)
	}
	c.vaultAddressStore = NewVaultAddressStore(c.storage.GetInternalDb())
	c.scanner.SetReorgStore(NewReorgStore(c.storage.GetInternalDb()))
	// Wire solvency reporting to the per-block scan loop. Without this, the
	// chain client's ReportSolvency only fires from the auto-unhalt
	// SolvencyCheckRunner — which itself is gated on the chain being already
	// halted, so vault drift is never detected during normal operation.
	// Match the XRP / Solana wiring pattern (their scanners take a
	// SolvencyReporter callback in their constructors).
	c.scanner.SetSolvencyReporter(c.ReportSolvency)
	if err := c.loadVaultAddressMap(); err != nil {
		c.logger.Warn().Err(err).Msg("fail to load persisted monero vault addresses")
	}

	return c, nil
}

func (c *Client) Start(
	globalTxsQueue chan stypes.TxIn,
	globalErrataQueue chan stypes.ErrataBlock,
	globalSolvencyQueue chan stypes.Solvency,
	globalNetworkFeeQueue chan common.NetworkFee,
) {
	c.ensureConsolidationState()
	c.globalSolvencyQueue = globalSolvencyQueue
	c.scanner.SetErrataQueue(globalErrataQueue)
	c.blockScanner.Start(globalTxsQueue, globalNetworkFeeQueue)
	c.wg.Add(1)
	go runners.SolvencyCheckRunner(c.GetChain(), c, c.thorchainBridge, c.stopchan, c.wg, constants.ThorchainBlockTime)
	c.wg.Add(1)
	go c.consolidationRunner()
	c.wg.Add(1)
	go c.spentOutputRefsSyncRunner()
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.restoreKeysharesFromRecoveryFiles()
	}()
}

func (c *Client) restoreKeysharesFromRecoveryFiles() {
	if c.sidecar == nil {
		return
	}
	pattern := filepath.Join(app.DefaultNodeHome, "frost-recovery-*.hex")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return
	}
	for _, path := range matches {
		base := filepath.Base(path)
		vaultPubKeyStr := strings.TrimPrefix(base, "frost-recovery-")
		vaultPubKeyStr = strings.TrimSuffix(vaultPubKeyStr, ".hex")

		vaultPubKey, parseErr := common.NewPubKey(vaultPubKeyStr)
		if parseErr != nil {
			c.logger.Err(parseErr).Str("file", base).Msg("xmr recovery: invalid vault pubkey in filename")
			continue
		}

		keyHex, readErr := os.ReadFile(path)
		if readErr != nil {
			c.logger.Err(readErr).Str("file", base).Msg("xmr recovery: failed to read recovery file")
			continue
		}

		restoreErr := c.RestoreKeyshare(vaultPubKey, string(keyHex))
		if restoreErr != nil {
			c.logger.Err(restoreErr).Str("vault", vaultPubKeyStr).Msg("xmr recovery: failed to restore frost keyshare")
			continue
		}

		if removeErr := wipeAndRemoveRecoveryFile(path); removeErr != nil {
			c.logger.Error().
				Err(removeErr).
				Str("vault", vaultPubKeyStr).
				Str("path", path).
				Msg("xmr recovery: frost keyshare restored but failed to remove raw recovery file")
			continue
		}
		c.logger.Info().Str("vault", vaultPubKeyStr).Msg("xmr recovery: frost keyshare restored and recovery file removed")
	}
}

func wipeAndRemoveRecoveryFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat recovery file: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("recovery file is a directory")
	}

	size := info.Size()
	if size > 0 {
		f, openErr := os.OpenFile(path, os.O_WRONLY, 0)
		if openErr != nil {
			return fmt.Errorf("open recovery file for wipe: %w", openErr)
		}
		zeros := make([]byte, 4096)
		remaining := size
		for remaining > 0 {
			n := int64(len(zeros))
			if remaining < n {
				n = remaining
			}
			nInt := int(n)
			written, writeErr := f.Write(zeros[:nInt])
			if writeErr != nil {
				_ = f.Close()
				return fmt.Errorf("wipe recovery file: %w", writeErr)
			}
			if written != nInt {
				_ = f.Close()
				return fmt.Errorf("wipe recovery file: short write")
			}
			remaining -= n
		}
		if err = f.Sync(); err != nil {
			_ = f.Close()
			return fmt.Errorf("sync wiped recovery file: %w", err)
		}
		if err = f.Close(); err != nil {
			return fmt.Errorf("close wiped recovery file: %w", err)
		}
	}

	if err = os.Remove(path); err != nil {
		return fmt.Errorf("remove recovery file: %w", err)
	}
	return nil
}

func (c *Client) spentOutputRefsSyncRunner() {
	defer c.wg.Done()

	c.catchupSpentOutputRefs()

	ticker := time.NewTicker(constants.ThorchainBlockTime * 10)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopchan:
			return
		case <-ticker.C:
			c.catchupSpentOutputRefs()
		}
	}
}

func (c *Client) catchupSpentOutputRefs() {
	if c.thorchainBridge == nil || c.sidecar == nil {
		return
	}
	vaultKeys, err := c.getSpentOutputCatchupVaultKeys()
	if err != nil {
		c.logger.Err(err).Msg("xmr catchup: failed to get vault pubkeys")
		return
	}
	for _, vaultPubKey := range vaultKeys {
		refs, err := c.thorchainBridge.GetXMRSpentOutputRefs(vaultPubKey)
		if err != nil {
			c.logger.Warn().
				Err(err).
				Str("vault", vaultPubKey.String()).
				Msg("xmr catchup: failed to get spent output refs")
			continue
		}

		refsBySpendTx := make(map[string][]common.OutputRef)
		for _, ref := range refs {
			if ref == nil {
				continue
			}
			spendTxHash := strings.TrimSpace(ref.SpendTxHash)
			if spendTxHash == "" {
				c.logger.Warn().
					Str("vault", vaultPubKey.String()).
					Str("input_tx_hash", ref.TxHash).
					Uint32("output_index", ref.OutputIndex).
					Msg("xmr catchup: skipping spent ref without spend tx hash")
				continue
			}
			refsBySpendTx[spendTxHash] = append(refsBySpendTx[spendTxHash], *ref)
		}

		for spendTxHash, refs := range refsBySpendTx {
			obsTx, outbound, ok := c.observedXMROutboundForSpentRefs(spendTxHash, vaultPubKey, refs)
			if !ok {
				continue
			}
			result, markErr := c.sidecar.MarkSpent(vaultPubKey.String(), spendTxHash, xmrSpentRefsToSidecar(refs), false, outbound)
			if markErr != nil {
				c.logger.Err(markErr).
					Str("vault", vaultPubKey.String()).
					Str("spend_tx_hash", spendTxHash).
					Int("refs", len(refs)).
					Msg("xmr catchup: failed to apply spent output refs")
				continue
			}
			c.logger.Info().
				Str("vault", vaultPubKey.String()).
				Str("spend_tx_hash", obsTx.Tx.ID.String()).
				Int("marked", result.Marked).
				Int("not_found", result.NotFound).
				Msg("xmr catchup: applied spent output refs from thorchain")
		}
	}
}

func (c *Client) observedXMROutboundForSpentRefs(txHash string, vaultPubKey common.PubKey, refs []common.OutputRef) (common.ObservedTx, *sidecar.OutboundMeta, bool) {
	if c.thorchainBridge == nil {
		return common.ObservedTx{}, nil, false
	}
	voter, err := c.thorchainBridge.GetObservedTxVoter(txHash)
	if err != nil {
		c.logger.Warn().Err(err).Str("tx_hash", txHash).Msg("xmr catchup: failed to load observed tx metadata")
		return common.ObservedTx{}, nil, false
	}
	observed, ok := queryObservedXMRTx(voter, common.TxID(txHash))
	if !ok {
		c.logger.Warn().Str("tx_hash", txHash).Msg("xmr catchup: observed tx metadata missing xmr outbound")
		return common.ObservedTx{}, nil, false
	}
	if !observed.ObservedPubKey.Equals(vaultPubKey) {
		c.logger.Warn().
			Str("tx_hash", txHash).
			Str("observed_vault", observed.ObservedPubKey.String()).
			Str("spent_ref_vault", vaultPubKey.String()).
			Msg("xmr catchup: spent refs vault does not match observed tx")
		return common.ObservedTx{}, nil, false
	}
	observed.SpentOutputRefs = refs
	if err := validateXMRSpentRefsForSidecar(observed); err != nil {
		c.logger.Warn().
			Err(err).
			Str("tx_hash", txHash).
			Str("vault", vaultPubKey.String()).
			Msg("xmr catchup: skipping invalid spent output refs")
		return common.ObservedTx{}, nil, false
	}
	return observed, xmrOutboundMeta(observed.Tx), true
}

func queryObservedXMRTx(voter *ttypes.QueryObservedTxVoter, txID common.TxID) (common.ObservedTx, bool) {
	if voter == nil {
		return common.ObservedTx{}, false
	}
	candidates := append([]ttypes.QueryObservedTx{voter.GetTx()}, voter.GetTxs()...)
	for _, candidate := range candidates {
		tx := candidate.GetTx()
		if !tx.ID.Equals(txID) || !tx.Chain.Equals(common.XMRChain) {
			continue
		}
		return common.ObservedTx{
			Tx:                    tx,
			Status:                common.Status_incomplete,
			BlockHeight:           candidate.GetBlockHeight(),
			ObservedPubKey:        candidate.GetObservedPubKey(),
			FinaliseHeight:        candidate.GetFinaliseHeight(),
			Aggregator:            candidate.GetAggregator(),
			AggregatorTarget:      candidate.GetAggregatorTarget(),
			AggregatorTargetLimit: candidate.AggregatorTargetLimit,
			SpentOutputRefs:       xmrOutputRefsFromPointers(candidate.GetSpentOutputRefs()),
		}, true
	}
	return common.ObservedTx{}, false
}

func xmrOutboundMeta(tx common.Tx) *sidecar.OutboundMeta {
	var amount, fee uint64
	xmrCoin := tx.Coins.GetCoin(common.XMRAsset)
	if !xmrCoin.IsEmpty() {
		amount, _ = thorchainAmountToAtomic(xmrCoin.Amount)
	}
	gasCoin := tx.Gas.ToCoins().GetCoin(common.XMRAsset)
	if !gasCoin.IsEmpty() {
		fee, _ = thorchainAmountToAtomic(gasCoin.Amount)
	}
	return &sidecar.OutboundMeta{
		FromAddress: tx.FromAddress.String(),
		ToAddress:   tx.ToAddress.String(),
		Amount:      amount,
		Fee:         fee,
		Memo:        tx.Memo,
	}
}

func (c *Client) getSpentOutputCatchupVaultKeys() (common.PubKeys, error) {
	if c.thorchainBridge == nil {
		return nil, fmt.Errorf("thorchain bridge is nil")
	}
	pubKeys, err := c.thorchainBridge.GetPubKeys()
	if err != nil {
		return nil, fmt.Errorf("fail to get vault pubkeys: %w", err)
	}

	vaultKeys := make(common.PubKeys, 0, len(pubKeys))
	seen := make(map[string]struct{}, len(pubKeys))
	for _, pair := range pubKeys {
		if pair.Algo != common.SigningAlgoEd25519 || pair.PubKey.IsEmpty() {
			continue
		}
		key := pair.PubKey.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		vaultKeys = append(vaultKeys, pair.PubKey)
	}
	return vaultKeys, nil
}

func (c *Client) Stop() {
	c.blockScanner.Stop()
	close(c.stopchan)
	c.wg.Wait()
}

func (c *Client) IsBlockScannerHealthy() bool {
	if c.blockScanner == nil {
		return false
	}
	return c.blockScanner.IsHealthy()
}

func (c *Client) ensureConsolidationState() {
	c.consolidationInitOnce.Do(func() {
		if c.consolidateInProgress == nil {
			c.consolidateInProgress = atomic.NewBool(false)
		}
		if c.lastConsolidationHeight == nil {
			c.lastConsolidationHeight = atomic.NewInt64(0)
		}
		if c.lastSolvencyCheckHeight == nil {
			c.lastSolvencyCheckHeight = atomic.NewInt64(0)
		}
		if c.vaultLocks == nil {
			c.vaultLocks = make(map[string]*sync.Mutex)
		}
		if c.consolidationState == nil {
			c.consolidationState = make(map[string]consolidationVaultState)
		}
	})
}

func (c *Client) GetVaultLock(vaultPubKey string) *sync.Mutex {
	c.ensureConsolidationState()
	key := strings.TrimSpace(vaultPubKey)
	if key == "" {
		return &c.emptyVaultLock
	}

	c.vaultLockMu.Lock()
	defer c.vaultLockMu.Unlock()
	lock, ok := c.vaultLocks[key]
	if !ok {
		lock = &sync.Mutex{}
		c.vaultLocks[key] = lock
	}
	return lock
}

func (c *Client) GetVaultLockKey(tx stypes.TxOutItem) string {
	vaultKey := txVaultPubKey(tx)
	return strings.TrimSpace(vaultKey.String())
}

func (c *Client) consolidationRunner() {
	defer c.wg.Done()

	ticker := time.NewTicker(constants.ThorchainBlockTime)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopchan:
			return
		case <-ticker.C:
			c.tryConsolidationPass()
		}
	}
}

func (c *Client) tryConsolidationPass() {
	c.ensureConsolidationState()
	if !c.consolidateInProgress.CompareAndSwap(false, true) {
		return
	}
	defer c.consolidateInProgress.Store(false)

	if err := c.runConsolidationPass(); err != nil {
		c.logger.Debug().Err(err).Msg("xmr consolidation pass failed")
	}
}

func (c *Client) runConsolidationPass() error {
	c.ensureConsolidationState()
	if c.thorchainBridge == nil || c.sidecar == nil || c.tssServer == nil {
		return nil
	}
	if c.localNodePubKey.IsEmpty() {
		return nil
	}
	if c.blockScanner != nil && !c.IsBlockScannerHealthy() {
		return nil
	}

	height, err := c.thorchainBridge.GetBlockHeight()
	if err != nil {
		return fmt.Errorf("fail to get thorchain block height for xmr consolidation: %w", err)
	}
	if height <= 0 {
		return nil
	}
	if c.lastConsolidationHeight.Load() >= height {
		return nil
	}
	c.lastConsolidationHeight.Store(height)

	settings := c.loadConsolidationSettings()
	if !settings.Enabled {
		return nil
	}

	nodeStatus, err := c.thorchainBridge.FetchNodeStatus()
	if err != nil {
		return fmt.Errorf("fail to get node status for xmr consolidation: %w", err)
	}
	if nodeStatus != ttypes.NodeStatus_Active {
		return nil
	}

	// Skip when HALTSIGNING / HaltSigning<Chain> is active. The shared helper is also
	// invoked from SignTx, so this entry-point check is a cheap fast-path that avoids
	// the GetAsgards round-trip below when the network has halted.
	if signing.IsSigningHalted(c.thorchainBridge, c.chain, c.logger) {
		return nil
	}

	vaults, err := c.thorchainBridge.GetAsgards()
	if err != nil {
		return fmt.Errorf("fail to get asgards for xmr consolidation: %w", err)
	}
	if len(vaults) == 0 {
		return nil
	}

	churnInterval := c.getChurnInterval()
	for _, vault := range vaults {
		if !vault.Contains(c.localNodePubKey) {
			continue
		}
		if !vault.GetChains().Has(c.chain) {
			continue
		}
		if c.shouldSkipConsolidationNearChurn(vault, height, churnInterval, settings.ChurnLookahead) {
			c.logger.Debug().
				Str("vault_pubkey", txVaultPubKey(stypes.TxOutItem{VaultPubKey: vault.PubKey, VaultPubKeyFrost: vault.PubKeyFrost}).String()).
				Int64("height", height).
				Int64("churn_lookahead", settings.ChurnLookahead).
				Msg("skip xmr consolidation: near churn")
			continue
		}
		triggerPeriodic := settings.CheckIntervalBlocks > 0 && height%settings.CheckIntervalBlocks == 0
		if !triggerPeriodic {
			continue
		}
		c.consolidateVault(vault, height, settings, triggerPeriodic)
	}
	return nil
}

func (c *Client) shouldSkipConsolidationNearChurn(vault ttypes.Vault, thorchainHeight, churnInterval, lookahead int64) bool {
	if vault.IsRetiring() {
		return true
	}
	if !vault.IsActive() || churnInterval <= 0 || lookahead <= 0 {
		return false
	}
	nextChurnHeight := vault.StatusSince + churnInterval
	return thorchainHeight+lookahead >= nextChurnHeight
}

func (c *Client) consolidateVault(
	vault ttypes.Vault,
	thorchainHeight int64,
	settings consolidationSettings,
	triggerPeriodic bool,
) {
	vaultPubKey := txVaultPubKey(stypes.TxOutItem{
		VaultPubKey:      vault.PubKey,
		VaultPubKeyEddsa: common.EmptyPubKey,
		VaultPubKeyFrost: vault.PubKeyFrost,
	})
	if vaultPubKey.IsEmpty() {
		return
	}

	stateKey := vaultPubKey.String()
	state := c.getConsolidationVaultState(stateKey)
	effectiveCooldown := scaledConsolidationCooldown(settings.CooldownBlocks, state.ConsecutiveFails)
	if effectiveCooldown > 0 && state.LastAttemptHeight > 0 && thorchainHeight-state.LastAttemptHeight < effectiveCooldown {
		c.logger.Debug().
			Str("vault_pubkey", stateKey).
			Int64("last_attempt_height", state.LastAttemptHeight).
			Int64("cooldown_blocks", effectiveCooldown).
			Msg("skip xmr consolidation: cooldown active")
		return
	}

	vaultAddress := c.resolveVaultAddress(vault)
	if vaultAddress.IsEmpty() {
		return
	}

	lock := c.GetVaultLock(stateKey)
	lock.Lock()
	defer lock.Unlock()

	hasMempoolOutgoing, err := c.hasMempoolOutgoingForVault(vaultPubKey, vaultAddress.String())
	if err != nil {
		c.logger.Debug().Err(err).Str("vault_pubkey", stateKey).Msg("skip xmr consolidation: fail to check mempool")
		return
	}
	if hasMempoolOutgoing {
		c.logger.Debug().Str("vault_pubkey", stateKey).Msg("skip xmr consolidation: pending mempool outbound")
		return
	}

	if err = c.ensureVaultRegistered(vaultPubKey); err != nil {
		c.logger.Debug().Err(err).Str("vault_pubkey", stateKey).Msg("skip xmr consolidation: fail to register vault")
		return
	}

	balance, err := c.sidecar.Balance(stateKey)
	if err != nil {
		c.logger.Debug().Err(err).Str("vault_pubkey", stateKey).Msg("skip xmr consolidation: fail to query balance")
		return
	}

	spendableAtomic := spendableBalanceAtomic(balance)
	if spendableAtomic < settings.MinUnlockedAtomic {
		c.logger.Debug().
			Str("vault_pubkey", stateKey).
			Uint64("spendable_atomic", spendableAtomic).
			Uint64("min_unlocked_atomic", settings.MinUnlockedAtomic).
			Msg("skip xmr consolidation: spendable balance below threshold")
		return
	}

	outputs, err := c.sidecar.Outputs(stateKey)
	if err != nil {
		c.logger.Debug().Err(err).Str("vault_pubkey", stateKey).Msg("skip xmr consolidation: fail to query outputs")
		return
	}
	currentHeight := int64(0)
	if c.scanner != nil {
		currentHeight = c.scanner.CurrentHeight()
	}
	unlockedOutputs := countUnlockedOutputs(outputs, currentHeight)
	if unlockedOutputs < settings.MinUnlockedOutputs {
		c.logger.Debug().
			Str("vault_pubkey", stateKey).
			Int64("unlocked_outputs", unlockedOutputs).
			Int64("min_unlocked_outputs", settings.MinUnlockedOutputs).
			Msg("skip xmr consolidation: output count below threshold")
		return
	}
	if unlockedOutputs <= settings.TargetOutputs {
		c.logger.Debug().
			Str("vault_pubkey", stateKey).
			Int64("unlocked_outputs", unlockedOutputs).
			Int64("target_outputs", settings.TargetOutputs).
			Msg("skip xmr consolidation: already at target output count")
		return
	}

	feeRate, err := c.sidecar.FeeRate()
	if err != nil {
		c.logger.Debug().Err(err).Str("vault_pubkey", stateKey).Msg("skip xmr consolidation: fail to query fee rate")
		return
	}

	maxRetries := settings.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 1
	}
	reserve := initialConsolidationReserve(spendableAtomic, settings.ReserveBps)
	maxLockAtomic := xmrConsolidationMaxLockAtomic(spendableAtomic, settings.MaxLockBps)
	if maxLockAtomic == 0 {
		c.logger.Debug().
			Str("vault_pubkey", stateKey).
			Uint64("spendable_atomic", spendableAtomic).
			Int64("max_lock_bps", settings.MaxLockBps).
			Msg("skip xmr consolidation: management lock budget is zero")
		return
	}
	lastErr := error(nil)
	for attempt := int64(1); attempt <= maxRetries; attempt++ {
		if reserve >= spendableAtomic {
			break
		}
		amountAtomic := spendableAtomic - reserve
		if amountAtomic > maxLockAtomic {
			amountAtomic = maxLockAtomic
		}
		amountThorchain := amountAtomic / moneroAtomicToThorchain
		if amountThorchain == 0 {
			break
		}

		gasRate := int64(feeRate)
		if feeRate > uint64(math.MaxInt64) {
			gasRate = math.MaxInt64
		}
		txOutItem := stypes.TxOutItem{
			Chain:            c.chain,
			ToAddress:        vaultAddress,
			VaultPubKey:      vault.PubKey,
			VaultPubKeyEddsa: common.EmptyPubKey,
			VaultPubKeyFrost: vault.PubKeyFrost,
			Coins: common.Coins{
				common.NewCoin(common.XMRAsset, cosmos.NewUint(amountThorchain)),
			},
			Memo:      memo.NewConsolidateMemo().String(),
			GasRate:   gasRate,
			SigningID: fmt.Sprintf("xmr-consolidation:%s:%d:%d:%d", stateKey, thorchainHeight, attempt, amountAtomic),
		}

		rawTx, _, observation, signErr := c.SignTx(txOutItem, thorchainHeight)
		if signErr != nil {
			lastErr = signErr
			if !isRetryableConsolidationErr(signErr) {
				break
			}
			reserve = bumpConsolidationReserve(reserve, settings.ReserveBps)
			continue
		}
		if len(rawTx) == 0 {
			lastErr = errors.New("signed consolidation transaction is empty")
			break
		}
		c.applySpentOutputRefsFromTxInItem(observation)

		txHash, broadcastErr := c.BroadcastTx(txOutItem, rawTx)
		if broadcastErr != nil {
			lastErr = broadcastErr
			if !isRetryableConsolidationErr(broadcastErr) {
				break
			}
			reserve = bumpConsolidationReserve(reserve, settings.ReserveBps)
			continue
		}

		c.setConsolidationVaultState(stateKey, consolidationVaultState{
			LastAttemptHeight: thorchainHeight,
			LastSuccessHeight: thorchainHeight,
			LastTxHash:        txHash,
			ConsecutiveFails:  0,
		})
		c.logger.Info().
			Str("vault_pubkey", stateKey).
			Str("tx_hash", txHash).
			Int64("unlocked_outputs", unlockedOutputs).
			Bool("periodic_trigger", triggerPeriodic).
			Msg("xmr consolidation broadcasted")
		return
	}

	state.LastAttemptHeight = thorchainHeight
	state.ConsecutiveFails++
	c.setConsolidationVaultState(stateKey, state)
	if lastErr != nil {
		c.logger.Debug().
			Err(lastErr).
			Str("vault_pubkey", stateKey).
			Int64("consecutive_failures", state.ConsecutiveFails).
			Msg("xmr consolidation failed")
	}
}

func (c *Client) applySpentOutputRefsFromTxInItem(obs *stypes.TxInItem) {
	if obs == nil || len(obs.SpentOutputRefs) == 0 {
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
	obsTx.SpentOutputRefs = xmrOutputRefsFromPointers(obs.SpentOutputRefs)
	c.ApplySpentOutputRefs(obsTx)
}

func countUnlockedOutputs(outputs []sidecar.Output, currentHeight int64) int64 {
	if len(outputs) == 0 {
		return 0
	}
	count := int64(0)
	for _, out := range outputs {
		if out.Spent {
			continue
		}
		if currentHeight > 0 {
			if currentHeight-int64(out.Height) < 10 {
				continue
			}
		}
		count++
	}
	return count
}

func initialConsolidationReserve(availableAtomic uint64, reserveBps int64) uint64 {
	if reserveBps <= 0 {
		reserveBps = defaultXMRConsolidationReserveBps
	}
	reserve := scaleUint64ByBasisPoints(availableAtomic, reserveBps)
	if reserve < defaultXMRConsolidationMinReserveAtomic {
		reserve = defaultXMRConsolidationMinReserveAtomic
	}
	return reserve
}

func xmrConsolidationMaxLockAtomic(spendableAtomic uint64, maxLockBps int64) uint64 {
	if maxLockBps <= 0 {
		maxLockBps = defaultXMRConsolidationMaxLockBps
	}
	if maxLockBps > 10_000 {
		maxLockBps = 10_000
	}
	return scaleUint64ByBasisPoints(spendableAtomic, maxLockBps)
}

func bumpConsolidationReserve(currentReserve uint64, reserveBps int64) uint64 {
	step := currentReserve / 2
	if reserveBps > 0 {
		extra := scaleUint64ByBasisPoints(currentReserve, reserveBps)
		if extra > step {
			step = extra
		}
	}
	if step < defaultXMRConsolidationMinReserveAtomic {
		step = defaultXMRConsolidationMinReserveAtomic
	}
	if currentReserve > ^uint64(0)-step {
		return ^uint64(0)
	}
	return currentReserve + step
}

func scaleUint64ByBasisPoints(value uint64, bps int64) uint64 {
	if value == 0 || bps <= 0 {
		return 0
	}

	scaled := new(big.Int).SetUint64(value)
	scaled.Mul(scaled, big.NewInt(bps))
	scaled.Quo(scaled, big.NewInt(10_000))
	if !scaled.IsUint64() {
		return math.MaxUint64
	}
	return scaled.Uint64()
}

func scaledConsolidationCooldown(baseCooldown, consecutiveFails int64) int64 {
	if baseCooldown <= 0 {
		return 0
	}
	if consecutiveFails <= 1 {
		return baseCooldown
	}
	if consecutiveFails > math.MaxInt64/baseCooldown {
		return math.MaxInt64
	}
	return baseCooldown * consecutiveFails
}

func isRetryableConsolidationErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "insufficient") || strings.Contains(msg, "not enough funds") {
		return true
	}
	if strings.Contains(msg, "fee") || strings.Contains(msg, "mempool") {
		return true
	}
	return false
}

func (c *Client) hasMempoolOutgoingForVault(vaultPubKey common.PubKey, vaultAddress string) (bool, error) {
	resp, err := c.sidecar.Mempool()
	if err != nil {
		return false, err
	}
	vaultKey := strings.TrimSpace(vaultPubKey.String())
	vaultAddress = strings.TrimSpace(vaultAddress)
	for _, transfer := range resp.Outgoing {
		if transfer.VaultPubKey != "" && strings.EqualFold(strings.TrimSpace(transfer.VaultPubKey), vaultKey) {
			return true, nil
		}
		if transfer.FromAddress != "" && vaultAddress != "" && strings.EqualFold(strings.TrimSpace(transfer.FromAddress), vaultAddress) {
			return true, nil
		}
	}
	return false, nil
}

func (c *Client) getConsolidationVaultState(vaultPubKey string) consolidationVaultState {
	c.ensureConsolidationState()
	c.consolidationStateMu.Lock()
	defer c.consolidationStateMu.Unlock()
	return c.consolidationState[vaultPubKey]
}

func (c *Client) setConsolidationVaultState(vaultPubKey string, state consolidationVaultState) {
	c.ensureConsolidationState()
	c.consolidationStateMu.Lock()
	c.consolidationState[vaultPubKey] = state
	c.consolidationStateMu.Unlock()
}

func (c *Client) loadConsolidationSettings() consolidationSettings {
	c.consolidationSettingsMu.RLock()
	if c.hasConsolidationConfig && time.Since(c.lastConsolidationConfig) < constants.ThorchainBlockTime {
		settings := c.consolidationSettings
		c.consolidationSettingsMu.RUnlock()
		return settings
	}
	c.consolidationSettingsMu.RUnlock()

	c.consolidationSettingsMu.Lock()
	defer c.consolidationSettingsMu.Unlock()
	if c.hasConsolidationConfig && time.Since(c.lastConsolidationConfig) < constants.ThorchainBlockTime {
		return c.consolidationSettings
	}

	enabled := c.cfg.XMRConsolidationEnabled
	if mimirEnabled, ok := c.mimirInt64Raw(mimirXMRConsolidationEnabled); ok {
		enabled = mimirEnabled > 0
	}
	settings := consolidationSettings{
		Enabled:             enabled,
		CheckIntervalBlocks: c.mimirInt64(mimirXMRConsolidationCheckIntervalBlocks, int64OrFallback(c.cfg.XMRConsolidationCheckIntervalBlocks, defaultXMRConsolidationCheckIntervalBlocks)),
		CooldownBlocks:      c.mimirInt64(mimirXMRConsolidationCooldownBlocks, int64OrFallback(c.cfg.XMRConsolidationCooldownBlocks, defaultXMRConsolidationCooldownBlocks)),
		MinUnlockedOutputs:  c.mimirInt64(mimirXMRConsolidationMinUnlockedOutputs, int64OrFallback(c.cfg.XMRConsolidationMinUnlockedOutputs, defaultXMRConsolidationMinUnlockedOutputs)),
		TargetOutputs:       c.mimirInt64(mimirXMRConsolidationTargetOutputs, int64OrFallback(c.cfg.XMRConsolidationTargetOutputs, defaultXMRConsolidationTargetOutputs)),
		MinUnlockedAtomic:   c.mimirUint64(mimirXMRConsolidationMinUnlockedAtomic, uint64OrFallback(c.cfg.XMRConsolidationMinUnlockedAtomic, defaultXMRConsolidationMinUnlockedAtomic)),
		ReserveBps:          c.mimirInt64(mimirXMRConsolidationReserveBps, int64OrFallback(c.cfg.XMRConsolidationReserveBps, defaultXMRConsolidationReserveBps)),
		ChurnLookahead:      c.mimirInt64(mimirXMRConsolidationChurnLookahead, int64OrFallback(c.cfg.XMRConsolidationChurnLookaheadBlocks, defaultXMRConsolidationChurnLookahead)),
		MaxRetries:          c.mimirInt64(mimirXMRConsolidationMaxRetries, int64OrFallback(c.cfg.XMRConsolidationMaxRetries, defaultXMRConsolidationMaxRetries)),
		MaxInputs:           c.mimirInt64(mimirXMRConsolidationMaxInputs, int64OrFallback(c.cfg.XMRConsolidationMaxInputs, defaultXMRConsolidationMaxInputs)),
		MaxLockBps:          c.mimirInt64(mimirXMRConsolidationMaxLockBps, int64OrFallback(c.cfg.XMRConsolidationMaxLockBps, defaultXMRConsolidationMaxLockBps)),
	}
	if settings.CheckIntervalBlocks <= 0 {
		settings.CheckIntervalBlocks = defaultXMRConsolidationCheckIntervalBlocks
	}
	if settings.CooldownBlocks <= 0 {
		settings.CooldownBlocks = defaultXMRConsolidationCooldownBlocks
	}
	if settings.MinUnlockedOutputs <= 0 {
		settings.MinUnlockedOutputs = defaultXMRConsolidationMinUnlockedOutputs
	}
	if settings.TargetOutputs <= 0 {
		settings.TargetOutputs = defaultXMRConsolidationTargetOutputs
	}
	if settings.MinUnlockedAtomic == 0 {
		settings.MinUnlockedAtomic = defaultXMRConsolidationMinUnlockedAtomic
	}
	if settings.ReserveBps <= 0 {
		settings.ReserveBps = defaultXMRConsolidationReserveBps
	}
	if settings.ChurnLookahead <= 0 {
		settings.ChurnLookahead = defaultXMRConsolidationChurnLookahead
	}
	if settings.MaxRetries <= 0 {
		settings.MaxRetries = defaultXMRConsolidationMaxRetries
	}
	if settings.MaxInputs <= 0 {
		settings.MaxInputs = defaultXMRConsolidationMaxInputs
	}
	if settings.MaxInputs > int64(common.MaxXMRSpentOutputRefs) {
		settings.MaxInputs = int64(common.MaxXMRSpentOutputRefs)
	}
	if settings.MaxLockBps <= 0 {
		settings.MaxLockBps = defaultXMRConsolidationMaxLockBps
	}
	if settings.MaxLockBps > 10000 {
		settings.MaxLockBps = 10000
	}
	c.consolidationSettings = settings
	c.lastConsolidationConfig = time.Now()
	c.hasConsolidationConfig = true
	return settings
}

func (c *Client) mimirInt64Raw(key string) (int64, bool) {
	if c.thorchainBridge == nil {
		return 0, false
	}
	value, err := c.thorchainBridge.GetMimir(key)
	if err != nil {
		c.logger.Debug().Err(err).Str("mimir_key", key).Msg("fail to fetch xmr consolidation mimir value")
		return 0, false
	}
	if value < 0 {
		return 0, false
	}
	return value, true
}

func (c *Client) mimirInt64(key string, fallback int64) int64 {
	value, ok := c.mimirInt64Raw(key)
	if !ok {
		return fallback
	}
	if value <= 0 {
		return fallback
	}
	return value
}

func (c *Client) mimirUint64(key string, fallback uint64) uint64 {
	value := c.mimirInt64(key, int64(fallback))
	if value <= 0 {
		return fallback
	}
	return uint64(value)
}

func int64OrFallback(value, fallback int64) int64 {
	if value <= 0 {
		return fallback
	}
	return value
}

func uint64OrFallback(value, fallback uint64) uint64 {
	if value == 0 {
		return fallback
	}
	return value
}

func (c *Client) getChurnInterval() int64 {
	consts, err := c.thorchainBridge.GetConstants()
	if err == nil {
		if interval := consts[constants.ChurnInterval.String()]; interval > 0 {
			return interval
		}
	}
	return constants.GetConstantValues(semver.MustParse("0.0.0")).GetInt64Value(constants.ChurnInterval)
}

func (c *Client) SignTx(tx stypes.TxOutItem, height int64) ([]byte, []byte, *stypes.TxInItem, error) {
	if !tx.Chain.Equals(c.chain) {
		return nil, nil, nil, fmt.Errorf("cannot sign non-xmr tx on monero client: %s", tx.Chain)
	}
	signedTx, checkpoint, observation, err := c.signTx(tx, height)
	if err != nil {
		err = c.postKeysignFailure(tx, height, err)
	}
	return signedTx, checkpoint, observation, err
}

func (c *Client) BroadcastTx(tx stypes.TxOutItem, txBytes []byte) (string, error) {
	var payload signedTxPayload
	if err := json.Unmarshal(txBytes, &payload); err != nil {
		return "", fmt.Errorf("fail to decode monero signed tx payload: %w", err)
	}
	return c.broadcastTx(tx, payload)
}

func (c *Client) broadcastTx(tx stypes.TxOutItem, payload signedTxPayload) (string, error) {
	if payload.TxHash == "" {
		return "", fmt.Errorf("FROST signed tx payload missing tx hash")
	}
	if strings.TrimSpace(payload.TxMetadata) == "" {
		return "", fmt.Errorf("FROST signed tx payload missing tx_hex for broadcast")
	}
	msgID := strings.TrimSpace(payload.MsgID)
	if msgID == "" {
		return "", fmt.Errorf("FROST signed tx payload missing msg_id")
	}
	vaultPubKey := strings.TrimSpace(txVaultPubKey(tx).String())
	if vaultPubKey == "" {
		return "", fmt.Errorf("FROST signed tx payload missing vault_pub_key_frost")
	}

	if c.sidecar == nil {
		return "", fmt.Errorf("%w: sidecar is not initialized", ErrSidecarNotConfigured)
	}

	resp, err := c.sidecar.Broadcast(sidecar.BroadcastReq{
		TxHex:       payload.TxMetadata,
		TxHash:      payload.TxHash,
		VaultPubKey: vaultPubKey,
		MsgID:       msgID,
	})
	if err != nil {
		return "", fmt.Errorf("fail to broadcast FROST tx: %w", err)
	}

	// Use the FROST-validated tx hash; never allow the sidecar to override it.
	txHash := payload.TxHash
	if sidecarHash := strings.TrimSpace(resp.TxHash); sidecarHash != "" && sidecarHash != txHash {
		c.logger.Warn().Str("frost_hash", txHash).Str("sidecar_hash", sidecarHash).
			Msg("sidecar broadcast returned different tx hash than FROST-validated hash; using FROST hash")
	}

	if err := c.signerCacheManager.SetSigned(tx.CacheHash(), txCacheVault(tx, c.GetChain()), txHash); err != nil {
		c.logger.Err(err).Msg("fail to set signer cache after FROST broadcast")
	}
	return txHash, nil
}

func (c *Client) signTx(tx stypes.TxOutItem, height int64) ([]byte, []byte, *stypes.TxInItem, error) {
	if c.sidecar == nil {
		return nil, nil, nil, fmt.Errorf("%w: sidecar is not initialized", ErrSidecarNotConfigured)
	}
	if c.tssServer == nil {
		return nil, nil, nil, errors.New("tss server is not initialized")
	}
	if tx.Coins.IsEmpty() {
		return nil, nil, nil, nil
	}
	if c.signerCacheManager.HasSigned(tx.CacheHash()) {
		c.logger.Info().Interface("tx", tx).Msg("transaction already signed, ignoring")
		return nil, nil, nil, nil
	}
	signReqCtx, err := c.validateSignTx(tx, height, true)
	if err != nil {
		return nil, nil, nil, err
	}

	policyHeight := height
	members, activeMembers, memberErr := c.vaultSigningMembers(signReqCtx.vaultPubKey, policyHeight)
	if memberErr != nil {
		return nil, nil, nil, fmt.Errorf("fail to resolve vault signing members for FROST signing: %w", memberErr)
	}
	version, err := c.thorchainBridge.GetThorchainVersion()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("fail to resolve thorchain version: %w", err)
	}

	txInHash := ""
	if !tx.InHash.IsEmpty() {
		txInHash = tx.InHash.String()
	}
	feeRate := uint64(0)
	if tx.GasRate > 0 {
		feeRate = uint64(tx.GasRate)
	}
	maxInputs := maxInputsFromMimir(0)
	if c.thorchainBridge != nil {
		m, mErr := c.thorchainBridge.GetMimir("MaxUTXOsToSpend")
		if mErr != nil {
			c.logger.Err(mErr).Msg("fail to get MaxUTXOsToSpend")
		}
		if m > 0 {
			maxInputs = maxInputsFromMimir(m)
		}
	}
	maxSelectedInputValueAtomic := uint64(0)
	if isConsolidationTx(tx) {
		consolidationSettings := c.loadConsolidationSettings()
		consolidationMaxInputs := maxInputsFromMimir(consolidationSettings.MaxInputs)
		if consolidationMaxInputs < maxInputs {
			maxInputs = consolidationMaxInputs
		}
		balance, balanceErr := c.sidecar.Balance(signReqCtx.vaultPubKey.String())
		if balanceErr != nil {
			return nil, nil, nil, fmt.Errorf("fail to query xmr consolidation spendable balance: %w", balanceErr)
		}
		maxSelectedInputValueAtomic = xmrConsolidationMaxLockAtomic(spendableBalanceAtomic(balance), consolidationSettings.MaxLockBps)
		if maxSelectedInputValueAtomic == 0 {
			return nil, nil, nil, fmt.Errorf("xmr consolidation selected-input budget is zero")
		}
	}
	dustThreshold, err := thorchainAmountToAtomic(common.XMRChain.DustThreshold())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("fail to convert xmr dust threshold to atomic units: %w", err)
	}

	var requiredInputs []tssp.FrostSpentOutputRef
	if len(tx.Checkpoint) > 0 {
		var ckpt signCheckpoint
		ckptErr := json.Unmarshal(tx.Checkpoint, &ckpt)
		if ckptErr != nil {
			return nil, nil, nil, fmt.Errorf("fail to unmarshal xmr sign checkpoint: %w", ckptErr)
		}
		for _, ref := range ckpt.SpentOutputRefs {
			requiredInputs = append(requiredInputs, tssp.FrostSpentOutputRef{
				TxHash:      ref.TxHash,
				OutputIndex: ref.OutputIndex,
				KeyImage:    ref.KeyImage,
				SpendTxHash: ref.SpendTxHash,
			})
		}
		c.logger.Info().Int("required_inputs", len(requiredInputs)).Msg("using checkpointed inputs for xmr signing retry")
	}
	frostReq := tssp.FrostKeySignRequest{
		VaultPubKey:                 signReqCtx.vaultPubKey.String(),
		ToAddress:                   signReqCtx.toAddress,
		AmountAtomic:                signReqCtx.amountAtomic,
		Memo:                        tx.Memo,
		FeeRate:                     feeRate,
		SignerPubKeys:               members,
		ActiveSignerPubKeys:         activeMembers,
		BlockHeight:                 policyHeight,
		Version:                     version.String(),
		TxInHash:                    txInHash,
		RequestID:                   tx.SigningID,
		RequiredInputs:              requiredInputs,
		MaxInputs:                   maxInputs,
		DustThreshold:               dustThreshold,
		MaxSelectedInputValueAtomic: maxSelectedInputValueAtomic,
	}
	resp, err := c.tssServer.FrostKeySign(frostReq, sidecar.NewFrostAdapter(c.sidecar))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("fail to execute FROST keysign: %w", err)
	}
	if resp.Status == gotsscommon.NA && strings.TrimSpace(resp.TxHex) == "" && strings.TrimSpace(resp.Blame.FailReason) == "" && len(resp.Blame.BlameNodes) == 0 {
		c.logger.Info().Str("vault_pubkey", signReqCtx.vaultPubKey.String()).Msg("local node is not selected for FROST keysign party; standing down")
		return nil, nil, nil, nil
	}
	if resp.Status != gotsscommon.Success {
		if len(resp.Blame.BlameNodes) > 0 {
			return nil, nil, nil, bifrosttss.NewKeysignError(frostBlameToThorchainBlame(resp.Blame))
		}
		return nil, nil, nil, fmt.Errorf("FROST keysign failed: %s", resp.Blame.FailReason)
	}
	msgID := strings.TrimSpace(resp.MsgID)
	if msgID == "" {
		return nil, nil, nil, fmt.Errorf("FROST keysign returned empty msg_id")
	}
	if validateErr := tssp.ValidateFrostEventuality(
		frostReq,
		msgID,
		resp.TxHash,
		resp.TxHex,
		resp.FeeAtomic,
		resp.Eventuality,
	); validateErr != nil {
		return nil, nil, nil, fmt.Errorf("invalid FROST eventuality: %w", validateErr)
	}
	var localSpentRefs []sidecar.SpentRef
	for _, ref := range resp.SpentOutputRefs {
		localSpentRefs = append(localSpentRefs, sidecar.SpentRef{
			TxHash:      ref.TxHash,
			OutputIndex: ref.OutputIndex,
			KeyImage:    ref.KeyImage,
			SpendTxHash: ref.SpendTxHash,
		})
	}
	payload := signedTxPayload{
		TxHash:          resp.TxHash,
		TxMetadata:      resp.TxHex,
		FeeAtomic:       resp.FeeAtomic,
		MsgID:           msgID,
		Eventuality:     resp.Eventuality,
		Broadcasted:     false,
		SpentOutputRefs: localSpentRefs,
	}
	signedTx, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("fail to encode FROST signed tx payload: %w", err)
	}
	ckptBytes, ckptErr := json.Marshal(signCheckpoint{SpentOutputRefs: localSpentRefs})
	if ckptErr != nil {
		c.logger.Error().Err(ckptErr).Msg("fail to marshal xmr sign checkpoint")
	}
	observation := c.buildOutboundObservation(tx, resp.TxHash, resp.FeeAtomic, localSpentRefs)
	return signedTx, ckptBytes, observation, nil
}

func (c *Client) validateSignTx(tx stypes.TxOutItem, thorchainHeight int64, requireThorchainHeight bool) (validatedSignRequest, error) {
	if tx.ToAddress.IsEmpty() {
		return validatedSignRequest{}, fmt.Errorf("to address is empty")
	}
	if !isValidXMROutboundDestination(c.chain, tx.ToAddress) {
		return validatedSignRequest{}, fmt.Errorf("to address %s is not a valid %s address", tx.ToAddress.String(), c.chain.String())
	}
	// Mirror the Rust FROST signer destination policy exactly so unsupported
	// Monero address variants fail before they enter signing.
	if requireThorchainHeight && thorchainHeight <= 0 {
		return validatedSignRequest{}, fmt.Errorf("thorchain height must be greater than zero")
	}

	memoForParsing := tx.GetMemo()
	if memoForParsing != "" {
		parsedMemo, err := memo.ParseMemo(common.LatestVersion, memoForParsing)
		if err != nil {
			return validatedSignRequest{}, fmt.Errorf("fail to parse outbound memo(%s): %w", memoForParsing, err)
		}
		if parsedMemo.IsInbound() {
			return validatedSignRequest{}, fmt.Errorf("inbound memo should not be used for outbound tx")
		}
	}

	if len(tx.Coins) != 1 {
		return validatedSignRequest{}, fmt.Errorf("xmr signing requires exactly one coin, got %d", len(tx.Coins))
	}
	xmrCoin := tx.Coins.GetCoin(common.XMRAsset)
	if xmrCoin.IsEmpty() {
		return validatedSignRequest{}, fmt.Errorf("xmr tx does not contain XMR coin")
	}
	if xmrCoin.Amount.IsZero() {
		return validatedSignRequest{}, fmt.Errorf("xmr outbound amount is zero")
	}
	amountAtomic, err := thorchainAmountToAtomic(xmrCoin.Amount)
	if err != nil {
		return validatedSignRequest{}, fmt.Errorf("fail to convert xmr outbound amount for signing: %w", err)
	}
	if amountAtomic == 0 {
		return validatedSignRequest{}, fmt.Errorf("xmr outbound amount converted to zero atomic units")
	}

	vaultPubKey := txVaultPubKey(tx)
	if vaultPubKey.IsEmpty() {
		return validatedSignRequest{}, fmt.Errorf("vault pubkey is empty")
	}
	fromAddress := strings.TrimSpace(c.deriveVaultAddress(vaultPubKey))
	if fromAddress == "" {
		return validatedSignRequest{}, fmt.Errorf("fail to derive monero vault address from pubkey %s", vaultPubKey.String())
	}
	from, err := common.NewAddress(fromAddress)
	if err != nil {
		return validatedSignRequest{}, fmt.Errorf("derived monero vault address %s is invalid: %w", fromAddress, err)
	}
	if !c.chain.IsValidAddress(from) {
		return validatedSignRequest{}, fmt.Errorf("derived monero vault address %s is not on %s", fromAddress, c.chain.String())
	}

	if err = c.ensureVaultBalance(vaultPubKey, amountAtomic, tx.MaxGas); err != nil {
		return validatedSignRequest{}, err
	}

	return validatedSignRequest{
		vaultPubKey:  vaultPubKey,
		fromAddress:  fromAddress,
		toAddress:    tx.ToAddress.String(),
		amountAtomic: amountAtomic,
	}, nil
}

func isValidXMROutboundDestination(chain common.Chain, address common.Address) bool {
	if !chain.Equals(common.XMRChain) {
		return chain.IsValidAddress(address)
	}
	return common.IsSupportedXMRAddress(address.String())
}

func (c *Client) ensureVaultBalance(vaultPubKey common.PubKey, amountAtomic uint64, maxGas common.Gas) error {
	if c.sidecar == nil {
		return nil
	}

	if err := c.ensureVaultRegistered(vaultPubKey); err != nil {
		return err
	}

	balance, err := c.sidecar.Balance(vaultPubKey.String())
	if err != nil {
		return fmt.Errorf("fail to verify xmr vault balance for %s: %w", vaultPubKey.String(), err)
	}
	availableAtomic := spendableBalanceAtomic(balance)

	maxGasAtomic, err := maxGasToAtomic(maxGas)
	if err != nil {
		return fmt.Errorf("fail to convert xmr max gas for balance guard: %w", err)
	}

	requiredAtomic := amountAtomic
	if maxGasAtomic > 0 {
		if requiredAtomic > (^uint64(0) - maxGasAtomic) {
			return fmt.Errorf("xmr outbound amount overflows uint64 when max gas is applied")
		}
		requiredAtomic += maxGasAtomic
	}

	if availableAtomic < requiredAtomic {
		totalAtomic := balance.Balance
		lockedAtomic := totalAtomic - availableAtomic
		if lockedAtomic > 0 {
			return fmt.Errorf(
				"insufficient xmr balance for signing: available=%d required=%d (total=%d locked=%d, outputs may need 10 confirmations)",
				availableAtomic, requiredAtomic, totalAtomic, lockedAtomic,
			)
		}
		return fmt.Errorf(
			"insufficient xmr balance for signing: available=%d required=%d (amount=%d max_gas=%d)",
			availableAtomic, requiredAtomic, amountAtomic, maxGasAtomic,
		)
	}

	return nil
}

func spendableBalanceAtomic(balance sidecar.Balance) uint64 {
	availableAtomic := balance.UnlockedBalance
	if availableAtomic > balance.Balance {
		availableAtomic = balance.Balance
	}
	if balance.SpendableBalance != nil {
		availableAtomic = *balance.SpendableBalance
		if availableAtomic > balance.Balance {
			availableAtomic = balance.Balance
		}
	}
	return availableAtomic
}

func maxInputsFromMimir(mimirValue int64) int64 {
	maxInputs := int64(15)
	if mimirValue > 0 {
		maxInputs = mimirValue
	}
	if maxInputs > int64(common.MaxXMRSpentOutputRefs) {
		return int64(common.MaxXMRSpentOutputRefs)
	}
	return maxInputs
}

func isConsolidationTx(tx stypes.TxOutItem) bool {
	return tx.Chain.Equals(common.XMRChain) &&
		strings.EqualFold(strings.TrimSpace(tx.Memo), memo.NewConsolidateMemo().String())
}

func maxGasToAtomic(maxGas common.Gas) (uint64, error) {
	if len(maxGas) == 0 {
		return 0, nil
	}
	xmrGasCoin := common.Coins(maxGas).GetCoin(common.XMRAsset)
	if xmrGasCoin.IsEmpty() || xmrGasCoin.Amount.IsZero() {
		return 0, nil
	}
	return thorchainAmountToAtomic(xmrGasCoin.Amount)
}

func fallbackOutboundObservationFee(scanner *Scanner) uint64 {
	if scanner == nil {
		return 0
	}

	txSize, feeRate := scanner.GetNetworkFee()
	if txSize == 0 || feeRate == 0 {
		return 0
	}

	totalAtomic := uint64(0)
	if feeRate > 0 && txSize > math.MaxUint64/feeRate {
		totalAtomic = math.MaxUint64
	} else {
		totalAtomic = txSize * feeRate
	}

	fee := totalFeeAtomicToThorchain(totalAtomic)
	if fee == 0 && totalAtomic > 0 {
		return 1
	}
	return fee
}

func (c *Client) buildOutboundObservation(tx stypes.TxOutItem, txHash string, feeAtomic uint64, spentRefs []sidecar.SpentRef) *stypes.TxInItem {
	vaultPubKey := txVaultPubKey(tx)
	from := c.deriveVaultAddress(vaultPubKey)
	height := int64(0)
	if c.scanner != nil {
		height = c.scanner.CurrentHeight()
	}
	if from == "" || tx.ToAddress.IsEmpty() {
		return nil
	}

	fee := feeAtomic / moneroAtomicToThorchain
	if fee == 0 && c.scanner != nil {
		fee = fallbackOutboundObservationFee(c.scanner)
	}
	item := stypes.NewTxInItem(
		height,
		txHash,
		tx.GetMemo(),
		from,
		tx.ToAddress.String(),
		tx.Coins,
		common.Gas{common.NewCoin(common.XMRAsset, cosmos.NewUint(fee))},
		vaultPubKey,
		"",
		"",
		nil,
	)
	if item != nil && len(spentRefs) > 0 {
		refs := make([]*common.OutputRef, len(spentRefs))
		for i, ref := range spentRefs {
			refs[i] = &common.OutputRef{
				TxHash:      ref.TxHash,
				OutputIndex: ref.OutputIndex,
				KeyImage:    ref.KeyImage,
				SpendTxHash: txHash,
			}
		}
		item.SpentOutputRefs = refs
	}
	return item
}

func (c *Client) ExportKeyshare(groupKey common.PubKey) ([]byte, error) {
	if c.sidecar == nil {
		return nil, fmt.Errorf("sidecar not configured")
	}
	hexKey, err := c.sidecar.ExportKeys(groupKey.String())
	if err != nil {
		return nil, fmt.Errorf("fail to export frost keys for group key %s: %w", groupKey.String(), err)
	}
	return []byte(hexKey), nil
}

func (c *Client) RestoreKeyshare(vaultPubKey common.PubKey, keyBytesHex string) error {
	if c.sidecar == nil {
		return fmt.Errorf("sidecar not configured")
	}
	restored, err := c.sidecar.RestoreKeys(vaultPubKey.String(), keyBytesHex)
	if err != nil {
		return err
	}
	if restored {
		c.logger.Info().Str("vault", vaultPubKey.String()).Msg("restored FROST keyshare from on-chain backup")
	}
	return nil
}

// ApplySpentOutputRefs imports XMR spent refs and outbound metadata from an
// attestation into the sidecar without creating a local attestation. The
// scanner must later observe the sidecar-verified outbound before this node
// signs it locally.
func (c *Client) ApplySpentOutputRefs(tx common.ObservedTx) {
	if !c.applySpentOutputRefsOnce(tx) {
		return
	}

	interval := c.spentRefsRetryInterval
	if interval <= 0 {
		interval = constants.ThorchainBlockTime
	}
	maxAttempts := c.spentRefsRetryMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultXMRSpentRefsRetryMaxAttempts
	}

	c.logger.Info().
		Str("tx_hash", tx.Tx.ID.String()).
		Str("vault", tx.ObservedPubKey.String()).
		Dur("interval", interval).
		Int("max_attempts", maxAttempts).
		Msg("queued xmr spent output refs retry")

	// Retry asynchronously so callers that still need to broadcast the tx, such
	// as local sign/consolidation paths, are not blocked waiting for the tx to
	// appear in the Monero daemon.
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.retrySpentOutputRefs(tx, interval, maxAttempts)
	}()
}

// applySpentOutputRefsOnce performs one verified mark-spent attempt. It returns
// true only for transient failures where another bounded retry can still seed
// the sidecar, such as the spend tx not being indexed yet or the sidecar being
// temporarily unavailable.
func (c *Client) applySpentOutputRefsOnce(tx common.ObservedTx) bool {
	if err := validateXMRSpentRefsForSidecar(tx); err != nil {
		c.logger.Warn().
			Err(err).
			Str("tx_hash", tx.Tx.ID.String()).
			Str("vault", tx.ObservedPubKey.String()).
			Msg("skip invalid xmr spent output refs")
		return false
	}
	vaultPubKey := tx.ObservedPubKey.String()
	txHash := tx.Tx.ID.String()

	refs := xmrSpentRefsToSidecar(tx.SpentOutputRefs)
	result, err := c.sidecar.MarkSpent(vaultPubKey, txHash, refs, false, xmrOutboundMeta(tx.Tx))
	if err != nil {
		// Peer pre-quorum imports cannot rely on Thornode catchup until the refs
		// are committed, so retry sidecar-local transient failures. Validation
		// failures remain terminal and a later committed catchup can still
		// reconcile successfully applied refs independently.
		if isRetryableSpentRefsSidecarErr(err) {
			c.logger.Warn().
				Err(err).
				Str("tx_hash", txHash).
				Str("vault", vaultPubKey).
				Int("refs", len(refs)).
				Msg("mark-spent transient failure")
			return true
		}
		c.logger.Err(err).
			Str("tx_hash", txHash).
			Str("vault", vaultPubKey).
			Int("refs", len(refs)).
			Msg("failed to apply spent output refs from attestation")
		return false
	}
	c.logger.Info().
		Str("tx_hash", txHash).
		Str("vault", vaultPubKey).
		Int("marked", result.Marked).
		Int("not_found", result.NotFound).
		Msg("applied spent output refs from attestation")
	return false
}

// isRetryableSpentRefsSidecarErr identifies sidecar failures that should not
// permanently consume the peer-import opportunity. HTTP 404 covers the sidecar's
// get_transactions race window; 429/5xx and non-HTTP call failures cover local
// sidecar startup, overload, or temporary transport/read/decode failures.
func isRetryableSpentRefsSidecarErr(err error) bool {
	var httpErr *sidecar.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Status == http.StatusNotFound ||
			httpErr.Status == http.StatusTooManyRequests ||
			httpErr.Status >= http.StatusInternalServerError
	}
	return err != nil
}

// retrySpentOutputRefs keeps retrying transient sidecar failures for a bounded
// window. This covers peer pre-quorum imports where the XMR spend may not be
// mined/indexed yet or where the local sidecar is temporarily unavailable, and
// Thornode catchup cannot help until the refs are committed.
func (c *Client) retrySpentOutputRefs(tx common.ObservedTx, interval time.Duration, maxAttempts int) {
	timer := time.NewTimer(interval)
	defer timer.Stop()

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// Wait before each retry so a just-gossiped outbound has time to appear
		// in the Monero daemon/sidecar transaction lookup.
		select {
		case <-c.stopchan:
			return
		case <-timer.C:
		}

		if !c.applySpentOutputRefsOnce(tx) {
			return
		}
		if attempt == maxAttempts {
			break
		}
		// Keep the retry cadence simple and bounded; successful committed
		// catchup can still reconcile the same refs independently later.
		timer.Reset(interval)
	}

	c.logger.Warn().
		Str("tx_hash", tx.Tx.ID.String()).
		Str("vault", tx.ObservedPubKey.String()).
		Int("attempts", maxAttempts).
		Msg("xmr spent output refs retry exhausted")
}

func xmrSpentRefsToSidecar(refs []common.OutputRef) []sidecar.SpentRef {
	sidecarRefs := make([]sidecar.SpentRef, len(refs))
	for i, ref := range refs {
		sidecarRefs[i] = sidecar.SpentRef{
			TxHash:      ref.TxHash,
			OutputIndex: ref.OutputIndex,
			KeyImage:    ref.KeyImage,
			SpendTxHash: ref.SpendTxHash,
		}
	}
	return sidecarRefs
}

func xmrOutputRefsFromPointers(refs []*common.OutputRef) []common.OutputRef {
	if len(refs) == 0 {
		return nil
	}
	out := make([]common.OutputRef, 0, len(refs))
	for _, ref := range refs {
		if ref == nil {
			continue
		}
		out = append(out, *ref)
	}
	return out
}

func validateXMRSpentRefsForSidecar(tx common.ObservedTx) error {
	if len(tx.SpentOutputRefs) == 0 {
		return fmt.Errorf("xmr spent refs are empty")
	}
	if err := tx.Valid(); err != nil {
		return err
	}
	if !tx.Tx.Chain.Equals(common.XMRChain) {
		return fmt.Errorf("spent output refs are only valid for XMR observed txs")
	}
	if err := validateFixedHex(tx.Tx.ID.String(), 32, "spend tx hash"); err != nil {
		return err
	}
	if !common.XMRChain.IsValidAddress(tx.Tx.ToAddress) {
		return fmt.Errorf("xmr spent refs require a valid xmr destination address")
	}
	vaultAddress, err := tx.ObservedPubKey.GetAddress(common.XMRChain)
	if err != nil {
		return fmt.Errorf("derive xmr vault address from observed pubkey: %w", err)
	}
	if !tx.Tx.FromAddress.Equals(vaultAddress) {
		return fmt.Errorf("xmr spent refs require outbound from observed vault")
	}
	for _, ref := range tx.SpentOutputRefs {
		if err := validateFixedHex(ref.TxHash, 32, "spent output tx hash"); err != nil {
			return err
		}
		if err := validateFixedHex(ref.KeyImage, 32, "key image"); err != nil {
			return err
		}
		if err := validateFixedHex(ref.SpendTxHash, 32, "spend tx hash"); err != nil {
			return err
		}
	}
	return nil
}

func validateFixedHex(value string, byteLen int, field string) error {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) != byteLen*2 {
		return fmt.Errorf("%s must be %d-byte hex", field, byteLen)
	}
	decoded, err := hex.DecodeString(trimmed)
	if err != nil {
		return fmt.Errorf("%s must be hex: %w", field, err)
	}
	if len(decoded) != byteLen {
		return fmt.Errorf("%s must be %d-byte hex", field, byteLen)
	}
	return nil
}

func (c *Client) postKeysignFailure(tx stypes.TxOutItem, height int64, signErr error) error {
	if signErr == nil || c.thorchainBridge == nil {
		return signErr
	}

	var keysignError bifrosttss.KeysignError
	if !errors.As(signErr, &keysignError) {
		return signErr
	}
	if len(keysignError.Blame.BlameNodes) == 0 {
		return signErr
	}

	txID, err := c.thorchainBridge.PostKeysignFailure(
		keysignError.Blame,
		height,
		tx.Memo,
		tx.Coins,
		txVaultPubKey(tx),
	)
	if err != nil {
		c.logger.Error().Err(err).Msg("fail to post keysign failure to thorchain")
		return multierror.Append(signErr, fmt.Errorf("fail to post keysign failure to THORChain: %w", err))
	}

	c.logger.Info().Str("tx_id", txID.String()).Msg("post keysign failure to thorchain")
	return signErr
}

func (c *Client) GetHeight() (int64, error) {
	if c.scanner == nil {
		return 0, errors.New("monero scanner is not initialized")
	}
	return c.scanner.GetHeight()
}

func (c *Client) GetAddress(poolPubKey common.PubKey) string {
	if addr := c.deriveVaultAddress(poolPubKey); addr != "" {
		return addr
	}
	addr, err := poolPubKey.GetAddress(c.chain)
	if err != nil {
		c.logger.Err(err).Str("pool_pub_key", poolPubKey.String()).Msg("fail to get monero address for pool pubkey")
		return ""
	}
	return addr.String()
}

func (c *Client) GetAccount(poolPubKey common.PubKey, height *big.Int) (common.Account, error) {
	if addr := c.deriveVaultAddress(poolPubKey); addr != "" {
		return c.GetAccountByAddress(addr, height)
	}

	addr, err := poolPubKey.GetAddress(c.chain)
	if err != nil {
		return common.Account{}, fmt.Errorf("fail to get monero address from pubkey: %w", err)
	}
	if addr.IsEmpty() {
		return common.Account{}, fmt.Errorf("fail to resolve monero vault address for %s", poolPubKey.String())
	}
	return c.GetAccountByAddress(addr.String(), height)
}

func (c *Client) GetAccountByAddress(address string, _ *big.Int) (common.Account, error) {
	if c.sidecar == nil {
		return common.Account{}, fmt.Errorf("xmr account query requires sidecar")
	}
	address = strings.TrimSpace(address)
	if address == "" {
		return common.Account{}, fmt.Errorf("xmr account query requires vault address")
	}

	pubKey, found := c.findVaultPubKeyByAddress(address)
	if !found {
		return common.Account{}, fmt.Errorf("no vault pub key found for address %s", address)
	}

	if err := c.ensureVaultRegistered(pubKey); err != nil {
		return common.Account{}, fmt.Errorf("fail to register monero vault for %s: %w", address, err)
	}

	balance, err := c.sidecar.Balance(pubKey.String())
	if err != nil {
		return common.Account{}, fmt.Errorf("fail to get monero wallet balance for %s: %w", address, err)
	}
	// Account reporting (used by solvency comparison vs thornode's vault.coins)
	// must use the TOTAL on-chain balance — locked + unlocked. Locked outputs
	// (those in the 10-conf maturity window) DO exist on chain and must count
	// toward the wallet's view of "what we own", otherwise every fresh inbound
	// triggers a false-positive insolvency report during normal operation.
	// Spendability for sign-time input selection is a separate concern handled
	// by ensureVaultBalance which uses spendableBalanceAtomic.
	amount := cosmos.NewUint(balance.Balance / moneroAtomicToThorchain)
	return common.Account{
		Sequence:      0,
		AccountNumber: 0,
		Coins: common.NewCoins(
			common.NewCoin(common.XMRAsset, amount),
		),
		HasMemoFlag: false,
	}, nil
}

func (c *Client) findVaultPubKeyByAddress(address string) (common.PubKey, bool) {
	address = strings.TrimSpace(address)
	if address == "" {
		return common.EmptyPubKey, false
	}
	c.vaultAddressMu.RLock()
	defer c.vaultAddressMu.RUnlock()
	if pubKeyStr, ok := c.vaultAddrToKey[strings.ToLower(address)]; ok {
		pk, err := common.NewPubKey(pubKeyStr)
		if err == nil {
			return pk, true
		}
	}
	for pubKeyStr, addr := range c.vaultAddressMap {
		if strings.EqualFold(addr.String(), address) {
			pk, err := common.NewPubKey(pubKeyStr)
			if err == nil {
				return pk, true
			}
		}
	}
	return common.EmptyPubKey, false
}

func (c *Client) GetChain() common.Chain {
	return c.chain
}

func (c *Client) GetConfig() config.BifrostChainConfiguration {
	return c.cfg
}

func (c *Client) CustomKeygen() bool {
	return true
}

func (c *Client) Keygen(req types.CustomKeygenRequest) (types.CustomKeygenResult, error) {
	if c.sidecar == nil {
		return types.CustomKeygenResult{}, fmt.Errorf("%w: sidecar is not initialized", ErrSidecarNotConfigured)
	}
	if c.tssServer == nil {
		return types.CustomKeygenResult{}, fmt.Errorf("tss server is not initialized")
	}

	xmrHeight, heightErr := c.GetHeight()
	if heightErr != nil || xmrHeight <= 0 {
		return types.CustomKeygenResult{}, fmt.Errorf("xmr keygen requires synced monero node (height: %d): %w", xmrHeight, heightErr)
	}

	members := make([]string, 0, len(req.Members))
	for _, member := range req.Members {
		member = strings.TrimSpace(member)
		if member != "" {
			members = append(members, member)
		}
	}
	if len(members) == 0 {
		return types.CustomKeygenResult{}, fmt.Errorf("xmr keygen requires non-empty member set")
	}
	requestedVaultKey := monovaultKeygenLabel(req.BlockHeight, req.PoolPubKey, members)
	version := strings.TrimSpace(req.ThorchainVersion)
	if version == "" && c.thorchainBridge != nil {
		chainVersion, versionErr := c.thorchainBridge.GetThorchainVersion()
		if versionErr == nil {
			version = chainVersion.String()
		}
	}

	frostResp, err := c.tssServer.FrostKeygen(tssp.FrostKeygenRequest{
		VaultPubKey:   requestedVaultKey,
		SignerPubKeys: members,
		BlockHeight:   req.BlockHeight,
		Version:       version,
	}, sidecar.NewFrostAdapter(c.sidecar))
	if err != nil {
		c.logFailClosedCoordination("frost_tss_keygen", err)
		return types.CustomKeygenResult{}, fmt.Errorf("fail to execute FROST keygen: %w", err)
	}
	if frostResp.Status != gotsscommon.Success {
		c.logFailClosedCoordination("frost_tss_keygen", fmt.Errorf("frost keygen failed: %s", frostResp.Blame.FailReason))
		var blame []ttypes.Blame
		if len(frostResp.Blame.BlameNodes) > 0 || strings.TrimSpace(frostResp.Blame.FailReason) != "" {
			blame = append(blame, frostBlameToThorchainBlame(frostResp.Blame))
		}
		return types.CustomKeygenResult{Blame: blame}, fmt.Errorf("FROST keygen failed: %s", strings.TrimSpace(frostResp.Blame.FailReason))
	}

	frostAddress := strings.TrimSpace(frostResp.Address)
	if frostAddress == "" {
		return types.CustomKeygenResult{}, fmt.Errorf("FROST keygen succeeded without an address")
	}
	frostAddr, err := common.NewAddress(frostAddress)
	if err != nil {
		return types.CustomKeygenResult{}, fmt.Errorf("invalid FROST keygen address %q: %w", frostAddress, err)
	}
	groupKeyHex := strings.TrimSpace(frostResp.GroupKey)
	if groupKeyHex == "" {
		return types.CustomKeygenResult{}, fmt.Errorf("FROST keygen succeeded without a group key")
	}

	frostVaultPubKey, err := vaultPubKeyFromGroupKeyHex(groupKeyHex)
	if err != nil {
		return types.CustomKeygenResult{}, fmt.Errorf("fail to convert FROST group key to vault pubkey: %w", err)
	}
	expectedAddress, err := frostVaultPubKey.GetAddress(common.XMRChain)
	if err != nil {
		return types.CustomKeygenResult{}, fmt.Errorf("fail to derive monero address from FROST group key %s: %w", groupKeyHex, err)
	}
	if !frostAddr.Equals(expectedAddress) {
		return types.CustomKeygenResult{}, fmt.Errorf(
			"FROST keygen returned address %s but expected derived vault address %s",
			frostAddr.String(),
			expectedAddress.String(),
		)
	}

	if _, err := c.sidecar.RegisterVault(sidecar.RegisterVaultReq{
		VaultPubKey:         frostVaultPubKey.String(),
		GroupKey:            groupKeyHex,
		Address:             frostAddr.String(),
		Source:              "bifrost",
		VaultCreationHeight: frostResp.ObservedMoneroHeight,
	}); err != nil {
		c.logFailClosedCoordination("frost_tss_keygen_register", err)
		return types.CustomKeygenResult{}, fmt.Errorf("fail to register FROST vault mapping for %s: %w", frostVaultPubKey.String(), err)
	}

	c.setVaultAddressMapping(frostVaultPubKey, frostAddr)
	keyshareHex := strings.TrimSpace(frostResp.KeyBytesHex)
	if keyshareHex == "" {
		return types.CustomKeygenResult{}, fmt.Errorf("FROST keygen returned empty recovery keyshare")
	}
	if _, err := hex.DecodeString(keyshareHex); err != nil {
		return types.CustomKeygenResult{}, fmt.Errorf("FROST keygen returned invalid recovery keyshare hex: %w", err)
	}
	return types.CustomKeygenResult{
		Address:              frostAddr,
		PoolPubKeyFrost:      frostVaultPubKey,
		FrostKeyshareHex:     keyshareHex,
		ObservedMoneroHeight: frostResp.ObservedMoneroHeight,
	}, nil
}

func monovaultKeygenLabel(blockHeight int64, poolPubKey common.PubKey, members []string) string {
	var canonicalMembers []string
	for _, member := range members {
		member = strings.TrimSpace(member)
		if member != "" {
			canonicalMembers = append(canonicalMembers, member)
		}
	}
	sort.Strings(canonicalMembers)
	labelInput := fmt.Sprintf(
		"thorchain-xmr-monovault-keygen\nheight=%d\npool=%s\nmembers=%s",
		blockHeight,
		strings.TrimSpace(poolPubKey.String()),
		strings.Join(canonicalMembers, ","),
	)
	sum := sha256.Sum256([]byte(labelInput))
	hrp := sdk.GetConfig().GetBech32AccountPubPrefix()

	converted, err := bech32.ConvertBits(sum[:20], 8, 5, true)
	if err != nil {
		return hrp + "1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"
	}
	placeholder, err := bech32.Encode(hrp, converted)
	if err != nil {
		return hrp + "1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"
	}
	return placeholder
}

// vaultSigningMembers returns both signer views needed by FROST: full vault
// membership for participant indexes and active membership for this keysign.
func (c *Client) vaultSigningMembers(vaultPubKey common.PubKey, blockHeight int64) ([]string, []string, error) {
	if vaultPubKey.IsEmpty() {
		return nil, nil, fmt.Errorf("vault pubkey is empty")
	}
	vault, err := c.getVaultByPubKey(vaultPubKey)
	if err != nil {
		return nil, nil, fmt.Errorf("fail to get vault membership: %w", err)
	}
	members := vault.GetMembership().Strings()
	if len(members) == 0 {
		// Some test and legacy vault records only populate the raw membership
		// strings. Keep that path so FROST can still derive stable indexes.
		for _, member := range vault.Membership {
			member = strings.TrimSpace(member)
			if member != "" {
				members = append(members, member)
			}
		}
	}
	if len(members) == 0 {
		return nil, nil, fmt.Errorf("vault %s has empty membership", vaultPubKey.String())
	}
	policy, err := c.thorchainBridge.GetKeysignPolicy(vaultPubKey, blockHeight)
	if err != nil {
		return nil, nil, err
	}
	activeMembers := policy.Signers.Strings()
	if len(activeMembers) == 0 {
		return nil, nil, fmt.Errorf("vault %s has empty active membership", vaultPubKey.String())
	}
	// The active signer set must be a filtered subset of full membership. This
	// protects FROST participant indexes from a malformed policy response.
	memberSet := make(map[string]bool, len(members))
	for _, member := range members {
		memberSet[strings.TrimSpace(member)] = true
	}
	for _, activeMember := range activeMembers {
		if !memberSet[strings.TrimSpace(activeMember)] {
			return nil, nil, fmt.Errorf("keysign policy signer %s is not in vault %s membership", activeMember, vaultPubKey.String())
		}
	}
	return members, activeMembers, nil
}

func frostBlameToThorchainBlame(in gotssblame.Blame) ttypes.Blame {
	out := ttypes.Blame{
		FailReason: in.FailReason,
		Round:      in.Round,
		IsUnicast:  in.IsUnicast,
		BlameNodes: make([]ttypes.Node, len(in.BlameNodes)),
	}
	for i, node := range in.BlameNodes {
		out.BlameNodes[i] = ttypes.Node{
			Pubkey:         node.Pubkey,
			BlameData:      node.BlameData,
			BlameSignature: node.BlameSignature,
		}
	}
	return out
}

func (c *Client) OnObservedTxIn(txIn stypes.TxInItem, _ int64) {
	m, err := memo.ParseMemo(common.LatestVersion, txIn.Memo)
	if err != nil {
		// ParseMemo errors are expected for non-outbound memos and THORName inbounds.
		c.logger.Debug().Err(err).Str("memo", txIn.Memo).Msg("fail to parse memo")
		return
	}
	if !m.IsOutbound() || m.GetTxID().IsEmpty() {
		return
	}

	if err = c.signerCacheManager.SetSigned(
		txIn.CacheHash(c.GetChain(), m.GetTxID().String()),
		txIn.CacheVault(c.GetChain()),
		txIn.Tx,
	); err != nil {
		c.logger.Err(err).Msg("fail to update signer cache")
	}
}

func (c *Client) GetConfirmationCount(txIn stypes.TxIn) int64 {
	if len(txIn.TxArray) == 0 || txIn.MemPool {
		return 0
	}

	addresses, err := c.getAsgardAddress()
	if err != nil {
		c.logger.Err(err).Msg("fail to get asgard addresses")
		c.asgardMu.RLock()
		addresses = append(addresses, c.asgardAddresses...)
		c.asgardMu.RUnlock()
	}

	totalValue := txIn.GetTotalTransactionValue(common.XMRAsset, addresses)
	coinbaseThorchain := coinbaseThorchain()

	// Apply mimir-configurable confirmation multiplier (same lever as UTXO/ETH chains).
	// Default confMul = MaxBasisPts (10000) = 1x coinbase. Higher values reduce
	// required confirmations; lower values increase them.
	confMul, err := utxo.GetConfMulBasisPoint(c.GetChain().String(), c.thorchainBridge)
	if err != nil {
		c.logger.Debug().Err(err).Msg("fail to get xmr conf multiplier mimir value")
	}
	confValue := common.GetUncappedShare(confMul, cosmos.NewUint(constants.MaxBasisPts), cosmos.NewUint(coinbaseThorchain))
	if confValue.IsZero() {
		confValue = cosmos.NewUint(coinbaseThorchain)
	}
	confirm := confirmationRequired(totalValue, confValue)

	// Apply mimir-configurable max confirmation cap.
	confirm, err = utxo.MaxConfAdjustment(confirm, c.GetChain().String(), c.thorchainBridge)
	if err != nil {
		c.logger.Debug().Err(err).Msg("fail to get xmr max conf adjustment")
	}

	// Apply configured minimum floor (always wins over max cap).
	if confirm < c.cfg.MinConfirmations {
		confirm = c.cfg.MinConfirmations
	}

	c.logger.Debug().
		Str("total_tx_value", totalValue.String()).
		Uint64("coinbase_atomic", defaultCoinbaseValue).
		Uint64("coinbase_thorchain", coinbaseThorchain).
		Str("conf_multiplier", confMul.String()).
		Str("conf_value", confValue.String()).
		Uint64("min_confirmations", c.cfg.MinConfirmations).
		Uint64("confirmations", confirm).
		Msg("calculated xmr confirmation requirement")

	return int64(confirm)
}

func (c *Client) ConfirmationCountReady(txIn stypes.TxIn) bool {
	if len(txIn.TxArray) == 0 || txIn.MemPool {
		return true
	}

	blockHeight := txIn.TxArray[0].BlockHeight
	confirm := txIn.ConfirmationRequired
	return (c.scanner.CurrentHeight() - blockHeight) >= confirm
}

func (c *Client) GetBlockScannerHeight() (int64, error) {
	return c.blockScanner.PreviousHeight(), nil
}

func (c *Client) GetLatestTxForVault(vault string) (string, string, error) {
	lastObserved, err := c.signerCacheManager.GetLatestRecordedTx(stypes.InboundCacheKey(vault, c.GetChain().String()))
	if err != nil {
		return "", "", err
	}
	lastBroadcasted, err := c.signerCacheManager.GetLatestRecordedTx(stypes.BroadcastCacheKey(vault, c.GetChain().String()))
	return lastObserved, lastBroadcasted, err
}

func (c *Client) RollbackBlockScanner() error {
	return c.blockScanner.RollbackToLastObserved()
}

func (c *Client) ShouldReportSolvency(height int64) bool {
	if c.lastSolvencyCheckHeight == nil || c.scanner == nil {
		return false
	}
	if height <= c.lastSolvencyCheckHeight.Load() {
		return false
	}
	solvencyBlocks := c.cfg.SolvencyBlocks
	if solvencyBlocks <= 0 {
		solvencyBlocks = 10
	}
	return height%solvencyBlocks == 0 && c.scanner.lastFee.Load() > 0
}

func (c *Client) ReportSolvency(height int64) error {
	if !c.ShouldReportSolvency(height) {
		return nil
	}
	// when block scanner is not healthy, only report from auto-unhalt SolvencyCheckRunner
	if !c.IsBlockScannerHealthy() {
		if c.blockScanner != nil && height == c.blockScanner.PreviousHeight()+1 {
			return nil
		}
	}

	asgardVaults, err := c.thorchainBridge.GetAsgards()
	if err != nil {
		return fmt.Errorf("fail to get asgards: %w", err)
	}

	currentGasFee := cosmos.NewUint(3 * totalFeeAtomicToThorchain(c.scanner.lastFee.Load()))
	msgs := make([]stypes.Solvency, 0, len(asgardVaults))
	solventMsgs := make([]stypes.Solvency, 0, len(asgardVaults))
	checkedVaults := 0
	for i := range asgardVaults {
		address := c.resolveVaultAddress(asgardVaults[i])
		if address.IsEmpty() {
			continue
		}

		acct, acctErr := c.GetAccountByAddress(address.String(), nil)
		if acctErr != nil {
			c.logger.Err(acctErr).Str("address", address.String()).Msg("fail to get monero account balance for solvency")
			continue
		}
		checkedVaults++

		msg := stypes.Solvency{
			Height: height,
			Chain:  c.chain,
			PubKey: asgardVaults[i].PubKey,
			Coins:  acct.Coins,
		}

		if runners.IsVaultSolvent(c.chain, acct, asgardVaults[i], currentGasFee) {
			solventMsgs = append(solventMsgs, msg)
			continue
		}
		msgs = append(msgs, msg)
	}

	if !c.IsBlockScannerHealthy() && checkedVaults > 0 && len(solventMsgs) == checkedVaults {
		msgs = solventMsgs
	}

	for i := range msgs {
		select {
		case c.globalSolvencyQueue <- msgs[i]:
		case <-time.After(constants.ThorchainBlockTime):
			c.logger.Info().Msg("fail to send monero solvency info to THORChain: timeout")
		}
	}
	c.lastSolvencyCheckHeight.Store(height)
	return nil
}

func (c *Client) logFailClosedCoordination(path string, err error) {
	if err == nil {
		return
	}
	c.logger.Error().
		Err(err).
		Str("coordination_path", path).
		Msg("xmr frost coordination failed closed")
}

func (c *Client) getAsgardAddress() ([]common.Address, error) {
	c.asgardMu.RLock()
	if time.Since(c.lastAsgard) < constants.ThorchainBlockTime && len(c.asgardAddresses) > 0 {
		result := append([]common.Address(nil), c.asgardAddresses...)
		c.asgardMu.RUnlock()
		return result, nil
	}
	c.asgardMu.RUnlock()

	c.asgardMu.Lock()
	defer c.asgardMu.Unlock()
	if time.Since(c.lastAsgard) < constants.ThorchainBlockTime && len(c.asgardAddresses) > 0 {
		return append([]common.Address(nil), c.asgardAddresses...), nil
	}

	vaults, err := c.thorchainBridge.GetAsgards()
	if err != nil {
		return nil, fmt.Errorf("fail to get asgards: %w", err)
	}

	next := make([]common.Address, 0, len(vaults))
	seen := make(map[string]struct{}, len(vaults))

	for _, vault := range vaults {
		address := c.resolveVaultAddress(vault)
		if address.IsEmpty() {
			continue
		}
		key := address.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		next = append(next, address)
	}

	if len(next) > maxAsgardAddresses {
		next = next[len(next)-maxAsgardAddresses:]
	}

	c.asgardAddresses = next
	c.lastAsgard = time.Now()
	result := append([]common.Address(nil), c.asgardAddresses...)

	return result, nil
}

func (c *Client) ensureVaultRegistered(vaultPubKey common.PubKey) error {
	if c.sidecar == nil || vaultPubKey.IsEmpty() {
		return nil
	}

	address, err := ensureVaultRegistered(c.logger, c.sidecar, c.chain, vaultPubKey, c.vaultBootstrap, "bifrost", 0)
	if err != nil {
		return err
	}
	if !address.IsEmpty() {
		c.setVaultAddressMapping(vaultPubKey, address)
	}
	return nil
}

func (c *Client) deriveVaultAddress(vaultPubKey common.PubKey) string {
	if vaultPubKey.IsEmpty() {
		return ""
	}
	address, err := vaultPubKey.GetAddress(c.chain)
	if err != nil || address.IsEmpty() {
		c.logger.Debug().Err(err).Str("vault_pubkey", vaultPubKey.String()).Msg("fail to derive monero vault address from pubkey")
		return ""
	}
	return address.String()
}

func (c *Client) resolveVaultAddress(vault ttypes.Vault) common.Address {
	vaultPubKey := vault.PubKeyFrost
	if vaultPubKey.IsEmpty() {
		return common.NoAddress
	}

	addr, err := vaultPubKey.GetAddress(c.chain)
	if err != nil || addr.IsEmpty() {
		return common.NoAddress
	}
	c.setVaultAddressMapping(vaultPubKey, addr)
	return addr
}

func (c *Client) setVaultAddressMapping(vaultPubKey common.PubKey, address common.Address) {
	if vaultPubKey.IsEmpty() || address.IsEmpty() {
		return
	}

	key := vaultPubKey.String()
	c.vaultAddressMu.Lock()
	if existing, ok := c.vaultAddressMap[key]; ok && existing.Equals(address) {
		c.vaultAddressMu.Unlock()
		return
	}
	c.vaultAddressMap[key] = address
	if c.vaultAddrToKey == nil {
		c.vaultAddrToKey = make(map[string]string)
	}
	c.vaultAddrToKey[strings.ToLower(address.String())] = key
	c.vaultAddressMu.Unlock()

	if c.vaultAddressStore != nil {
		if err := c.vaultAddressStore.Set(key, address.String()); err != nil {
			c.logger.Debug().Err(err).Str("vault_pubkey", key).Msg("fail to persist monero vault address mapping")
		}
	}
	if c.scanner != nil {
		c.scanner.SetManualVaultAddress(vaultPubKey, address.String())
	}
}

func (c *Client) cachedVaultAddress(vaultPubKey common.PubKey) (common.Address, bool) {
	if vaultPubKey.IsEmpty() {
		return common.NoAddress, false
	}

	c.vaultAddressMu.RLock()
	address, ok := c.vaultAddressMap[vaultPubKey.String()]
	c.vaultAddressMu.RUnlock()
	if ok && !address.IsEmpty() {
		return address, true
	}
	return common.NoAddress, false
}

func (c *Client) loadVaultAddressMap() error {
	if c.vaultAddressStore == nil {
		return nil
	}
	stored, err := c.vaultAddressStore.List()
	if err != nil {
		return err
	}
	for vaultPubKey, addressStr := range stored {
		vaultKey, keyErr := common.NewPubKey(vaultPubKey)
		if keyErr != nil {
			c.logger.Debug().Err(keyErr).Str("vault_pubkey", vaultPubKey).Msg("skip invalid persisted monero vault pubkey")
			continue
		}
		address, addrErr := common.NewAddress(addressStr)
		if addrErr != nil {
			c.logger.Debug().Err(addrErr).Str("address", addressStr).Msg("skip invalid persisted monero vault address")
			continue
		}
		derived, deriveErr := vaultKey.GetAddress(c.chain)
		if deriveErr != nil || derived.IsEmpty() {
			c.logger.Debug().Err(deriveErr).Str("vault_pubkey", vaultPubKey).Msg("skip persisted monero vault mapping: cannot derive address from vault pubkey")
			continue
		}
		if !derived.Equals(address) {
			c.logger.Warn().
				Str("vault_pubkey", vaultPubKey).
				Str("persisted_address", address.String()).
				Str("derived_address", derived.String()).
				Msg("ignoring persisted monero vault mapping that does not match derived vault address")
			continue
		}
		c.setVaultAddressMapping(vaultKey, address)
	}
	return nil
}

func txVaultPubKey(tx stypes.TxOutItem) common.PubKey {
	return tx.VaultPubKeyFrost
}

func txCacheVault(tx stypes.TxOutItem, chain common.Chain) string {
	return stypes.BroadcastCacheKey(txVaultPubKey(tx).String(), chain.String())
}

func coinbaseThorchain() uint64 {
	coinbaseThorchain := uint64(defaultCoinbaseValue / moneroAtomicToThorchain)
	if coinbaseThorchain == 0 {
		return 1
	}
	return coinbaseThorchain
}

func confirmationRequired(totalValue, confValue cosmos.Uint) uint64 {
	if confValue.IsZero() {
		return 0
	}
	return totalValue.Quo(confValue).Uint64()
}

func thorchainAmountToAtomic(amount cosmos.Uint) (uint64, error) {
	if amount.IsNil() {
		return 0, fmt.Errorf("xmr outbound amount is nil")
	}

	amountAtomic := amount.MulUint64(moneroAtomicToThorchain)
	amountAtomicBI := amountAtomic.BigInt()
	if amountAtomicBI == nil || !amountAtomicBI.IsUint64() {
		return 0, fmt.Errorf("xmr outbound amount %s overflows uint64 after atomic conversion", amount.String())
	}

	return amountAtomicBI.Uint64(), nil
}

func (c *Client) getVaultByPubKey(vaultPubKey common.PubKey) (ttypes.Vault, error) {
	if vaultPubKey.IsEmpty() {
		return ttypes.Vault{}, fmt.Errorf("vault pubkey is empty")
	}
	vault, err := c.thorchainBridge.GetVault(vaultPubKey.String())
	if err == nil {
		return vault, nil
	}

	vaults, asgardErr := c.thorchainBridge.GetAsgards()
	if asgardErr != nil {
		return ttypes.Vault{}, asgardErr
	}
	for _, item := range vaults {
		if item.PubKeyFrost.Equals(vaultPubKey) {
			return item, nil
		}
	}
	return ttypes.Vault{}, err
}
