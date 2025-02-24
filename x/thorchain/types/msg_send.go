package types

import (
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"google.golang.org/protobuf/proto"

	"gitlab.com/thorchain/thornode/v3/api/types"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
)

var (
	_ sdk.Msg              = &MsgSend{}
	_ sdk.HasValidateBasic = &MsgSend{}
	_ sdk.LegacyMsg        = &MsgSend{}
)

// NewMsgSend - construct a msg to send coins from one account to another.
func NewMsgSend(fromAddr, toAddr cosmos.AccAddress, amount cosmos.Coins) *MsgSend {
	return &MsgSend{FromAddress: fromAddr, ToAddress: toAddr, Amount: amount}
}

// ValidateBasic implements HasValidateBasic
// ValidateBasic is now ran in the message service router handler for messages that
// used to be routed using the external handler and only when HasValidateBasic is implemented.
// No versioning is used there.
func (m *MsgSend) ValidateBasic() error {
	if err := cosmos.VerifyAddressFormat(m.FromAddress); err != nil {
		return cosmos.ErrInvalidAddress(m.FromAddress.String())
	}

	if err := cosmos.VerifyAddressFormat(m.ToAddress); err != nil {
		return cosmos.ErrInvalidAddress(m.ToAddress.String())
	}

	if !m.Amount.IsValid() {
		return cosmos.ErrInvalidCoins("coins must be valid")
	}

	if !m.Amount.IsAllPositive() {
		return cosmos.ErrInvalidCoins("coins must be positive")
	}

	return nil
}

// GetSigners Implements LegacyMsg.
func (m *MsgSend) GetSigners() []cosmos.AccAddress {
	return []cosmos.AccAddress{m.FromAddress}
}

func MsgSendCustomGetSigners(m proto.Message) ([][]byte, error) {
	msgSend, ok := m.(*types.MsgSend)
	if !ok {
		return nil, errors.New("can't cast as MsgSend")
	}
	return [][]byte{msgSend.FromAddress}, nil
}
