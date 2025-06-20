package thorchain

import (
	"github.com/blang/semver"
	"gitlab.com/thorchain/thornode/v3/constants"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/keeper"
)

type DummyMgr struct {
	K             keeper.Keeper
	constAccessor constants.ConstantValues
	gasMgr        GasManager
	eventMgr      EventManager
	txOutStore    TxOutStore
	networkMgr    NetworkManager
	validatorMgr  ValidatorManager
	obMgr         ObserverManager
	poolMgr       PoolManager
	swapQ         SwapQueue
	advSwapQueue  AdvSwapQueue
	slasher       Slasher
	tradeMgr      TradeAccountManager
	securedMgr    SecuredAssetManager
	wasmMgr       WasmManager
	switchMgr     SwitchManager
}

func NewDummyMgrWithKeeper(k keeper.Keeper) *DummyMgr {
	return &DummyMgr{
		K:             k,
		constAccessor: constants.GetConstantValues(GetCurrentVersion()),
		gasMgr:        NewDummyGasManager(),
		eventMgr:      NewDummyEventMgr(),
		txOutStore:    NewTxStoreDummy(),
		networkMgr:    NewNetworkMgrDummy(),
		validatorMgr:  NewValidatorDummyMgr(),
		obMgr:         NewDummyObserverManager(),
		poolMgr:       NewDummyPoolManager(),
		slasher:       NewDummySlasher(),
		tradeMgr:      NewDummyTradeAccountManager(),
		wasmMgr:       NewDummyWasmManager(),
		switchMgr:     NewDummySwitchManager(),
		// TODO add dummy advanced swap queue
	}
}

func NewDummyMgr() *DummyMgr {
	return &DummyMgr{
		K:             keeper.KVStoreDummy{},
		constAccessor: constants.GetConstantValues(GetCurrentVersion()),
		gasMgr:        NewDummyGasManager(),
		eventMgr:      NewDummyEventMgr(),
		txOutStore:    NewTxStoreDummy(),
		networkMgr:    NewNetworkMgrDummy(),
		validatorMgr:  NewValidatorDummyMgr(),
		obMgr:         NewDummyObserverManager(),
		poolMgr:       NewDummyPoolManager(),
		slasher:       NewDummySlasher(),
		tradeMgr:      NewDummyTradeAccountManager(),
		wasmMgr:       NewDummyWasmManager(),
		switchMgr:     NewDummySwitchManager(),
		// TODO add dummy advanced swap queue
	}
}

func (m DummyMgr) GetVersion() semver.Version               { return GetCurrentVersion() }
func (m DummyMgr) GetConstants() constants.ConstantValues   { return m.constAccessor }
func (m DummyMgr) Keeper() keeper.Keeper                    { return m.K }
func (m DummyMgr) GasMgr() GasManager                       { return m.gasMgr }
func (m DummyMgr) EventMgr() EventManager                   { return m.eventMgr }
func (m DummyMgr) TxOutStore() TxOutStore                   { return m.txOutStore }
func (m DummyMgr) NetworkMgr() NetworkManager               { return m.networkMgr }
func (m DummyMgr) ValidatorMgr() ValidatorManager           { return m.validatorMgr }
func (m DummyMgr) ObMgr() ObserverManager                   { return m.obMgr }
func (m DummyMgr) PoolMgr() PoolManager                     { return m.poolMgr }
func (m DummyMgr) SwapQ() SwapQueue                         { return m.swapQ }
func (m DummyMgr) Slasher() Slasher                         { return m.slasher }
func (m DummyMgr) AdvSwapQueueMgr() AdvSwapQueue            { return m.advSwapQueue }
func (m DummyMgr) TradeAccountManager() TradeAccountManager { return m.tradeMgr }
func (m DummyMgr) SecuredAssetManager() SecuredAssetManager { return m.securedMgr }
func (m DummyMgr) WasmManager() WasmManager                 { return m.wasmMgr }
func (m DummyMgr) SwitchManager() SwitchManager             { return m.switchMgr }
