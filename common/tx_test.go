package common

import (
	cosmos "gitlab.com/thorchain/thornode/v3/common/cosmos"
	. "gopkg.in/check.v1"
)

type TxSuite struct{}

var _ = Suite(&TxSuite{})

func (s TxSuite) TestTxID(c *C) {
	ID := "A7DA8FF1B7C290616D68A276F30AC618315E6CCE982EB8F7A79339E163798F49"
	tx, err := NewTxID(ID)
	c.Assert(err, IsNil)
	c.Check(tx.String(), Equals, ID)
	c.Check(tx.IsEmpty(), Equals, false)
	c.Check(tx.Equals(TxID(ID)), Equals, true)
	c.Check(func() { tx.Int64() }, Not(Panics), "Failed to convert")

	// check eth hash
	_, err = NewTxID("0xb41cf456e942f3430681298c503def54b79a96e3373ef9d44ea314d7eae41952")
	c.Assert(err, IsNil)

	tx, err = NewTxID("bogus")
	c.Check(err, NotNil)
	c.Check(tx.Int64(), Equals, int64(0))
}

func (s TxSuite) TestTx(c *C) {
	id, err := NewTxID("0xb41cf456e942f3430681298c503def54b79a96e3373ef9d44ea314d7eae41952")
	c.Assert(err, IsNil)
	tx := NewTx(
		id,
		Address("0x90f2b1ae50e6018230e90a33f98c7844a0ab635a"),
		Address("0x90f2b1ae50e6018230e90a33f98c7844a0ab635a"),
		Coins{NewCoin(ETHAsset, cosmos.NewUint(5*One))},
		Gas{NewCoin(ETHAsset, cosmos.NewUint(10000))},
		"hello memo",
	)
	c.Check(tx.ID.Equals(id), Equals, true)
	c.Check(tx.IsEmpty(), Equals, false)
	c.Check(tx.FromAddress.IsEmpty(), Equals, false)
	c.Check(tx.ToAddress.IsEmpty(), Equals, false)
	c.Assert(tx.Coins, HasLen, 1)
	c.Check(tx.Coins[0].Equals(NewCoin(ETHAsset, cosmos.NewUint(5*One))), Equals, true)
	c.Check(tx.Memo, Equals, "hello memo")
}
