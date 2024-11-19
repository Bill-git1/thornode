package types

import (
	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
)

type MsgLoanSuite struct{}

var _ = Suite(&MsgLoanSuite{})

func (MsgLoanSuite) TestMsgLoanOpenSuite(c *C) {
	acc := GetRandomBech32Addr()

	owner := GetRandomTHORAddress()
	colA := common.BTCAsset
	units := cosmos.NewUint(100)
	targetA := GetRandomBTCAddress()
	msg := NewMsgLoanOpen(owner, colA, units, targetA, colA, cosmos.ZeroUint(), common.NoAddress, cosmos.ZeroUint(), "", "", cosmos.ZeroUint(), acc, common.TxID("test_tx_id"))
	c.Assert(msg.ValidateBasic(), IsNil)
	c.Assert(msg.GetSigners(), NotNil)
	c.Assert(msg.GetSigners()[0].String(), Equals, acc.String())

	msg = NewMsgLoanOpen(common.NoAddress, colA, units, targetA, colA, cosmos.ZeroUint(), common.NoAddress, cosmos.ZeroUint(), "", "", cosmos.ZeroUint(), acc, common.TxID("test_tx_id"))
	c.Assert(msg.ValidateBasic(), NotNil)
	msg = NewMsgLoanOpen(owner, common.EmptyAsset, units, targetA, colA, cosmos.ZeroUint(), common.NoAddress, cosmos.ZeroUint(), "", "", cosmos.ZeroUint(), acc, common.TxID("test_tx_id"))
	c.Assert(msg.ValidateBasic(), NotNil)
	msg = NewMsgLoanOpen(owner, common.TOR, units, targetA, colA, cosmos.ZeroUint(), common.NoAddress, cosmos.ZeroUint(), "", "", cosmos.ZeroUint(), acc, common.TxID("test_tx_id"))
	c.Assert(msg.ValidateBasic(), NotNil)
	msg = NewMsgLoanOpen(owner, colA, cosmos.ZeroUint(), targetA, colA, cosmos.ZeroUint(), common.NoAddress, cosmos.ZeroUint(), "", "", cosmos.ZeroUint(), acc, common.TxID("test_tx_id"))
	c.Assert(msg.ValidateBasic(), NotNil)
	msg = NewMsgLoanOpen(owner, colA, units, GetRandomETHAddress(), colA, cosmos.ZeroUint(), common.NoAddress, cosmos.ZeroUint(), "", "", cosmos.ZeroUint(), acc, common.TxID("test_tx_id"))
	c.Assert(msg.ValidateBasic(), NotNil)
	msg = NewMsgLoanOpen(owner, colA, units, common.NoAddress, colA, cosmos.ZeroUint(), common.NoAddress, cosmos.ZeroUint(), "", "", cosmos.ZeroUint(), acc, common.TxID("test_tx_id"))
	c.Assert(msg.ValidateBasic(), NotNil)
	msg = NewMsgLoanOpen(owner, colA, units, targetA, common.EmptyAsset, cosmos.ZeroUint(), common.NoAddress, cosmos.ZeroUint(), "", "", cosmos.ZeroUint(), acc, common.TxID("test_tx_id"))
	c.Assert(msg.ValidateBasic(), NotNil)
	msg = NewMsgLoanOpen(owner, colA, units, targetA, colA, cosmos.ZeroUint(), common.NoAddress, cosmos.ZeroUint(), "", "", cosmos.ZeroUint(), cosmos.AccAddress{}, common.TxID("test_tx_id"))
	c.Assert(msg.ValidateBasic(), NotNil)
}

func (MsgLoanSuite) TestMsgLoanRepaySuite(c *C) {
	acc := GetRandomBech32Addr()

	owner := GetRandomETHAddress()
	colA := common.BTCAsset
	coin := common.NewCoin(common.ETHAsset, cosmos.NewUint(90*common.One))
	msg := NewMsgLoanRepayment(owner, colA, cosmos.OneUint(), owner, coin, acc, common.TxID("test_tx_id"))
	c.Assert(msg.ValidateBasic(), IsNil)
	c.Assert(msg.GetSigners(), NotNil)
	c.Assert(msg.GetSigners()[0].String(), Equals, acc.String())

	msg = NewMsgLoanRepayment(common.NoAddress, colA, cosmos.OneUint(), owner, coin, acc, common.TxID("test_tx_id"))
	c.Assert(msg.ValidateBasic(), NotNil)
	msg = NewMsgLoanRepayment(owner, common.EmptyAsset, cosmos.OneUint(), owner, coin, acc, common.TxID("test_tx_id"))
	c.Assert(msg.ValidateBasic(), NotNil)
	msg = NewMsgLoanRepayment(owner, colA, cosmos.OneUint(), owner, common.Coin{}, acc, common.TxID("test_tx_id"))
	c.Assert(msg.ValidateBasic(), NotNil)
	msg = NewMsgLoanRepayment(owner, colA, cosmos.OneUint(), owner, coin, cosmos.AccAddress{}, common.TxID("test_tx_id"))
	c.Assert(msg.ValidateBasic(), NotNil)
}
