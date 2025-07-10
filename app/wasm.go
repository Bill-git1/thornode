package app

import (
	"context"

	"github.com/CosmWasm/wasmd/x/wasm"
	wasmkeeper "github.com/CosmWasm/wasmd/x/wasm/keeper"
	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
	wasmvmtypes "github.com/CosmWasm/wasmvm/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	apitypes "gitlab.com/thorchain/thornode/v3/api/types"
)

var wasmAcceptedQueries = wasmkeeper.AcceptedQueries{
	"/types.Query/Network":           &apitypes.QueryNetworkResponse{},
	"/types.Query/Borrower":          &apitypes.QueryBorrowerResponse{},
	"/types.Query/LiquidityProvider": &apitypes.QueryLiquidityProviderResponse{},
	"/types.Query/Node":              &apitypes.QueryNodeResponse{},
	"/types.Query/OutboundFee":       &apitypes.QueryOutboundFeeResponse{},
	"/types.Query/Pool":              &apitypes.QueryPoolResponse{},
	"/types.Query/QuoteSwap":         &apitypes.QueryQuoteSwapResponse{},
	"/types.Query/SecuredAsset":      &apitypes.QuerySecuredAssetResponse{},
}

// Support slightly larger wasm files
var WasmMaxSize = 2_624_000

var WasmGasRegister = wasmtypes.NewWasmGasRegister(wasmtypes.WasmGasRegisterConfig{
	InstanceCost:               wasmtypes.DefaultInstanceCost,
	InstanceCostDiscount:       wasmtypes.DefaultInstanceCostDiscount,
	CompileCost:                wasmtypes.DefaultCompileCost * 100,
	GasMultiplier:              wasmtypes.DefaultGasMultiplier,
	EventPerAttributeCost:      wasmtypes.DefaultPerAttributeCost,
	CustomEventCost:            wasmtypes.DefaultPerCustomEventCost,
	EventAttributeDataCost:     wasmtypes.DefaultEventAttributeDataCost,
	EventAttributeDataFreeTier: wasmtypes.DefaultEventAttributeDataFreeTier,
	ContractMessageDataCost:    wasmtypes.DefaultContractMessageDataCost,
	// Wasm compile/store cost 100x due to auto-pinning
	// Contracts are stored in memory my default, rather than on disk
	// Standard contract ~ 20m gas to store, vs 50-200k for a regular contract execution
	UncompressCost: wasmvmtypes.UFraction{Numerator: 15, Denominator: 1},
})

// CustomWasmModule re-exposes the underlying module's methods,
// but prevents Services from being registered, as these
// should be registered and handled in x/thorchain
type CustomWasmModule struct {
	*wasm.AppModule
}

// NewCustomWasmModule creates a new CustomWasmModule object
func NewCustomWasmModule(
	module *wasm.AppModule,
) CustomWasmModule {
	return CustomWasmModule{module}
}

func (am CustomWasmModule) RegisterServices(cfg module.Configurator) {
}

// x/wasm uses bankkeeper.IsSendEnabledCoins to check the movement of
// funds when instantiating, executing, sudoing, and executing submsgs
// WasmBankKeeper bypasses the IsSendEnabledCoins check to allow funds
// to be transferred for these actions, without affecting the behaviour
// of x/bank MsgSend
type WasmBankKeeper struct {
	wasmtypes.BankKeeper
}

func NewWasmBankKeeper(keeper wasmtypes.BankKeeper) WasmBankKeeper {
	return WasmBankKeeper{keeper}
}

func (c WasmBankKeeper) IsSendEnabledCoins(ctx context.Context, coins ...sdk.Coin) error {
	return nil
}
