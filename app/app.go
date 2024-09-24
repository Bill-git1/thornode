package app

import (
	"io"
	"os"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/grpc/tmservice"
	"github.com/cosmos/cosmos-sdk/client/rpc"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/server/api"
	"github.com/cosmos/cosmos-sdk/server/config"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/cosmos/cosmos-sdk/version"
	"github.com/cosmos/cosmos-sdk/x/auth"
	"github.com/cosmos/cosmos-sdk/x/auth/ante"
	authrest "github.com/cosmos/cosmos-sdk/x/auth/client/rest"
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/bank"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/libs/log"
	tmos "github.com/tendermint/tendermint/libs/os"
	dbm "github.com/tendermint/tm-db"

	"github.com/cosmos/cosmos-sdk/x/genutil"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	"github.com/cosmos/cosmos-sdk/x/mint"
	mintkeeper "github.com/cosmos/cosmos-sdk/x/mint/keeper"
	minttypes "github.com/cosmos/cosmos-sdk/x/mint/types"
	"github.com/cosmos/cosmos-sdk/x/params"
	paramskeeper "github.com/cosmos/cosmos-sdk/x/params/keeper"
	paramstypes "github.com/cosmos/cosmos-sdk/x/params/types"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/cosmos/cosmos-sdk/x/upgrade"
	upgradekeeper "github.com/cosmos/cosmos-sdk/x/upgrade/keeper"
	upgradetypes "github.com/cosmos/cosmos-sdk/x/upgrade/types"
	appparams "gitlab.com/thorchain/thornode/app/params"
	"gitlab.com/thorchain/thornode/x/thorchain"
	thorchainkeeper "gitlab.com/thorchain/thornode/x/thorchain/keeper"
	thorchainkeeperv1 "gitlab.com/thorchain/thornode/x/thorchain/keeper/v1"
	thorchaintypes "gitlab.com/thorchain/thornode/x/thorchain/types"
)

var (
	// DefaultNodeHome default home directories for the application daemon
	DefaultNodeHome = func() string {
		return os.ExpandEnv("$HOME/.thornode")
	}

	// ModuleBasics defines the module BasicManager is in charge of setting up basic,
	// non-dependant module elements, such as codec registration
	// and genesis verification.
	ModuleBasics = module.NewBasicManager(
		auth.AppModuleBasic{},
		genutil.AppModuleBasic{},
		bank.AppModuleBasic{},
		mint.AppModuleBasic{},
		params.AppModuleBasic{},
		upgrade.AppModuleBasic{},
		thorchain.AppModuleBasic{},
	)

	// module account permissions
	maccPerms = map[string][]string{
		authtypes.FeeCollectorName:       nil,
		minttypes.ModuleName:             {authtypes.Minter},
		stakingtypes.BondedPoolName:      {authtypes.Burner, authtypes.Staking},
		stakingtypes.NotBondedPoolName:   {authtypes.Burner, authtypes.Staking},
		thorchain.ModuleName:             {authtypes.Minter, authtypes.Burner},
		thorchain.AsgardName:             {},
		thorchain.BondName:               {},
		thorchain.ReserveName:            {},
		thorchain.LendingName:            {},
		thorchain.AffiliateCollectorName: {},
		thorchain.TreasuryName:           {},
		thorchain.RUNEPoolName:           {},
	}

	// module accounts that are allowed to receive tokens
	allowedReceivingModAcc = map[string]bool{
		thorchain.LendingName:  true,
		thorchain.TreasuryName: true,
	}
)

var (
	_ App                     = (*THORChainApp)(nil)
	_ servertypes.Application = (*THORChainApp)(nil)
)

// THORChainApp extends an ABCI application
type THORChainApp struct {
	*baseapp.BaseApp
	appName           string
	cdc               *codec.LegacyAmino
	appCodec          codec.Codec
	interfaceRegistry types.InterfaceRegistry
	invCheckPeriod    uint
	// keys to access the substores
	keys    map[string]*sdk.KVStoreKey
	tkeys   map[string]*sdk.TransientStoreKey
	memKeys map[string]*sdk.MemoryStoreKey

	// keepers
	AccountKeeper authkeeper.AccountKeeper
	BankKeeper    bankkeeper.Keeper
	StakingKeeper stakingkeeper.Keeper
	MintKeeper    mintkeeper.Keeper
	UpgradeKeeper upgradekeeper.Keeper
	ParamsKeeper  paramskeeper.Keeper

	thorchainKeeper thorchainkeeper.Keeper

	// the module manager
	mm *module.Manager
}

// New returns a reference to an initialized thornode application.
func New(
	appName string, logger log.Logger, db dbm.DB, traceStore io.Writer, loadLatest bool, skipUpgradeHeights map[int64]bool,
	homePath string, invCheckPeriod uint, encodingConfig appparams.EncodingConfig, telemetryEnabled bool, baseAppOptions ...func(*baseapp.BaseApp),
) *THORChainApp {
	appCodec := encodingConfig.Marshaler
	cdc := encodingConfig.Amino
	interfaceRegistry := encodingConfig.InterfaceRegistry

	bApp := baseapp.NewBaseApp(appName, logger, db, encodingConfig.TxConfig.TxDecoder(), baseAppOptions...)
	bApp.SetCommitMultiStoreTracer(traceStore)
	bApp.SetVersion(version.Version)
	bApp.SetInterfaceRegistry(interfaceRegistry)
	// bApp.GRPCQueryRouter().RegisterSimulateService(bApp.Simulate, interfaceRegistry)

	keys := sdk.NewKVStoreKeys(
		authtypes.StoreKey, banktypes.StoreKey, stakingtypes.StoreKey,
		minttypes.StoreKey,
		upgradetypes.StoreKey,
		paramstypes.StoreKey,
		thorchaintypes.StoreKey,
	)
	tkeys := sdk.NewTransientStoreKeys(paramstypes.TStoreKey)
	memKeys := sdk.NewMemoryStoreKeys()

	app := &THORChainApp{
		BaseApp:           bApp,
		appName:           appName,
		cdc:               cdc,
		appCodec:          appCodec,
		interfaceRegistry: interfaceRegistry,
		invCheckPeriod:    invCheckPeriod,
		keys:              keys,
		tkeys:             tkeys,
		memKeys:           memKeys,
	}

	app.ParamsKeeper = initParamsKeeper(appCodec, cdc, keys[paramstypes.StoreKey], tkeys[paramstypes.TStoreKey])

	// set the BaseApp's parameter store
	bApp.SetParamStore(app.ParamsKeeper.Subspace(baseapp.Paramspace).WithKeyTable(paramskeeper.ConsensusParamsKeyTable()))

	// add keepers
	app.AccountKeeper = authkeeper.NewAccountKeeper(
		appCodec, keys[authtypes.StoreKey], app.GetSubspace(authtypes.ModuleName), authtypes.ProtoBaseAccount, maccPerms,
	)
	app.BankKeeper = bankkeeper.NewBaseKeeper(
		appCodec, keys[banktypes.StoreKey], app.AccountKeeper, app.GetSubspace(banktypes.ModuleName), app.BlockedAddrs(),
	)

	stakingKeeper := stakingkeeper.NewKeeper(
		appCodec, keys[stakingtypes.StoreKey], app.AccountKeeper, app.BankKeeper, app.GetSubspace(stakingtypes.ModuleName),
	)

	app.MintKeeper = mintkeeper.NewKeeper(
		appCodec, keys[minttypes.StoreKey], app.GetSubspace(minttypes.ModuleName), &stakingKeeper,
		app.AccountKeeper, app.BankKeeper, authtypes.FeeCollectorName,
	)

	app.UpgradeKeeper = upgradekeeper.NewKeeper(skipUpgradeHeights, keys[upgradetypes.StoreKey], appCodec, homePath, app.BaseApp)

	app.thorchainKeeper = thorchainkeeperv1.NewKeeper(
		appCodec, app.BankKeeper, app.AccountKeeper, app.UpgradeKeeper, keys[thorchaintypes.StoreKey],
	)

	// NOTE: Any module instantiated in the module manager that is later modified
	// must be passed by reference here.

	app.mm = module.NewManager(
		genutil.NewAppModule(
			app.AccountKeeper, app.StakingKeeper, app.BaseApp.DeliverTx,
			encodingConfig.TxConfig,
		),
		auth.NewAppModule(appCodec, app.AccountKeeper, nil),
		bank.NewAppModule(appCodec, app.BankKeeper, app.AccountKeeper),
		upgrade.NewAppModule(app.UpgradeKeeper),
		params.NewAppModule(app.ParamsKeeper),
		thorchain.NewAppModule(
			app.thorchainKeeper,
			appCodec,
			app.BankKeeper,
			app.AccountKeeper,
			app.UpgradeKeeper,
			keys[thorchaintypes.StoreKey],
			telemetryEnabled,
		),
	)

	app.mm.SetOrderBeginBlockers(
		upgradetypes.ModuleName, // NOTE: upgrade module must be first
		genutiltypes.ModuleName,
		authtypes.ModuleName,
		banktypes.ModuleName,
		paramstypes.ModuleName,
		thorchain.ModuleName,
	)

	// NOTE: The genutils module must occur after staking so that pools are
	// properly initialized with tokens from genesis accounts.
	app.mm.SetOrderInitGenesis(
		authtypes.ModuleName,
		banktypes.ModuleName,
		paramstypes.ModuleName,
		genutiltypes.ModuleName,
		upgradetypes.ModuleName,
		thorchain.ModuleName,
	)

	app.mm.RegisterRoutes(app.Router(), app.QueryRouter(), encodingConfig.Amino)
	app.mm.RegisterServices(module.NewConfigurator(app.appCodec, app.MsgServiceRouter(), app.GRPCQueryRouter()))

	// initialize stores
	app.MountKVStores(keys)
	app.MountTransientStores(tkeys)
	app.MountMemoryStores(memKeys)

	// initialize BaseApp
	app.SetInitChainer(app.InitChainer)
	app.SetBeginBlocker(app.BeginBlocker)

	anteHandler, err := NewAnteHandler(AnteHandlerOptions{
		AccountKeeper:   app.AccountKeeper,
		BankKeeper:      app.BankKeeper,
		SigGasConsumer:  ante.DefaultSigVerificationGasConsumer,
		SignModeHandler: encodingConfig.TxConfig.SignModeHandler(),
		THORChainKeeper: app.thorchainKeeper,
	})
	if err != nil {
		tmos.Exit(err.Error())
	}

	app.SetAnteHandler(anteHandler)
	app.SetEndBlocker(app.EndBlocker)

	if loadLatest {
		if err = app.LoadLatestVersion(); err != nil {
			tmos.Exit(err.Error())
		}
	}

	return app
}

// Name returns the name of the App
func (app *THORChainApp) Name() string { return app.BaseApp.Name() }

// InitChainer application update at chain initialization
func (app *THORChainApp) InitChainer(ctx sdk.Context, req abci.RequestInitChain) abci.ResponseInitChain {
	var genesisState GenesisState
	app.cdc.MustUnmarshalJSON(req.AppStateBytes, &genesisState)
	return app.mm.InitGenesis(ctx, app.appCodec, genesisState)
}

// LoadHeight loads a particular height
func (app *THORChainApp) LoadHeight(height int64) error {
	return app.LoadVersion(height)
}

// ModuleAccountAddrs returns all the app's module account addresses.
func (app *THORChainApp) ModuleAccountAddrs() map[string]bool {
	modAccAddrs := make(map[string]bool)
	for acc := range maccPerms {
		modAccAddrs[authtypes.NewModuleAddress(acc).String()] = true
	}

	return modAccAddrs
}

// BlockedAddrs returns all the app's module account addresses that are not
// allowed to receive external tokens.
func (app *THORChainApp) BlockedAddrs() map[string]bool {
	blockedAddrs := make(map[string]bool)
	for acc := range maccPerms {
		blockedAddrs[authtypes.NewModuleAddress(acc).String()] = !allowedReceivingModAcc[acc]
	}

	return blockedAddrs
}

// LegacyAmino returns the app's amino codec.
//
// NOTE: This is solely to be used for testing purposes as it may be desirable
// for modules to register their own custom testing types.
func (app *THORChainApp) LegacyAmino() *codec.LegacyAmino {
	return app.cdc
}

// AppCodec returns app codec.
//
// NOTE: This is solely to be used for testing purposes as it may be desirable
// for modules to register their own custom testing types.
func (app *THORChainApp) AppCodec() codec.Codec {
	return app.appCodec
}

// InterfaceRegistry returns InterfaceRegistry
func (app *THORChainApp) InterfaceRegistry() types.InterfaceRegistry {
	return app.interfaceRegistry
}

// GetKey returns the KVStoreKey for the provided store key.
//
// NOTE: This is solely to be used for testing purposes.
func (app *THORChainApp) GetKey(storeKey string) *sdk.KVStoreKey {
	return app.keys[storeKey]
}

// GetTKey returns the TransientStoreKey for the provided store key.
//
// NOTE: This is solely to be used for testing purposes.
func (app *THORChainApp) GetTKey(storeKey string) *sdk.TransientStoreKey {
	return app.tkeys[storeKey]
}

// GetMemKey returns the MemStoreKey for the provided mem key.
//
// NOTE: This is solely used for testing purposes.
func (app *THORChainApp) GetMemKey(storeKey string) *sdk.MemoryStoreKey {
	return app.memKeys[storeKey]
}

// GetSubspace returns a param subspace for a given module name.
//
// NOTE: This is solely to be used for testing purposes.
func (app *THORChainApp) GetSubspace(moduleName string) paramstypes.Subspace {
	subspace, _ := app.ParamsKeeper.GetSubspace(moduleName)
	return subspace
}

// RegisterAPIRoutes registers all application module routes with the provided
// API server.
func (app *THORChainApp) RegisterAPIRoutes(apiSvr *api.Server, apiConfig config.APIConfig) {
	clientCtx := apiSvr.ClientCtx
	rpc.RegisterRoutes(clientCtx, apiSvr.Router)
	authrest.RegisterTxRoutes(clientCtx, apiSvr.Router)

	// Register new tx routes from grpc-gateway.
	authtx.RegisterGRPCGatewayRoutes(clientCtx, apiSvr.GRPCGatewayRouter)
	// Register new tendermint queries routes from grpc-gateway.
	tmservice.RegisterGRPCGatewayRoutes(clientCtx, apiSvr.GRPCGatewayRouter)

	// Register legacy and grpc-gateway routes for all modules.
	ModuleBasics.RegisterRESTRoutes(clientCtx, apiSvr.Router)
	ModuleBasics.RegisterGRPCGatewayRoutes(clientCtx, apiSvr.GRPCGatewayRouter)
}

// RegisterTendermintService implements the Application.RegisterTendermintService method.
func (app *THORChainApp) RegisterTendermintService(ctx client.Context) {
	tmservice.RegisterTendermintService(app.BaseApp.GRPCQueryRouter(), ctx, app.interfaceRegistry)
}

// RegisterTxService implements the Application.RegisterTxService method.
func (app *THORChainApp) RegisterTxService(ctx client.Context) {
	authtx.RegisterTxService(app.BaseApp.GRPCQueryRouter(), ctx, app.BaseApp.Simulate, app.interfaceRegistry)
}

// initParamsKeeper init params keeper and its subspaces
func initParamsKeeper(appCodec codec.BinaryCodec, legacyAmino *codec.LegacyAmino, key, tkey sdk.StoreKey) paramskeeper.Keeper {
	paramsKeeper := paramskeeper.NewKeeper(appCodec, legacyAmino, key, tkey)

	paramsKeeper.Subspace(authtypes.ModuleName)
	paramsKeeper.Subspace(banktypes.ModuleName)
	paramsKeeper.Subspace(stakingtypes.ModuleName)
	paramsKeeper.Subspace(minttypes.ModuleName)

	return paramsKeeper
}
