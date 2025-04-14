package keeperv1

import (
	"cosmossdk.io/math"
	"gitlab.com/thorchain/thornode/v3/common"
	. "gopkg.in/check.v1"
)

type KeeperTCYStakerSuite struct{}

var _ = Suite(&KeeperTCYStakerSuite{})

func (s *KeeperTCYStakerSuite) TestTCYStaker(c *C) {
	ctx, k := setupKeeperForTest(c)
	initStakers := []TCYStaker{
		{
			Address: GetRandomRUNEAddress(),
			Amount:  math.NewUint(1 * common.One),
		},
		{
			Address: GetRandomRUNEAddress(),
			Amount:  math.NewUint(10 * common.One),
		},
		{
			Address: GetRandomRUNEAddress(),
			Amount:  math.NewUint(100 * common.One),
		},
		{
			Address: GetRandomRUNEAddress(),
			Amount:  math.NewUint(1000 * common.One),
		},
		{
			Address: GetRandomRUNEAddress(),
			Amount:  math.NewUint(10000 * common.One),
		},
	}

	// Set stakers
	for _, staker := range initStakers {
		c.Assert(k.SetTCYStaker(ctx, staker), IsNil)
	}

	stakers, err := k.ListTCYStakers(ctx)
	c.Assert(err, IsNil)

	// Include TCY smart contract staker
	expectedLen := len(initStakers) + 1
	c.Assert(len(stakers), Equals, expectedLen)

	var staker TCYStaker
	for _, initStaker := range initStakers {
		c.Assert(k.TCYStakerExists(ctx, initStaker.Address), Equals, true)
		staker, err = k.GetTCYStaker(ctx, initStaker.Address)
		c.Assert(err, IsNil)
		c.Assert(staker.Amount.Equal(initStaker.Amount), Equals, true)
	}

	// Delete stakers
	for _, staker := range initStakers {
		k.DeleteTCYStaker(ctx, staker.Address)
	}

	stakers, err = k.ListTCYStakers(ctx)
	c.Assert(err, IsNil)

	// Just TCY smart contract staker
	c.Assert(len(stakers), Equals, 1)

	for _, staker := range initStakers {
		c.Assert(k.TCYStakerExists(ctx, staker.Address), Equals, false)
	}
}
