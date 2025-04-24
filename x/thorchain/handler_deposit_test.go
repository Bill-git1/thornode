package thorchain

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	tmtypes "github.com/cometbft/cometbft/types"
	se "github.com/cosmos/cosmos-sdk/types/errors"
	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/keeper"
)

type HandlerDepositSuite struct{}

var _ = Suite(&HandlerDepositSuite{})

func (s *HandlerDepositSuite) TestValidate(c *C) {
	ctx, k := setupKeeperForTest(c)

	addr := GetRandomBech32Addr()

	coins := common.Coins{
		common.NewCoin(common.RuneNative, cosmos.NewUint(200*common.One)),
	}
	msg := NewMsgDeposit(coins, fmt.Sprintf("ADD:DOGE.DOGE:%s", GetRandomRUNEAddress()), addr)

	handler := NewDepositHandler(NewDummyMgrWithKeeper(k))
	err := handler.validate(ctx, *msg)
	c.Assert(err, IsNil)

	// invalid msg
	msg = &MsgDeposit{}
	err = handler.validate(ctx, *msg)
	c.Assert(err, NotNil)
}

func (s *HandlerDepositSuite) TestHandle(c *C) {
	ctx, k := setupKeeperForTest(c)
	activeNode := GetRandomValidatorNode(NodeActive)
	c.Assert(k.SetNodeAccount(ctx, activeNode), IsNil)
	dummyMgr := NewDummyMgrWithKeeper(k)
	handler := NewDepositHandler(dummyMgr)

	addr := GetRandomBech32Addr()

	coins := common.Coins{
		common.NewCoin(common.RuneNative, cosmos.NewUint(200*common.One)),
	}

	FundAccount(c, ctx, k, addr, 300*common.One)
	pool := NewPool()
	pool.Asset = common.DOGEAsset
	pool.BalanceAsset = cosmos.NewUint(100 * common.One)
	pool.BalanceRune = cosmos.NewUint(100 * common.One)
	pool.Status = PoolAvailable
	c.Assert(k.SetPool(ctx, pool), IsNil)
	msg := NewMsgDeposit(coins, "ADD:DOGE.DOGE", addr)

	_, err := handler.handle(ctx, *msg)
	c.Assert(err, IsNil)
	// ensure observe tx had been saved
	hash := tmtypes.Tx(ctx.TxBytes()).Hash()
	txID, err := common.NewTxID(fmt.Sprintf("%X", hash))
	c.Assert(err, IsNil)
	voter, err := k.GetObservedTxInVoter(ctx, txID)
	c.Assert(err, IsNil)
	c.Assert(voter.Tx.IsEmpty(), Equals, false)
	c.Assert(voter.Tx.Status, Equals, common.Status_done)

	FundAccount(c, ctx, k, addr, 300*common.One)
	// do it again, make sure the transaction get rejected
	_, err = handler.handle(ctx, *msg)
	c.Assert(err, NotNil)
}

type HandlerDepositTestHelper struct {
	keeper.Keeper
}

func NewHandlerDepositTestHelper(k keeper.Keeper) *HandlerDepositTestHelper {
	return &HandlerDepositTestHelper{
		Keeper: k,
	}
}

func (s *HandlerDepositSuite) TestDifferentValidation(c *C) {
	acctAddr := GetRandomBech32Addr()
	badAsset := common.Asset{
		Chain:  common.THORChain,
		Symbol: "ETH~ETH",
	}
	testCases := []struct {
		name            string
		messageProvider func(c *C, ctx cosmos.Context, helper *HandlerDepositTestHelper) cosmos.Msg
		validator       func(c *C, ctx cosmos.Context, result *cosmos.Result, err error, helper *HandlerDepositTestHelper, name string)
	}{
		{
			name: "invalid message should result an error",
			messageProvider: func(c *C, ctx cosmos.Context, helper *HandlerDepositTestHelper) cosmos.Msg {
				return NewMsgNetworkFee(ctx.BlockHeight(), common.DOGEChain, 1, 10000, GetRandomBech32Addr())
			},
			validator: func(c *C, ctx cosmos.Context, result *cosmos.Result, err error, helper *HandlerDepositTestHelper, name string) {
				c.Check(err, NotNil, Commentf(name))
				c.Check(result, IsNil, Commentf(name))
				c.Check(errors.Is(err, errInvalidMessage), Equals, true, Commentf(name))
			},
		},
		{
			name: "coin is not on THORChain should result in an error",
			messageProvider: func(c *C, ctx cosmos.Context, helper *HandlerDepositTestHelper) cosmos.Msg {
				return NewMsgDeposit(common.Coins{
					common.NewCoin(common.DOGEAsset, cosmos.NewUint(100)),
				}, "hello", GetRandomBech32Addr())
			},
			validator: func(c *C, ctx cosmos.Context, result *cosmos.Result, err error, helper *HandlerDepositTestHelper, name string) {
				c.Check(err, NotNil, Commentf(name))
				c.Check(result, IsNil, Commentf(name))
			},
		},
		{
			name: "invalid coin should result in error",
			messageProvider: func(c *C, ctx cosmos.Context, helper *HandlerDepositTestHelper) cosmos.Msg {
				return NewMsgDeposit(common.Coins{
					common.NewCoin(badAsset, cosmos.NewUint(100)),
				}, "hello", GetRandomBech32Addr())
			},
			validator: func(c *C, ctx cosmos.Context, result *cosmos.Result, err error, helper *HandlerDepositTestHelper, name string) {
				c.Check(err, NotNil, Commentf(name))
				c.Check(strings.Contains(err.Error(), "invalid coin"), Equals, true, Commentf(name))
			},
		},
		{
			name: "Insufficient funds should result in an error",
			messageProvider: func(c *C, ctx cosmos.Context, helper *HandlerDepositTestHelper) cosmos.Msg {
				return NewMsgDeposit(common.Coins{
					common.NewCoin(common.RuneNative, cosmos.NewUint(100)),
				}, "hello", GetRandomBech32Addr())
			},
			validator: func(c *C, ctx cosmos.Context, result *cosmos.Result, err error, helper *HandlerDepositTestHelper, name string) {
				c.Check(err, NotNil, Commentf(name))
				c.Check(result, IsNil, Commentf(name))
				c.Check(err, Equals, se.ErrInsufficientFunds, Commentf(name))
			},
		},
		{
			name: "invalid memo should err",
			messageProvider: func(c *C, ctx cosmos.Context, helper *HandlerDepositTestHelper) cosmos.Msg {
				FundAccount(c, ctx, helper.Keeper, acctAddr, 100*common.One)
				vault := NewVault(ctx.BlockHeight(), ActiveVault, AsgardVault, GetRandomPubKey(), common.Chains{common.DOGEChain, common.THORChain}.Strings(), []ChainContract{})
				c.Check(helper.Keeper.SetVault(ctx, vault), IsNil)
				return NewMsgDeposit(common.Coins{
					common.NewCoin(common.RuneNative, cosmos.NewUint(2*common.One)),
				}, "hello", acctAddr)
			},
			validator: func(c *C, ctx cosmos.Context, result *cosmos.Result, err error, helper *HandlerDepositTestHelper, name string) {
				c.Check(err, NotNil, Commentf(name))
				c.Check(result, IsNil, Commentf(name))
				c.Check(strings.Contains(err.Error(), "invalid tx type: hello"), Equals, true)
			},
		},
	}
	for _, tc := range testCases {
		ctx, mgr := setupManagerForTest(c)
		helper := NewHandlerDepositTestHelper(mgr.Keeper())
		mgr.K = helper
		handler := NewDepositHandler(mgr)
		msg := tc.messageProvider(c, ctx, helper)
		result, err := handler.Run(ctx, msg)
		tc.validator(c, ctx, result, err, helper, tc.name)
	}
}

func (s *HandlerDepositSuite) TestAddSwap(c *C) {
	SetupConfigForTest()
	ctx, mgr := setupManagerForTest(c)
	handler := NewDepositHandler(mgr)
	affAddr := GetRandomTHORAddress()
	tx := common.NewTx(
		GetRandomTxHash(),
		GetRandomTHORAddress(),
		GetRandomTHORAddress(),
		common.Coins{common.NewCoin(common.RuneNative, cosmos.NewUint(common.One))},
		common.Gas{
			{Asset: common.DOGEAsset, Amount: cosmos.NewUint(37500)},
		},
		fmt.Sprintf("=:BTC.BTC:%s", GetRandomBTCAddress().String()),
	)
	// no affiliate fee
	msg := NewMsgSwap(tx, common.BTCAsset, GetRandomBTCAddress(), cosmos.ZeroUint(), common.NoAddress, cosmos.ZeroUint(), "", "", nil, MarketSwap, 0, 0, GetRandomBech32Addr())

	handler.addSwap(ctx, *msg)
	swap, err := mgr.Keeper().GetSwapQueueItem(ctx, tx.ID, 0)
	c.Assert(err, IsNil)
	c.Assert(swap.String(), Equals, msg.String())

	tx.Memo = fmt.Sprintf("=:BTC.BTC:%s::%s:20000", GetRandomBTCAddress().String(), affAddr.String())

	// affiliate fee, with more than 10K as basis points
	msg1 := NewMsgSwap(tx, common.BTCAsset, GetRandomBTCAddress(), cosmos.ZeroUint(), GetRandomTHORAddress(), cosmos.NewUint(20000), "", "", nil, MarketSwap, 0, 0, GetRandomBech32Addr())

	// Check balance before swap
	affiliateFeeAddr, err := msg1.GetAffiliateAddress().AccAddress()
	c.Assert(err, IsNil)
	acct := mgr.Keeper().GetBalance(ctx, affiliateFeeAddr)
	c.Assert(acct.AmountOf(common.RuneNative.Native()).String(), Equals, "0")

	handler.addSwap(ctx, *msg1)
	swap, err = mgr.Keeper().GetSwapQueueItem(ctx, tx.ID, 0)
	c.Assert(err, IsNil)
	c.Assert(swap.Tx.Coins[0].Amount.IsZero(), Equals, false)
	// Check balance after swap, should be the same
	c.Assert(acct.AmountOf(common.RuneNative.Native()).String(), Equals, "0")

	// affiliate fee not taken on deposit
	tx.Memo = fmt.Sprintf("=:BTC.BTC:%s::%s:1000", GetRandomBTCAddress().String(), affAddr.String())
	tx.Coins[0].Amount = cosmos.NewUint(common.One)
	msg2 := NewMsgSwap(tx, common.BTCAsset, GetRandomBTCAddress(), cosmos.ZeroUint(), affAddr, cosmos.NewUint(1000), "", "", nil, MarketSwap, 0, 0, GetRandomBech32Addr())
	handler.addSwap(ctx, *msg2)
	swap, err = mgr.Keeper().GetSwapQueueItem(ctx, tx.ID, 0)
	c.Assert(err, IsNil)
	c.Assert(swap.Tx.Coins[0].Amount.IsZero(), Equals, false)
	c.Assert(swap.Tx.Coins[0].Amount.String(), Equals, cosmos.NewUint(common.One).String())

	affiliateFeeAddr2, err := msg2.GetAffiliateAddress().AccAddress()
	c.Assert(err, IsNil)
	acct2 := mgr.Keeper().GetBalance(ctx, affiliateFeeAddr2)
	c.Assert(acct2.AmountOf(common.RuneNative.Native()).String(), Equals, strconv.FormatInt(0, 10))

	// NONE RUNE , synth asset should be handled correctly

	synthAsset, err := common.NewAsset("BTC/BTC")
	c.Assert(err, IsNil)
	tx1 := common.NewTx(
		GetRandomTxHash(),
		GetRandomTHORAddress(),
		GetRandomTHORAddress(),
		common.Coins{common.NewCoin(synthAsset, cosmos.NewUint(common.One))},
		common.Gas{
			{Asset: common.RuneNative, Amount: cosmos.NewUint(200000)},
		},
		tx.Memo,
	)

	c.Assert(mgr.Keeper().MintToModule(ctx, ModuleName, tx1.Coins[0]), IsNil)
	c.Assert(mgr.Keeper().SendFromModuleToModule(ctx, ModuleName, AsgardName, tx1.Coins), IsNil)
	msg3 := NewMsgSwap(tx1, common.BTCAsset, GetRandomBTCAddress(), cosmos.ZeroUint(), affAddr, cosmos.NewUint(1000), "", "", nil, MarketSwap, 0, 0, GetRandomBech32Addr())
	handler.addSwap(ctx, *msg3)
	swap, err = mgr.Keeper().GetSwapQueueItem(ctx, tx1.ID, 0)
	c.Assert(err, IsNil)
	c.Assert(swap.Tx.Coins[0].Amount.IsZero(), Equals, false)
	c.Assert(swap.Tx.Coins[0].Amount.String(), Equals, cosmos.NewUint(common.One).String())

	// affiliate fee not taken on deposit
	affiliateFeeAddr3, err := msg3.GetAffiliateAddress().AccAddress()
	c.Assert(err, IsNil)
	acct3 := mgr.Keeper().GetBalance(ctx, affiliateFeeAddr3)
	c.Assert(acct3.AmountOf(common.RuneNative.Native()).String(), Equals, strconv.FormatInt(0, 10))
}

func (s *HandlerDepositSuite) TestTargetModule(c *C) {
	acctAddr := GetRandomBech32Addr()
	testCases := []struct {
		name            string
		moduleName      string
		messageProvider func(c *C, ctx cosmos.Context) *MsgDeposit
		validator       func(c *C, ctx cosmos.Context, result *cosmos.Result, err error, name string, balDelta cosmos.Uint)
	}{
		{
			name:       "thorname coins should go to reserve",
			moduleName: ReserveName,
			messageProvider: func(c *C, ctx cosmos.Context) *MsgDeposit {
				addr := GetRandomRUNEAddress()
				coin := common.NewCoin(common.RuneAsset(), cosmos.NewUint(20_00000000))
				return NewMsgDeposit(common.Coins{coin}, "name:test:THOR:"+addr.String(), acctAddr)
			},
			validator: func(c *C, ctx cosmos.Context, result *cosmos.Result, err error, name string, balDelta cosmos.Uint) {
				c.Check(err, IsNil, Commentf(name))
				c.Assert(cosmos.NewUint(20_00000000).String(), Equals, balDelta.String(), Commentf(name))
			},
		},
	}
	for _, tc := range testCases {
		ctx, mgr := setupManagerForTest(c)
		handler := NewDepositHandler(mgr)
		msg := tc.messageProvider(c, ctx)
		totalCoins := common.NewCoins(msg.Coins[0])
		c.Assert(mgr.Keeper().MintToModule(ctx, ModuleName, totalCoins[0]), IsNil)
		c.Assert(mgr.Keeper().SendFromModuleToAccount(ctx, ModuleName, msg.Signer, totalCoins), IsNil)
		balBefore := mgr.Keeper().GetRuneBalanceOfModule(ctx, tc.moduleName)
		result, err := handler.Run(ctx, msg)
		balAfter := mgr.Keeper().GetRuneBalanceOfModule(ctx, tc.moduleName)
		balDelta := balAfter.Sub(balBefore)
		tc.validator(c, ctx, result, err, tc.name, balDelta)
	}
}
