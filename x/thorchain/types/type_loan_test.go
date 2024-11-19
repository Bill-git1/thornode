package types

import (
	"gitlab.com/thorchain/thornode/v3/common"
	. "gopkg.in/check.v1"
)

type TypeLoanSuite struct{}

var _ = Suite(&TypeLoanSuite{})

func (mas *TypeLoanSuite) SetUpSuite(c *C) {
	SetupConfigForTest()
}

func (TypeLoanSuite) TestLoan(c *C) {
	addr := common.Address("0x90f2b1ae50e6018230e90a33f98c7844a0ab635a")
	loan := NewLoan(addr, common.ETHAsset, 25)

	c.Check(loan.Key(), Equals, "ETH.ETH/0x90f2b1ae50e6018230e90a33f98c7844a0ab635a")

	// happy path
	c.Check(loan.Valid(), IsNil)

	// bad last height
	loan.LastOpenHeight = 0
	c.Check(loan.Valid(), NotNil)
	loan.LastOpenHeight = -5
	c.Check(loan.Valid(), NotNil)

	// bad owner
	loan.LastOpenHeight = 25
	loan.Owner = common.NoAddress
	c.Check(loan.Valid(), NotNil)

	// bad asset
	loan.Owner = addr
	loan.Asset = common.EmptyAsset
	c.Check(loan.Valid(), NotNil)
}
