package thorchain

import (
	"math"

	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
)

type HandlerManageTHORNameSuite struct{}

var _ = Suite(&HandlerManageTHORNameSuite{})

func (s *HandlerManageTHORNameSuite) TestValidator(c *C) {
	ctx, mgr := setupManagerForTest(c)

	handler := NewManageTHORNameHandler(mgr)
	coin := common.NewCoin(common.RuneAsset(), cosmos.NewUint(100*common.One))
	addr := GetRandomTHORAddress()
	acc, _ := addr.AccAddress()
	name := NewTHORName("hello", 50, []THORNameAlias{{Chain: common.THORChain, Address: addr}})
	mgr.Keeper().SetTHORName(ctx, name)

	// set pool for preferred asset
	pool, err := mgr.Keeper().GetPool(ctx, common.ETHAsset)
	c.Assert(err, IsNil)
	pool.Asset = common.ETHAsset
	err = mgr.Keeper().SetPool(ctx, pool)
	c.Assert(err, IsNil)

	// happy path
	msg := NewMsgManageTHORName("I-am_the_99th_walrus+", common.THORChain, addr, coin, 0, common.ETHAsset, acc, acc)
	c.Assert(handler.validate(ctx, *msg), IsNil)

	// fail: address is wrong chain
	msg.Chain = common.ETHChain
	c.Assert(handler.validate(ctx, *msg), NotNil)

	// fail: address is wrong network
	mainnetBTCAddr, err := common.NewAddress("bc1qy0tj9fh0u6fgz0mejjp6776z6kugych0zwrkwr")
	c.Assert(err, IsNil)
	msg.Address = mainnetBTCAddr
	c.Assert(handler.validate(ctx, *msg), NotNil)

	// restore to happy path
	msg.Chain = common.THORChain
	msg.Address = addr

	// fail: name is too long
	msg.Name = "this_name_is_way_too_long_to_be_a_valid_name"
	c.Assert(handler.validate(ctx, *msg), NotNil)

	// fail: bad characters
	msg.Name = "i am the walrus"
	c.Assert(handler.validate(ctx, *msg), NotNil)

	// fail: bad attempt to inflate expire block height
	msg.Name = "hello"
	msg.ExpireBlockHeight = 100
	c.Assert(handler.validate(ctx, *msg), NotNil)

	// fail: bad auth
	msg.ExpireBlockHeight = 0
	msg.Signer = GetRandomBech32Addr()
	c.Assert(handler.validate(ctx, *msg), NotNil)

	// fail: not enough funds for new THORName
	msg.Name = "bang"
	msg.Coin.Amount = cosmos.ZeroUint()
	c.Assert(handler.validate(ctx, *msg), NotNil)
}

func (s *HandlerManageTHORNameSuite) TestHandler(c *C) {
	ver := GetCurrentVersion()
	constAccessor := constants.GetConstantValues(ver)
	feePerBlock := constAccessor.GetInt64Value(constants.TNSFeePerBlock)
	registrationFee := constAccessor.GetInt64Value(constants.TNSRegisterFee)
	ctx, mgr := setupManagerForTest(c)

	blocksPerYear := mgr.GetConstants().GetInt64Value(constants.BlocksPerYear)
	handler := NewManageTHORNameHandler(mgr)
	coin := common.NewCoin(common.RuneAsset(), cosmos.NewUint(100*common.One))
	addr := GetRandomTHORAddress()
	acc, _ := addr.AccAddress()
	tnName := "hello"

	// add rune to addr for gas
	FundAccount(c, ctx, mgr.Keeper(), acc, 10*common.One)

	// happy path, register new name
	msg := NewMsgManageTHORName(tnName, common.THORChain, addr, coin, 0, common.RuneAsset(), acc, acc)
	_, err := handler.handle(ctx, *msg)
	c.Assert(err, IsNil)
	name, err := mgr.Keeper().GetTHORName(ctx, tnName)
	c.Assert(err, IsNil)
	c.Check(name.Owner.Empty(), Equals, false)
	c.Check(name.ExpireBlockHeight, Equals, ctx.BlockHeight()+blocksPerYear+(int64(coin.Amount.Uint64())-registrationFee)/feePerBlock)

	// happy path, set alt chain address
	ethAddr := GetRandomETHAddress()
	msg = NewMsgManageTHORName(tnName, common.ETHChain, ethAddr, coin, 0, common.RuneAsset(), acc, acc)
	_, err = handler.handle(ctx, *msg)
	c.Assert(err, IsNil)
	name, err = mgr.Keeper().GetTHORName(ctx, tnName)
	c.Assert(err, IsNil)
	c.Check(name.GetAlias(common.ETHChain).Equals(ethAddr), Equals, true)

	// happy path, update alt chain address
	ethAddr = GetRandomETHAddress()
	msg = NewMsgManageTHORName(tnName, common.ETHChain, ethAddr, coin, 0, common.RuneAsset(), acc, acc)
	_, err = handler.handle(ctx, *msg)
	c.Assert(err, IsNil)
	name, err = mgr.Keeper().GetTHORName(ctx, tnName)
	c.Assert(err, IsNil)
	c.Check(name.GetAlias(common.ETHChain).Equals(ethAddr), Equals, true)

	// update preferred asset
	msg = NewMsgManageTHORName(tnName, common.ETHChain, ethAddr, coin, 0, common.ETHAsset, acc, acc)
	_, err = handler.handle(ctx, *msg)
	c.Assert(err, IsNil)
	name, err = mgr.Keeper().GetTHORName(ctx, tnName)
	c.Assert(err, IsNil)
	c.Check(name.PreferredAsset, Equals, common.ETHAsset)

	// transfer thorname to new owner, should reset preferred asset/external aliases
	addr2 := GetRandomTHORAddress()
	acc2, _ := addr2.AccAddress()
	msg = NewMsgManageTHORName(tnName, common.THORChain, addr, coin, 0, common.RuneAsset(), acc2, acc)
	_, err = handler.handle(ctx, *msg)
	c.Assert(err, IsNil)
	name, err = mgr.Keeper().GetTHORName(ctx, tnName)
	c.Assert(err, IsNil)
	c.Check(len(name.GetAliases()), Equals, 0)
	c.Check(name.PreferredAsset.IsEmpty(), Equals, true)
	c.Check(name.GetOwner().Equals(acc2), Equals, true)

	// happy path, release thorname back into the wild
	msg = NewMsgManageTHORName(tnName, common.THORChain, addr, common.NewCoin(common.RuneAsset(), cosmos.ZeroUint()), 1, common.RuneAsset(), acc, acc)
	_, err = handler.handle(ctx, *msg)
	c.Assert(err, IsNil)
	name, err = mgr.Keeper().GetTHORName(ctx, tnName)
	c.Assert(err, IsNil)
	c.Check(name.Owner.Empty(), Equals, true)
	c.Check(name.ExpireBlockHeight, Equals, int64(0))
}

func (s *HandlerManageTHORNameSuite) TestHandlerIntegerOverflow(c *C) {
	ctx, mgr := setupManagerForTest(c)

	handler := NewManageTHORNameHandler(mgr)
	addr := GetRandomTHORAddress()
	acc, _ := addr.AccAddress()

	// add rune to addr for gas
	FundAccount(c, ctx, mgr.Keeper(), acc, 10*common.One)

	// Test coin amount exceeding int64 max
	// Create a coin with an amount larger than MaxInt64
	hugeAmount := cosmos.NewUint(math.MaxUint64)
	hugeCoin := common.NewCoin(common.RuneAsset(), hugeAmount)
	msg := NewMsgManageTHORName("overflow-test", common.THORChain, addr, hugeCoin, 0, common.RuneAsset(), acc, acc)
	_, err := handler.handle(ctx, *msg)
	c.Assert(err, NotNil)
	c.Assert(err.Error(), Matches, ".*coin amount exceeds maximum allowed value.*")
}

func (s *HandlerManageTHORNameSuite) TestHandlerBlockHeightOverflow(c *C) {
	ver := GetCurrentVersion()
	constAccessor := constants.GetConstantValues(ver)
	feePerBlock := constAccessor.GetInt64Value(constants.TNSFeePerBlock)
	ctx, mgr := setupManagerForTest(c)

	handler := NewManageTHORNameHandler(mgr)
	addr := GetRandomTHORAddress()
	acc, _ := addr.AccAddress()
	tnName := "overflow-expire"

	// add rune to addr for gas
	FundAccount(c, ctx, mgr.Keeper(), acc, 10*common.One)

	// First, register a THORName
	coin := common.NewCoin(common.RuneAsset(), cosmos.NewUint(100*common.One))
	msg := NewMsgManageTHORName(tnName, common.THORChain, addr, coin, 0, common.RuneAsset(), acc, acc)
	_, err := handler.handle(ctx, *msg)
	c.Assert(err, IsNil)

	// Now create a THORName with very high expire block height and try to extend it
	// This should trigger the overflow check when trying to add more blocks
	name, err := mgr.Keeper().GetTHORName(ctx, tnName)
	c.Assert(err, IsNil)

	// Set expire block height to near MaxInt64 to trigger overflow when adding blocks
	name.ExpireBlockHeight = math.MaxInt64 - 100
	mgr.Keeper().SetTHORName(ctx, name)

	// Try to extend with a large amount that would overflow
	// Calculate the amount needed to generate enough blocks to overflow
	// addBlocks = amount / feePerBlock, so we need amount > 100 * feePerBlock to overflow
	overflowAmount := cosmos.NewUint(uint64(200 * feePerBlock))
	overflowCoin := common.NewCoin(common.RuneAsset(), overflowAmount)
	msg = NewMsgManageTHORName(tnName, common.THORChain, addr, overflowCoin, 0, common.RuneAsset(), acc, acc)
	_, err = handler.handle(ctx, *msg)
	c.Assert(err, NotNil)
	c.Assert(err.Error(), Matches, ".*block calculation overflow.*")
}
