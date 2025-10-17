package keeper_test

import (
	"context"
	"testing"

	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/suite"
	"gitlab.com/thorchain/thornode/v3/x/scheduler/keeper"
	"gitlab.com/thorchain/thornode/v3/x/scheduler/types"
)

var (
	authAcc  = sdk.AccAddress([]byte("auth1_______________"))
	accAddrs = []sdk.AccAddress{
		sdk.AccAddress([]byte("addr1_______________")),
		sdk.AccAddress([]byte("addr2_______________")),
	}
)

type KeeperTestSuite struct {
	suite.Suite

	ctx    context.Context
	keeper keeper.Keeper

	queryClient types.QueryClient
	msgServer   types.MsgServer

	encCfg moduletestutil.TestEncodingConfig
}

func TestKeeperTestSuite(t *testing.T) {
	suite.Run(t, new(KeeperTestSuite))
}

func (suite *KeeperTestSuite) SetupTest() {
	keys := storetypes.NewKVStoreKeys(authtypes.StoreKey, banktypes.StoreKey, types.StoreKey)
	testCtx := testutil.DefaultContextWithKeys(
		keys,
		storetypes.NewTransientStoreKeys(),
		storetypes.NewMemoryStoreKeys(),
	)
	wasmkeeper := &MockWasmKeeper{}
	encCfg := moduletestutil.MakeTestEncodingConfig()
	suite.ctx = testCtx
	suite.keeper = keeper.NewKeeper(
		encCfg.Codec,
		runtime.NewKVStoreService(keys[types.StoreKey]),
		wasmkeeper,
		authAcc.String(),
	)

	authtypes.RegisterInterfaces(encCfg.InterfaceRegistry)
	types.RegisterInterfaces(encCfg.InterfaceRegistry)
	queryHelper := baseapp.NewQueryServerTestHelper(testCtx, encCfg.InterfaceRegistry)
	types.RegisterQueryServer(queryHelper, suite.keeper)

	suite.queryClient = types.NewQueryClient(queryHelper)
	suite.msgServer = keeper.NewMsgServerImpl(suite.keeper)
	suite.encCfg = encCfg
}

var _ types.WasmKeeper = MockWasmKeeper{}

type MockWasmKeeper struct{}

// Execute implements types.WasmKeeper.
func (m MockWasmKeeper) Execute(ctx sdk.Context, contractAddress, caller sdk.AccAddress, msg []byte, coins sdk.Coins) ([]byte, error) {
	return nil, nil
}
