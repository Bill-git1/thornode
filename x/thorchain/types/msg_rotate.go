package types

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
)

var (
	_ sdk.Msg              = &MsgRotate{}
	_ sdk.HasValidateBasic = &MsgRotate{}
	_ sdk.LegacyMsg        = &MsgRotate{}
)

// NewMsgRotate is a constructor function for MsgRotate
func NewMsgRotate(signer, operatorAddress cosmos.AccAddress, coin common.Coin) *MsgRotate {
	return &MsgRotate{
		Signer:          signer,
		OperatorAddress: operatorAddress,
		Coin:            coin,
	}
}

// ValidateBasic runs stateless checks on the message
func (m *MsgRotate) ValidateBasic() error {
	if m.Signer.Empty() {
		return cosmos.ErrUnknownRequest("signer cannot be empty")
	}
	if m.OperatorAddress.Empty() {
		return cosmos.ErrUnknownRequest("operator address cannot be empty")
	}
	if !m.Coin.Amount.IsZero() {
		return cosmos.ErrUnknownRequest("coin amount must be zero")
	}
	return nil
}

// GetSigners defines whose signature is required
func (m *MsgRotate) GetSigners() []cosmos.AccAddress {
	return []cosmos.AccAddress{m.Signer}
}
