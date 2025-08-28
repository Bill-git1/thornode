package keeperv1

import (
	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
)

type KeeperOutboundFeesSuite struct{}

var _ = Suite(&KeeperOutboundFeesSuite{})

func (s *KeeperOutboundFeesSuite) TestGetSurplusForTargetMultiplier(c *C) {
	ctx, k := setupKeeperForTest(c)

	surplus := k.GetSurplusForTargetMultiplier(ctx, cosmos.NewUint(10_000))
	c.Check(surplus.String(), Equals, "689655172414")
}

func (s *KeeperOutboundFeesSuite) TestOutboundRuneRecords(c *C) {
	ctx, k := setupKeeperForTest(c)

	// Nothing set returns 0.
	feeWithheldRune, err := k.GetOutboundFeeWithheldRune(ctx, common.BTCAsset)
	c.Assert(err, IsNil)
	feeSpentRune, err := k.GetOutboundFeeSpentRune(ctx, common.BTCAsset)
	c.Assert(err, IsNil)
	// The initial withheld amount is the surplus for the target multiplier
	initialWithheldBTC := k.GetSurplusForTargetMultiplier(ctx, cosmos.NewUint(10_000))
	c.Check(feeWithheldRune.String(), Equals, initialWithheldBTC.String())
	c.Check(feeSpentRune.String(), Equals, "0")

	// Adding sets.
	err = k.AddToOutboundFeeWithheldRune(ctx, common.BTCAsset, cosmos.NewUint(uint64(200)))
	c.Assert(err, IsNil)
	initialWithheldBTC = initialWithheldBTC.Add(cosmos.NewUint(uint64(200)))
	err = k.AddToOutboundFeeSpentRune(ctx, common.BTCAsset, cosmos.NewUint(uint64(100)))
	c.Assert(err, IsNil)

	feeWithheldRune, err = k.GetOutboundFeeWithheldRune(ctx, common.BTCAsset)
	c.Assert(err, IsNil)
	feeSpentRune, err = k.GetOutboundFeeSpentRune(ctx, common.BTCAsset)
	c.Assert(err, IsNil)
	c.Check(feeWithheldRune.String(), Equals, initialWithheldBTC.String())
	c.Check(feeSpentRune.String(), Equals, "100")

	// Adding again adds.
	err = k.AddToOutboundFeeWithheldRune(ctx, common.BTCAsset, cosmos.NewUint(uint64(400)))
	c.Assert(err, IsNil)
	initialWithheldBTC = initialWithheldBTC.Add(cosmos.NewUint(uint64(400)))
	err = k.AddToOutboundFeeSpentRune(ctx, common.BTCAsset, cosmos.NewUint(uint64(300)))
	c.Assert(err, IsNil)

	feeWithheldRune, err = k.GetOutboundFeeWithheldRune(ctx, common.BTCAsset)
	c.Assert(err, IsNil)
	feeSpentRune, err = k.GetOutboundFeeSpentRune(ctx, common.BTCAsset)
	c.Assert(err, IsNil)
	c.Check(feeWithheldRune.String(), Equals, initialWithheldBTC.String())
	c.Check(feeSpentRune.String(), Equals, cosmos.NewUint(uint64(400)).String())

	// Set values are distinct by Asset.
	initialWithheldETH := k.GetSurplusForTargetMultiplier(ctx, cosmos.NewUint(10_000))
	feeWithheldRune, err = k.GetOutboundFeeWithheldRune(ctx, common.ETHAsset)
	c.Assert(err, IsNil)
	feeSpentRune, err = k.GetOutboundFeeSpentRune(ctx, common.ETHAsset)
	c.Assert(err, IsNil)
	c.Check(feeWithheldRune.String(), Equals, initialWithheldETH.String())
	c.Check(feeSpentRune.String(), Equals, "0")

	err = k.AddToOutboundFeeWithheldRune(ctx, common.ETHAsset, cosmos.NewUint(uint64(50)))
	c.Assert(err, IsNil)
	initialWithheldETH = initialWithheldETH.Add(cosmos.NewUint(uint64(50)))
	err = k.AddToOutboundFeeSpentRune(ctx, common.BTCAsset, cosmos.NewUint(uint64(30)))
	c.Assert(err, IsNil)

	feeWithheldRune, err = k.GetOutboundFeeWithheldRune(ctx, common.BTCAsset)
	c.Assert(err, IsNil)
	feeSpentRune, err = k.GetOutboundFeeSpentRune(ctx, common.BTCAsset)
	c.Assert(err, IsNil)
	c.Check(feeWithheldRune.String(), Equals, initialWithheldBTC.String())
	c.Check(feeSpentRune.String(), Equals, "430")

	feeWithheldRune, err = k.GetOutboundFeeWithheldRune(ctx, common.ETHAsset)
	c.Assert(err, IsNil)
	feeSpentRune, err = k.GetOutboundFeeSpentRune(ctx, common.ETHAsset)
	c.Assert(err, IsNil)
	c.Check(feeWithheldRune.String(), Equals, initialWithheldETH.String())
	c.Check(feeSpentRune.String(), Equals, "0")
}
