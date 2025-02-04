package thorchain

import (
	"errors"
	"fmt"

	upgradekeeper "cosmossdk.io/x/upgrade/keeper"
	"github.com/blang/semver"
	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cosmos/cosmos-sdk/codec"
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/keeper"
	kv1 "gitlab.com/thorchain/thornode/v3/x/thorchain/keeper/v1"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

const (
	genesisBlockHeight = 1
)

// ErrNotEnoughToPayFee will happen when the emitted asset is not enough to pay for fee
var ErrNotEnoughToPayFee = errors.New("not enough asset to pay for fees")

// Manager is an interface to define all the required methods
type Manager interface {
	GetConstants() constants.ConstantValues
	GetVersion() semver.Version
	Keeper() keeper.Keeper
	GasMgr() GasManager
	EventMgr() EventManager
	TxOutStore() TxOutStore
	NetworkMgr() NetworkManager
	ValidatorMgr() ValidatorManager
	ObMgr() ObserverManager
	PoolMgr() PoolManager
	SwapQ() SwapQueue
	OrderBookMgr() OrderBook
	Slasher() Slasher
	TradeAccountManager() TradeAccountManager
	SecuredAssetManager() SecuredAssetManager
}

type TradeAccountManager interface {
	EndBlock(ctx cosmos.Context, keeper keeper.Keeper) error
	Deposit(_ cosmos.Context, _ common.Asset, amount cosmos.Uint, owner cosmos.AccAddress, assetAddr common.Address, _ common.TxID) (cosmos.Uint, error)
	Withdrawal(_ cosmos.Context, _ common.Asset, amount cosmos.Uint, owner cosmos.AccAddress, assetAddr common.Address, _ common.TxID) (cosmos.Uint, error)
	BalanceOf(_ cosmos.Context, _ common.Asset, owner cosmos.AccAddress) cosmos.Uint
}

type SecuredAssetManager interface {
	EndBlock(ctx cosmos.Context, keeper keeper.Keeper) error
	Deposit(_ cosmos.Context, _ common.Asset, amount cosmos.Uint, owner cosmos.AccAddress, assetAddr common.Address, _ common.TxID) (cosmos.Coin, error)
	Withdraw(_ cosmos.Context, _ common.Asset, amount cosmos.Uint, owner cosmos.AccAddress, assetAddr common.Address, _ common.TxID) (common.Coin, error)
	BalanceOf(_ cosmos.Context, _ common.Asset, owner cosmos.AccAddress) cosmos.Uint
	GetSecuredAssetStatus(_ cosmos.Context, _ common.Asset) (keeper.SecuredAsset, cosmos.Uint, error)
	GetShareSupply(_ cosmos.Context, _ common.Asset) cosmos.Uint
	CheckHalt(_ cosmos.Context) error
}

// GasManager define all the methods required to manage gas
type GasManager interface {
	BeginBlock()
	EndBlock(ctx cosmos.Context, keeper keeper.Keeper, eventManager EventManager)
	AddGasAsset(outAsset common.Asset, gas common.Gas, increaseTxCount bool)
	ProcessGas(ctx cosmos.Context, keeper keeper.Keeper)
	GetGas() common.Gas
	GetAssetOutboundFee(ctx cosmos.Context, asset common.Asset, inRune bool) (cosmos.Uint, error)
	GetMaxGas(ctx cosmos.Context, chain common.Chain) (common.Coin, error)
	GetGasRate(ctx cosmos.Context, chain common.Chain) cosmos.Uint
	GetNetworkFee(ctx cosmos.Context, chain common.Chain) (types.NetworkFee, error)
	CalcOutboundFeeMultiplier(ctx cosmos.Context, targetSurplusRune, gasSpentRune, gasWithheldRune, maxMultiplier, minMultiplier cosmos.Uint) cosmos.Uint
}

// EventManager define methods need to be support to manage events
type EventManager interface {
	EmitEvent(ctx cosmos.Context, evt EmitEventItem) error
	EmitGasEvent(ctx cosmos.Context, gasEvent *EventGas) error
	EmitSwapEvent(ctx cosmos.Context, swap *EventSwap) error
	EmitFeeEvent(ctx cosmos.Context, feeEvent *EventFee) error
}

// TxOutStore define the method required for TxOutStore
type TxOutStore interface {
	EndBlock(ctx cosmos.Context, mgr Manager) error
	GetBlockOut(ctx cosmos.Context) (*TxOut, error)
	ClearOutboundItems(ctx cosmos.Context)
	GetOutboundItems(ctx cosmos.Context) ([]TxOutItem, error)
	TryAddTxOutItem(ctx cosmos.Context, mgr Manager, toi TxOutItem, minOut cosmos.Uint) (bool, error)
	UnSafeAddTxOutItem(ctx cosmos.Context, mgr Manager, toi TxOutItem, height int64) error
	GetOutboundItemByToAddress(cosmos.Context, common.Address) []TxOutItem
	CalcTxOutHeight(cosmos.Context, semver.Version, TxOutItem) (int64, cosmos.Uint, error)
	DiscoverOutbounds(ctx cosmos.Context, transactionFeeAsset cosmos.Uint, maxGasAsset common.Coin, toi TxOutItem, vaults Vaults) ([]TxOutItem, cosmos.Uint)
}

// ObserverManager define the method to manage observes
type ObserverManager interface {
	BeginBlock()
	EndBlock(ctx cosmos.Context, keeper keeper.Keeper)
	AppendObserver(chain common.Chain, addrs []cosmos.AccAddress)
	List() []cosmos.AccAddress
}

// ValidatorManager define the method to manage validators
type ValidatorManager interface {
	BeginBlock(ctx cosmos.Context, mgr Manager, existingValidators []string) error
	EndBlock(ctx cosmos.Context, mgr Manager) []abci.ValidatorUpdate
	processRagnarok(ctx cosmos.Context, mgr Manager) error
	NodeAccountPreflightCheck(ctx cosmos.Context, na NodeAccount, constAccessor constants.ConstantValues) (NodeStatus, error)
}

// NetworkManager interface define the contract of network Manager
type NetworkManager interface {
	TriggerKeygen(ctx cosmos.Context, nas NodeAccounts) error
	RotateVault(ctx cosmos.Context, vault Vault) error
	BeginBlock(ctx cosmos.Context, mgr Manager) error
	EndBlock(ctx cosmos.Context, mgr Manager) error
	UpdateNetwork(ctx cosmos.Context, constAccessor constants.ConstantValues, gasManager GasManager, eventMgr EventManager) error
	SpawnDerivedAsset(ctx cosmos.Context, asset common.Asset, mgr Manager)
	CalcAnchor(_ cosmos.Context, _ Manager, _ common.Asset) (cosmos.Uint, cosmos.Uint, cosmos.Uint)
}

// PoolManager interface define the contract of PoolManager
type PoolManager interface {
	EndBlock(ctx cosmos.Context, mgr Manager) error
}

// SwapQueue interface define the contract of Swap Queue
type SwapQueue interface {
	EndBlock(ctx cosmos.Context, mgr Manager) error
}

// OrderBook interface define the contract of Order Book
type OrderBook interface {
	EndBlock(ctx cosmos.Context, mgr Manager) error
}

// Slasher define all the method to perform slash
type Slasher interface {
	BeginBlock(ctx cosmos.Context, constAccessor constants.ConstantValues)
	LackSigning(ctx cosmos.Context, mgr Manager) error
	SlashVault(ctx cosmos.Context, vaultPK common.PubKey, coins common.Coins, mgr Manager) error
	IncSlashPoints(ctx cosmos.Context, point int64, addresses ...cosmos.AccAddress)
	DecSlashPoints(ctx cosmos.Context, point int64, addresses ...cosmos.AccAddress)
}

// Though Swapper is not a full manager, it is recorded here for versioning convenience.
type Swapper interface {
	Swap(ctx cosmos.Context,
		keeper keeper.Keeper,
		tx common.Tx,
		target common.Asset,
		destination common.Address,
		swapTarget cosmos.Uint,
		dexAgg string,
		dexAggTargetAsset string,
		dexAggLimit *cosmos.Uint,
		swp StreamingSwap,
		synthVirtualDepthMult int64,
		mgr Manager,
	) (cosmos.Uint, []*EventSwap, error)
	CalcAssetEmission(X, x, Y cosmos.Uint) cosmos.Uint
	CalcLiquidityFee(X, x, Y cosmos.Uint) cosmos.Uint
	CalcSwapSlip(Xi, xi cosmos.Uint) cosmos.Uint
	GetSwapCalc(X, x, Y, slipBps, minSlipBps cosmos.Uint) (emitAssets, liquidityFee, slip cosmos.Uint)
}

// Mgrs is an implementation of Manager interface
type Mgrs struct {
	currentVersion semver.Version
	constAccessor  constants.ConstantValues
	gasMgr         GasManager
	eventMgr       EventManager
	txOutStore     TxOutStore
	networkMgr     NetworkManager
	validatorMgr   ValidatorManager
	obMgr          ObserverManager
	poolMgr        PoolManager
	swapQ          SwapQueue
	orderBook      OrderBook
	slasher        Slasher
	tradeManager   TradeAccountManager
	securedManager SecuredAssetManager

	K             keeper.Keeper
	cdc           codec.Codec
	coinKeeper    bankkeeper.Keeper
	accountKeeper authkeeper.AccountKeeper
	upgradeKeeper *upgradekeeper.Keeper
	storeKey      cosmos.StoreKey
}

// NewManagers  create a new Manager
func NewManagers(
	keeper keeper.Keeper,
	cdc codec.Codec,
	coinKeeper bankkeeper.Keeper,
	accountKeeper authkeeper.AccountKeeper,
	upgradeKeeper *upgradekeeper.Keeper,
	storeKey cosmos.StoreKey,
) *Mgrs {
	return &Mgrs{
		K:             keeper,
		cdc:           cdc,
		coinKeeper:    coinKeeper,
		accountKeeper: accountKeeper,
		upgradeKeeper: upgradeKeeper,
		storeKey:      storeKey,
	}
}

func (mgr *Mgrs) GetVersion() semver.Version {
	return mgr.currentVersion
}

func (mgr *Mgrs) GetConstants() constants.ConstantValues {
	return mgr.constAccessor
}

// LoadManagerIfNecessary detect whether there are new version available, if it is available then create a new version of Mgr
func (mgr *Mgrs) LoadManagerIfNecessary(ctx cosmos.Context) error {
	v := mgr.K.GetLowestActiveVersion(ctx)
	if v.Equals(mgr.GetVersion()) {
		return nil
	}
	// version is different , thus all the manager need to re-create
	mgr.currentVersion = v
	mgr.constAccessor = constants.GetConstantValues(v)
	var err error

	mgr.K, err = GetKeeper(v, mgr.cdc, mgr.coinKeeper, mgr.accountKeeper, mgr.upgradeKeeper, mgr.storeKey)
	if err != nil {
		return fmt.Errorf("fail to create keeper: %w", err)
	}

	shouldEmitVersion := false
	storedVer, hasStoredVer := mgr.Keeper().GetVersionWithCtx(ctx)
	if !hasStoredVer || v.GT(storedVer) {
		// store the version for contextual lookups if it has been upgraded
		mgr.Keeper().SetVersionWithCtx(ctx, v)
	}

	// Managers might be new from a fresh node,
	// but the keeper should be consistent.
	shouldEmitVersion = (hasStoredVer && v.GT(storedVer))

	mgr.gasMgr, err = GetGasManager(v, mgr.K)
	if err != nil {
		return fmt.Errorf("fail to create gas manager: %w", err)
	}

	mgr.eventMgr, err = GetEventManager(v)
	if err != nil {
		return fmt.Errorf("fail to get event manager: %w", err)
	}
	// Having created the event manager, emit a version event
	// as long as there was indeed a previously-recorded version changed from.
	// (As indicated by the keeper, rather than by managers created for the first time.)
	if shouldEmitVersion {
		evt := NewEventVersion(v)
		if err := mgr.EventMgr().EmitEvent(ctx, evt); err != nil {
			ctx.Logger().Error("fail to emit version event", "error", err)
		}
	}

	mgr.txOutStore, err = GetTxOutStore(v, mgr.K, mgr.eventMgr, mgr.gasMgr)
	if err != nil {
		return fmt.Errorf("fail to get tx out store: %w", err)
	}

	mgr.networkMgr, err = GetNetworkManager(v, mgr.K, mgr.txOutStore, mgr.eventMgr)
	if err != nil {
		return fmt.Errorf("fail to get vault manager: %w", err)
	}

	mgr.poolMgr, err = GetPoolManager(v)
	if err != nil {
		return fmt.Errorf("fail to get pool manager: %w", err)
	}

	mgr.validatorMgr, err = GetValidatorManager(v, mgr.K, mgr.networkMgr, mgr.txOutStore, mgr.eventMgr)
	if err != nil {
		return fmt.Errorf("fail to get validator manager: %w", err)
	}

	mgr.obMgr, err = GetObserverManager(v)
	if err != nil {
		return fmt.Errorf("fail to get observer manager: %w", err)
	}

	mgr.swapQ, err = GetSwapQueue(v, mgr.K)
	if err != nil {
		return fmt.Errorf("fail to create swap queue: %w", err)
	}

	mgr.orderBook, err = GetOrderBook(v, mgr.K)
	if err != nil {
		return fmt.Errorf("fail to create order book: %w", err)
	}

	mgr.slasher, err = GetSlasher(v, mgr.K, mgr.eventMgr)
	if err != nil {
		return fmt.Errorf("fail to create swap queue: %w", err)
	}

	mgr.tradeManager, err = GetTradeAccountManager(v, mgr.K, mgr.eventMgr)
	if err != nil {
		return fmt.Errorf("fail to create trade manager: %w", err)
	}

	mgr.securedManager, err = GetSecuredAssetManager(v, mgr.K, mgr.eventMgr)
	if err != nil {
		return fmt.Errorf("fail to create secured manager: %w", err)
	}

	return nil
}

// Keeper return Keeper
func (mgr *Mgrs) Keeper() keeper.Keeper { return mgr.K }

// GasMgr return GasManager
func (mgr *Mgrs) GasMgr() GasManager { return mgr.gasMgr }

// EventMgr return EventMgr
func (mgr *Mgrs) EventMgr() EventManager { return mgr.eventMgr }

// TxOutStore return an TxOutStore
func (mgr *Mgrs) TxOutStore() TxOutStore { return mgr.txOutStore }

// VaultMgr return a valid NetworkManager
func (mgr *Mgrs) NetworkMgr() NetworkManager { return mgr.networkMgr }

// PoolMgr return a valid PoolManager
func (mgr *Mgrs) PoolMgr() PoolManager { return mgr.poolMgr }

// ValidatorMgr return an implementation of ValidatorManager
func (mgr *Mgrs) ValidatorMgr() ValidatorManager { return mgr.validatorMgr }

// ObMgr return an implementation of ObserverManager
func (mgr *Mgrs) ObMgr() ObserverManager { return mgr.obMgr }

// SwapQ return an implementation of SwapQueue
func (mgr *Mgrs) SwapQ() SwapQueue { return mgr.swapQ }

// OrderBookMgr
func (mgr *Mgrs) OrderBookMgr() OrderBook { return mgr.orderBook }

// Slasher return an implementation of Slasher
func (mgr *Mgrs) Slasher() Slasher { return mgr.slasher }

func (mgr *Mgrs) TradeAccountManager() TradeAccountManager { return mgr.tradeManager }

func (mgr *Mgrs) SecuredAssetManager() SecuredAssetManager { return mgr.securedManager }

// GetKeeper return Keeper
func GetKeeper(
	version semver.Version,
	cdc codec.BinaryCodec,
	coinKeeper bankkeeper.Keeper,
	accountKeeper authkeeper.AccountKeeper,
	upgradeKeeper *upgradekeeper.Keeper,
	storeKey cosmos.StoreKey,
) (keeper.Keeper, error) {
	if version.GTE(semver.MustParse("3.0.0")) {
		kvs := kv1.NewKVStore(cdc, coinKeeper, accountKeeper, upgradeKeeper, storeKey, version)
		return &kvs, nil
	}
	return nil, errInvalidVersion
}

// GetGasManager return GasManager
func GetGasManager(version semver.Version, keeper keeper.Keeper) (GasManager, error) {
	constAccessor := constants.GetConstantValues(version)
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return newGasMgrVCUR(constAccessor, keeper), nil
	default:
		return nil, errInvalidVersion
	}
}

// GetEventManager will return an implementation of EventManager
func GetEventManager(version semver.Version) (EventManager, error) {
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return newEventMgrVCUR(), nil
	default:
		return nil, errInvalidVersion
	}
}

// GetTxOutStore will return an implementation of the txout store that
func GetTxOutStore(version semver.Version, keeper keeper.Keeper, eventMgr EventManager, gasManager GasManager) (TxOutStore, error) {
	constAccessor := constants.GetConstantValues(version)
	return newTxOutStorageVCUR(keeper, constAccessor, eventMgr, gasManager), nil
}

// GetNetworkManager  retrieve a NetworkManager that is compatible with the given version
func GetNetworkManager(version semver.Version, keeper keeper.Keeper, txOutStore TxOutStore, eventMgr EventManager) (NetworkManager, error) {
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return newNetworkMgrVCUR(keeper, txOutStore, eventMgr), nil
	default:
		return nil, errInvalidVersion
	}
}

// GetValidatorManager create a new instance of Validator Manager
func GetValidatorManager(version semver.Version, keeper keeper.Keeper, networkMgr NetworkManager, txOutStore TxOutStore, eventMgr EventManager) (ValidatorManager, error) {
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return newValidatorMgrVCUR(keeper, networkMgr, txOutStore, eventMgr), nil
	default:
		return nil, errInvalidVersion
	}
}

// GetObserverManager return an instance that implements ObserverManager interface
// when there is no version can match the given semver , it will return nil
func GetObserverManager(version semver.Version) (ObserverManager, error) {
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return newObserverMgrVCUR(), nil
	default:
		return nil, errInvalidVersion
	}
}

// GetPoolManager return an implementation of PoolManager
func GetPoolManager(version semver.Version) (PoolManager, error) {
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return newPoolMgrVCUR(), nil
	default:
		return nil, errInvalidVersion
	}
}

// GetSwapQueue retrieve a SwapQueue that is compatible with the given version
func GetSwapQueue(version semver.Version, keeper keeper.Keeper) (SwapQueue, error) {
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return newSwapQueueVCUR(keeper), nil
	default:
		return nil, errInvalidVersion
	}
}

// GetOrderBook retrieve a OrderBook that is compatible with the given version
func GetOrderBook(version semver.Version, keeper keeper.Keeper) (OrderBook, error) {
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return newOrderBookVCUR(keeper), nil
	default:
		return nil, errInvalidVersion
	}
}

// GetSlasher return an implementation of Slasher
func GetSlasher(version semver.Version, keeper keeper.Keeper, eventMgr EventManager) (Slasher, error) {
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return newSlasherVCUR(keeper, eventMgr), nil
	default:
		return nil, errInvalidVersion
	}
}

// Though Swapper is not a full manager, it is recorded here for versioning convenience.
// GetSwapper return an implementation of Swapper
func GetSwapper(version semver.Version) (Swapper, error) {
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return newSwapperVCUR(), nil
	default:
		return nil, errInvalidVersion
	}
}

func GetTradeAccountManager(version semver.Version, keeper keeper.Keeper, eventMgr EventManager) (TradeAccountManager, error) {
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return newTradeMgrVCUR(keeper, eventMgr), nil
	default:
		return nil, errInvalidVersion
	}
}

func GetSecuredAssetManager(version semver.Version, keeper keeper.Keeper, eventMgr EventManager) (SecuredAssetManager, error) {
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return newSecuredAssetMgrVCUR(keeper, eventMgr), nil
	default:
		return nil, errInvalidVersion
	}
}
