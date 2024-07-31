package tokenlist

import (
	"github.com/blang/semver"
	"gitlab.com/thorchain/thornode/constants"
	. "gopkg.in/check.v1"
)

type ETHTokenListSuite struct{}

var _ = Suite(&ETHTokenListSuite{})

func (s ETHTokenListSuite) TestLoad(c *C) {
	tokens := GetETHTokenList(constants.SWVersion)
	c.Check(tokens.Name, Equals, "")
	c.Check(len(tokens.Tokens) > 0, Equals, false)

	v, _ := semver.Make("999.0.0")
	tokens = GetETHTokenList(v)
	c.Check(tokens.Name, Equals, "Mocknet Token List")
	c.Check(len(tokens.Tokens) > 0, Equals, true)
}
