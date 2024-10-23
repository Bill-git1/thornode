//go:build mocknet
// +build mocknet

package constants

import (
	"testing"

	. "gopkg.in/check.v1"
)

func TestPackage(t *testing.T) { TestingT(t) }

type ConstantsSuite struct{}

var _ = Suite(&ConstantsSuite{})

func (s *ConstantsSuite) Test010(c *C) {
	consts := NewConstantValue()
	c.Check(consts.GetInt64Value(PoolCycle), Equals, int64(43200))
}

func (s *ConstantsSuite) TestCamelToSnake(c *C) {
	c.Check(camelToSnakeUpper("PoolCycle"), Equals, "POOL_CYCLE")
	c.Check(camelToSnakeUpper("L1SlipMinBps"), Equals, "L1_SLIP_MIN_BPS")
	c.Check(camelToSnakeUpper("TNSRegisterFee"), Equals, "TNS_REGISTER_FEE")
	c.Check(camelToSnakeUpper("MaxNodeToChurnOutForLowVersion"), Equals, "MAX_NODE_TO_CHURN_OUT_FOR_LOW_VERSION")
}
