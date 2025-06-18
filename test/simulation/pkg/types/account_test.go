package types

import (
	"testing"

	"gitlab.com/thorchain/thornode/v3/common"
	. "gopkg.in/check.v1"
)

func AccountTest(t *testing.T) { TestingT(t) }

type AccountSuite struct{}

var _ = Suite(&AccountSuite{})

func (s *ActorSuite) TestNewUser(c *C) {
	testMnemonic := "dog dog dog dog dog dog dog dog dog dog dog dog dog dog dog dog dog dog dog dog dog dog dog fossil"
	constructors := make(map[common.Chain]LiteChainClientConstructor)
	user := NewUser(testMnemonic, constructors)

	c.Assert(user, NotNil)
}
