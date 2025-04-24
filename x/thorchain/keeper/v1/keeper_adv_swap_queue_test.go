package keeperv1

import (
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
	. "gopkg.in/check.v1"
)

type KeeperAdvSwapQueueSuite struct{}

var _ = Suite(&KeeperAdvSwapQueueSuite{})

func (s *KeeperAdvSwapQueueSuite) TestKeeperAdvSwapQueue(c *C) {
	ctx, k := setupKeeperForTest(c)

	// not found
	_, err := k.GetAdvSwapQueueItem(ctx, GetRandomTxHash())
	c.Assert(err, NotNil)

	msg1 := MsgSwap{
		Tx:          GetRandomTx(),
		TradeTarget: cosmos.NewUint(10 * common.One),
		SwapType:    types.SwapType_limit,
	}
	msg2 := MsgSwap{
		Tx:          GetRandomTx(),
		TradeTarget: cosmos.NewUint(10 * common.One),
		SwapType:    types.SwapType_limit,
	}

	c.Assert(k.SetAdvSwapQueueItem(ctx, msg1), IsNil)
	c.Assert(k.SetAdvSwapQueueItem(ctx, msg2), IsNil)
	msg3, err := k.GetAdvSwapQueueItem(ctx, msg1.Tx.ID)
	c.Assert(err, IsNil)
	c.Check(msg3.Tx.ID.Equals(msg1.Tx.ID), Equals, true)

	c.Check(k.HasAdvSwapQueueItem(ctx, msg1.Tx.ID), Equals, true)
	ok, err := k.HasAdvSwapQueueIndex(ctx, msg1)
	c.Assert(err, IsNil)
	c.Check(ok, Equals, true)

	iter := k.GetAdvSwapQueueItemIterator(ctx)
	for ; iter.Valid(); iter.Next() {
		var m MsgSwap
		k.Cdc().MustUnmarshal(iter.Value(), &m)
		c.Check(m.Tx.ID.Equals(msg1.Tx.ID) || m.Tx.ID.Equals(msg2.Tx.ID), Equals, true)
	}
	iter.Close()

	iter = k.GetAdvSwapQueueIndexIterator(ctx, msg1.SwapType, msg1.Tx.Coins[0].Asset, msg1.TargetAsset)
	for ; iter.Valid(); iter.Next() {
		hashes := make([]string, 0)
		ok, err = k.getStrings(ctx, string(iter.Key()), &hashes)
		c.Assert(err, IsNil)
		c.Check(ok, Equals, true)
		c.Check(hashes, HasLen, 2)
		c.Check(hashes[0], Equals, msg1.Tx.ID.String())
		c.Check(hashes[1], Equals, msg2.Tx.ID.String())
	}
	iter.Close()

	// test remove
	c.Assert(k.RemoveAdvSwapQueueItem(ctx, msg1.Tx.ID), IsNil)
	_, err = k.GetAdvSwapQueueItem(ctx, msg1.Tx.ID)
	c.Check(err, NotNil)
	c.Check(k.HasAdvSwapQueueItem(ctx, msg1.Tx.ID), Equals, false)
	ok, err = k.HasAdvSwapQueueIndex(ctx, msg1)
	c.Assert(err, IsNil)
	c.Check(ok, Equals, false)
}

func (s *KeeperAdvSwapQueueSuite) TestGetAdvSwapQueueIndexKey(c *C) {
	ctx, k := setupKeeperForTest(c)
	msg := MsgSwap{
		SwapType: types.SwapType_limit,
		Tx: common.Tx{
			Coins: common.NewCoins(common.NewCoin(common.BTCAsset, cosmos.NewUint(10000))),
		},
		TargetAsset: common.RuneAsset(),
		TradeTarget: cosmos.NewUint(1239585),
	}
	c.Check(k.getAdvSwapQueueIndexKey(ctx, msg), Equals, "aqlim//BTC.BTC>THOR.RUNE/000000000000806721/")
}

func (s *KeeperAdvSwapQueueSuite) TestRewriteRatio(c *C) {
	c.Check(rewriteRatio(3, "5"), Equals, "005")    // smaller
	c.Check(rewriteRatio(3, "5000"), Equals, "500") // larger
	c.Check(rewriteRatio(3, "500"), Equals, "500")  // just right
}

func (s *KeeperAdvSwapQueueSuite) TestRemoveSlice(c *C) {
	c.Check(removeString([]string{"foo", "bar", "baz"}, 0), DeepEquals, []string{"baz", "bar"})
	c.Check(removeString([]string{"foo", "bar", "baz"}, 1), DeepEquals, []string{"foo", "baz"})
	c.Check(removeString([]string{"foo", "bar", "baz"}, 2), DeepEquals, []string{"foo", "bar"})
	c.Check(removeString([]string{"foo", "bar", "baz"}, 3), DeepEquals, []string{"foo", "bar", "baz"})
	c.Check(removeString([]string{"foo", "bar", "baz"}, -1), DeepEquals, []string{"foo", "bar", "baz"})
}
