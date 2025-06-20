package common

import (
	"strings"

	cosmos "gitlab.com/thorchain/thornode/v3/common/cosmos"
	. "gopkg.in/check.v1"
)

type TxSuite struct{}

var _ = Suite(&TxSuite{})

func (s TxSuite) TestTxID(c *C) {
	testCases := []struct {
		Id      string
		IsValid bool
		ToUpper bool
		Int64   int64
	}{
		{
			// Cosmos
			Id:      "A7DA8FF1B7C290616D68A276F30AC618315E6CCE982EB8F7A79339E163798F49",
			IsValid: true,
			ToUpper: true,
			Int64:   1668910921, // 63798F49
		}, {
			// Cosmos indexed
			Id:      "A7DA8FF1B7C290616D68A276F30AC618315E6CCE982EB8F7A79339E163798F49-1",
			IsValid: true,
			ToUpper: true,
			Int64:   932770961, // 3798F49 + 1
		}, {
			// Cosmos indexed, big number
			Id:      "A7DA8FF1B7C290616D68A276F30AC618315E6CCE982EB8F7A79339E163798F49-97836",
			IsValid: true,
			ToUpper: true,
			Int64:   4103175724, // F49 + 17E2C
		}, {
			// EVM
			Id:      "0xb41cf456e942f3430681298c503def54b79a96e3373ef9d44ea314d7eae41952",
			IsValid: true,
			ToUpper: true,
			Int64:   3940817234, // EAE41952
		}, {
			// Bogus
			Id:      "bogus",
			IsValid: false,
		}, {
			// Cosmos indexed, invalid index char
			Id:      "A7DA8FF1B7C290616D68A276F30AC618315E6CCE982EB8F7A79339E163798F49-A",
			IsValid: false,
		}, {
			// Cosmos indexed, invalid index at pos 63
			Id:      "A7DA8FF1B7C290616D68A276F30AC618315E6CCE982EB8F7A79339E163798F4-12",
			IsValid: false,
		}, {
			// Cosmos indexed, multiple dashes
			Id:      "A7DA8FF1B7C290616D68A-76F30AC618315E6-CE982EB8F7A79339E163798F49-1",
			IsValid: false,
		},
	}

	for _, testCase := range testCases {
		tx, err := NewTxID(testCase.Id)

		if testCase.IsValid {
			c.Assert(err, IsNil)

			expectedId := testCase.Id
			if testCase.ToUpper {
				expectedId = strings.ToUpper(testCase.Id)
			}

			c.Check(tx.String(), Equals, expectedId)
			c.Check(tx.IsEmpty(), Equals, false)
			c.Check(tx.Equals(TxID(testCase.Id)), Equals, true)
			c.Check(func() { tx.Int64() }, Not(Panics), "Failed to convert")
		} else {
			c.Check(err, NotNil)
			c.Check(tx.String(), Equals, "")
			c.Check(tx.Int64(), Equals, int64(0))
		}
	}
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
