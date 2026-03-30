package thorchain

import (
	"fmt"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	. "gopkg.in/check.v1"
)

type HandlerWasmExecSuite struct{}

var _ = Suite(&HandlerWasmExecSuite{})

func (s *HandlerWasmExecSuite) SetUpSuite(c *C) {
	SetupConfigForTest()
}

func (s *HandlerWasmExecSuite) TestWasmExecHandler_InvalidMessage(c *C) {
	ctx, mgr := setupManagerForTest(c)
	h := NewWasmExecHandler(mgr)

	// Passing wrong message type should fail
	result, err := h.Run(ctx, &MsgMimir{})
	c.Assert(err, NotNil)
	c.Assert(result, IsNil)
	c.Assert(err, Equals, errInvalidMessage)
}

func (s *HandlerWasmExecSuite) TestWasmExecHandler_ValidateBasicFails(c *C) {
	ctx, mgr := setupManagerForTest(c)
	h := NewWasmExecHandler(mgr)

	// Empty asset should fail validation
	msg := &MsgWasmExec{
		Asset:    common.EmptyAsset,
		Amount:   cosmos.NewUint(100),
		Contract: GetRandomBech32Addr(),
		Sender:   GetRandomBech32Addr(),
		Signer:   GetRandomBech32Addr(),
		Msg:      []byte(`{"action":"test"}`),
		Tx:       common.Tx{ID: GetRandomTxHash()},
	}
	result, err := h.Run(ctx, msg)
	c.Assert(err, NotNil)
	c.Assert(result, IsNil)

	// Zero amount should fail validation
	msg2 := NewMsgWasmExec(
		common.BTCAsset,
		cosmos.ZeroUint(),
		GetRandomBech32Addr(),
		GetRandomBech32Addr(),
		GetRandomBech32Addr(),
		[]byte(`{"action":"test"}`),
		common.Tx{ID: GetRandomTxHash()},
	)
	result2, err2 := h.Run(ctx, msg2)
	c.Assert(err2, NotNil)
	c.Assert(result2, IsNil)

	// Empty contract address should fail
	msg3 := NewMsgWasmExec(
		common.BTCAsset,
		cosmos.NewUint(100),
		cosmos.AccAddress{},
		GetRandomBech32Addr(),
		GetRandomBech32Addr(),
		[]byte(`{"action":"test"}`),
		common.Tx{ID: GetRandomTxHash()},
	)
	result3, err3 := h.Run(ctx, msg3)
	c.Assert(err3, NotNil)
	c.Assert(result3, IsNil)

	// Empty sender should fail
	msg4 := NewMsgWasmExec(
		common.BTCAsset,
		cosmos.NewUint(100),
		GetRandomBech32Addr(),
		cosmos.AccAddress{},
		GetRandomBech32Addr(),
		[]byte(`{"action":"test"}`),
		common.Tx{ID: GetRandomTxHash()},
	)
	result4, err4 := h.Run(ctx, msg4)
	c.Assert(err4, NotNil)
	c.Assert(result4, IsNil)

	// Empty signer should fail
	msg5 := NewMsgWasmExec(
		common.BTCAsset,
		cosmos.NewUint(100),
		GetRandomBech32Addr(),
		GetRandomBech32Addr(),
		cosmos.AccAddress{},
		[]byte(`{"action":"test"}`),
		common.Tx{ID: GetRandomTxHash()},
	)
	result5, err5 := h.Run(ctx, msg5)
	c.Assert(err5, NotNil)
	c.Assert(result5, IsNil)

	// Empty tx ID should fail
	msg6 := NewMsgWasmExec(
		common.BTCAsset,
		cosmos.NewUint(100),
		GetRandomBech32Addr(),
		GetRandomBech32Addr(),
		GetRandomBech32Addr(),
		[]byte(`{"action":"test"}`),
		common.Tx{},
	)
	result6, err6 := h.Run(ctx, msg6)
	c.Assert(err6, NotNil)
	c.Assert(result6, IsNil)
}

func (s *HandlerWasmExecSuite) TestWasmExecHandler_ChainHalted(c *C) {
	ctx, mgr := setupManagerForTest(c)
	h := NewWasmExecHandler(mgr)

	// Halt THORChain (key format is "Halt%sChain" where chain is "THOR")
	mgr.Keeper().SetMimir(ctx, "HaltTHORChain", 1)

	msg := NewMsgWasmExec(
		common.BTCAsset,
		cosmos.NewUint(100),
		GetRandomBech32Addr(),
		GetRandomBech32Addr(),
		GetRandomBech32Addr(),
		[]byte(`{"action":"test"}`),
		common.Tx{ID: GetRandomTxHash()},
	)
	result, err := h.Run(ctx, msg)
	c.Assert(err, NotNil)
	c.Assert(result, IsNil)
	c.Assert(err.Error(), Equals, "unable to use MsgWasmExec while THORChain is halted")
}

func (s *HandlerWasmExecSuite) TestWasmExecHandler_SecuredAsset(c *C) {
	ctx, mgr := setupManagerForTest(c)

	// Replace real wasm manager with dummy (real one needs a deployed contract)
	mgr.wasmManager = NewDummyWasmManager()

	h := NewWasmExecHandler(mgr)

	// Use a secured asset - this path skips the SecuredAssetManager.Deposit call
	// and directly creates the coin
	securedAsset := common.BTCAsset.GetSecuredAsset()

	msg := NewMsgWasmExec(
		securedAsset,
		cosmos.NewUint(100),
		GetRandomBech32Addr(),
		GetRandomBech32Addr(),
		GetRandomBech32Addr(),
		[]byte(`{"action":"test"}`),
		common.Tx{ID: GetRandomTxHash()},
	)

	result, err := h.Run(ctx, msg)
	c.Assert(err, IsNil)
	c.Assert(result, NotNil)
}

func (s *HandlerWasmExecSuite) TestWasmExecHandler_NonSecuredAsset_DepositFails(c *C) {
	ctx, mgr := setupManagerForTest(c)
	h := NewWasmExecHandler(mgr)

	// Use a non-secured, non-native asset (e.g. BTC)
	// This will call SecuredAssetManager().Deposit() which requires a pool
	// Without a pool set up, the deposit should fail
	msg := NewMsgWasmExec(
		common.BTCAsset,
		cosmos.NewUint(100),
		GetRandomBech32Addr(),
		GetRandomBech32Addr(),
		GetRandomBech32Addr(),
		[]byte(`{"action":"test"}`),
		common.Tx{
			ID:          GetRandomTxHash(),
			FromAddress: GetRandomBTCAddress(),
		},
	)

	result, err := h.Run(ctx, msg)
	c.Assert(err, NotNil)
	c.Assert(result, IsNil)
}

func (s *HandlerWasmExecSuite) TestWasmExecHandler_NonSecuredAsset_DepositSuccess(c *C) {
	ctx, mgr := setupManagerForTest(c)

	// Replace real wasm manager with dummy (real one needs a deployed contract)
	mgr.wasmManager = NewDummyWasmManager()

	h := NewWasmExecHandler(mgr)

	// Set up a BTC pool so SecuredAssetManager.Deposit can succeed
	btcPool := NewPool()
	btcPool.Asset = common.BTCAsset
	btcPool.BalanceAsset = cosmos.NewUint(1000 * common.One)
	btcPool.BalanceRune = cosmos.NewUint(10000 * common.One)
	btcPool.Status = PoolAvailable
	c.Assert(mgr.Keeper().SetPool(ctx, btcPool), IsNil)

	msg := NewMsgWasmExec(
		common.BTCAsset,
		cosmos.NewUint(100),
		GetRandomBech32Addr(),
		GetRandomBech32Addr(),
		GetRandomBech32Addr(),
		[]byte(`{"action":"test"}`),
		common.Tx{
			ID:          GetRandomTxHash(),
			FromAddress: GetRandomBTCAddress(),
		},
	)

	result, err := h.Run(ctx, msg)
	c.Assert(err, IsNil)
	c.Assert(result, NotNil)
}

func (s *HandlerWasmExecSuite) TestWasmExecHandler_ExecuteContractFails(c *C) {
	ctx, mgr := setupManagerForTest(c)

	// Replace WasmManager with one that returns an error on ExecuteContract
	mgr.wasmManager = &failingWasmManager{}

	h := NewWasmExecHandler(mgr)

	securedAsset := common.BTCAsset.GetSecuredAsset()
	msg := NewMsgWasmExec(
		securedAsset,
		cosmos.NewUint(100),
		GetRandomBech32Addr(),
		GetRandomBech32Addr(),
		GetRandomBech32Addr(),
		[]byte(`{"action":"test"}`),
		common.Tx{ID: GetRandomTxHash()},
	)

	result, err := h.Run(ctx, msg)
	c.Assert(err, NotNil)
	c.Assert(result, IsNil)
	c.Assert(err.Error(), Equals, "execute contract failed")
}

// failingWasmManager is a test double that returns errors on ExecuteContract
type failingWasmManager struct {
	DummyWasmManager
}

func (f *failingWasmManager) ExecuteContract(ctx cosmos.Context,
	contractAddr, senderAddr cosmos.AccAddress,
	msg []byte,
	coins cosmos.Coins,
) ([]byte, error) {
	return nil, fmt.Errorf("execute contract failed")
}

func (s *HandlerWasmExecSuite) TestWasmExecHandler_THORChainAssetRejected(c *C) {
	ctx, mgr := setupManagerForTest(c)
	h := NewWasmExecHandler(mgr)

	// THORChain native asset (RUNE) that is not a secured asset should be rejected
	// by ValidateBasic: "asset cannot be THORChain asset"
	msg := NewMsgWasmExec(
		common.RuneAsset(),
		cosmos.NewUint(100),
		GetRandomBech32Addr(),
		GetRandomBech32Addr(),
		GetRandomBech32Addr(),
		[]byte(`{"action":"test"}`),
		common.Tx{ID: GetRandomTxHash()},
	)

	result, err := h.Run(ctx, msg)
	c.Assert(err, NotNil)
	c.Assert(result, IsNil)
}
