package thorchain

import (
	. "gopkg.in/check.v1"
)

type MigrationDataSuite struct{}

var _ = Suite(&MigrationDataSuite{})

func (s MigrationDataSuite) TestVerifyTotal(c *C) {
	// verify total
	sum := uint64(0)
	for _, refund := range mainnetSlashRefunds4to5 {
		sum += refund.amount
	}
	// $ https thornode-v2.ninerealms.com/thorchain/block height==20518466 | \
	//   jq '[.txs[1].result.events[]|select(.bond_type=="bond_cost")|.amount|tonumber]|add'
	// 9674032456636
	c.Assert(sum, Equals, uint64(9674032456636))

	// verify no duplicates
	addresses := make(map[string]bool)
	for _, refund := range mainnetSlashRefunds4to5 {
		c.Assert(addresses[refund.address], Equals, false)
		addresses[refund.address] = true
	}
}
