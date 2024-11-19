package keeperv1

import (
	"gitlab.com/thorchain/thornode/v3/common"
	. "gopkg.in/check.v1"
)

type KeeperTHORNameSuite struct{}

var _ = Suite(&KeeperTHORNameSuite{})

func (s *KeeperTHORNameSuite) TestTHORName(c *C) {
	ctx, k := setupKeeperForTest(c)
	var err error
	ref := "helloworld"

	ok := k.THORNameExists(ctx, ref)
	c.Assert(ok, Equals, false)

	thorAddr := GetRandomTHORAddress()
	ethAddr := GetRandomETHAddress()
	name := NewTHORName(ref, 50, []THORNameAlias{{Chain: common.THORChain, Address: thorAddr}, {Chain: common.ETHChain, Address: ethAddr}})
	k.SetTHORName(ctx, name)

	ok = k.THORNameExists(ctx, ref)
	c.Assert(ok, Equals, true)
	ok = k.THORNameExists(ctx, "bogus")
	c.Assert(ok, Equals, false)

	name, err = k.GetTHORName(ctx, ref)
	c.Assert(err, IsNil)
	c.Assert(name.GetAlias(common.THORChain).Equals(thorAddr), Equals, true)
	c.Assert(name.GetAlias(common.ETHChain).Equals(ethAddr), Equals, true)
}
