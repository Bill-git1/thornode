package types

import (
	sdk "github.com/cosmos/cosmos-sdk/types"

	"gitlab.com/thorchain/thornode/v3/common"
	cosmos "gitlab.com/thorchain/thornode/v3/common/cosmos"
)

var (
	_ sdk.Msg              = &MsgManageTHORName{}
	_ sdk.HasValidateBasic = &MsgManageTHORName{}
	_ sdk.LegacyMsg        = &MsgManageTHORName{}
)

// NewMsgManageTHORName create a new instance of MsgManageTHORName
func NewMsgManageTHORName(name string, chain common.Chain, addr common.Address, coin common.Coin, exp int64, asset common.Asset, owner, signer cosmos.AccAddress) *MsgManageTHORName {
	return &MsgManageTHORName{
		Name:              name,
		Chain:             chain,
		Address:           addr,
		Coin:              coin,
		ExpireBlockHeight: exp,
		PreferredAsset:    asset,
		Owner:             owner,
		Signer:            signer,
	}
}

// ValidateBasic runs stateless checks on the message
func (m *MsgManageTHORName) ValidateBasic() error {
	// validate n
	if m.Signer.Empty() {
		return cosmos.ErrInvalidAddress(m.Signer.String())
	}
	if m.Chain.IsEmpty() {
		return cosmos.ErrUnknownRequest("chain can't be empty")
	}
	if m.Address.IsEmpty() {
		return cosmos.ErrUnknownRequest("address can't be empty")
	}
	if !m.Address.IsChain(m.Chain) {
		return cosmos.ErrUnknownRequest("address and chain must match")
	}
	if !m.Coin.IsRune() {
		return cosmos.ErrUnknownRequest("coin must be native rune")
	}
	return nil
}

// GetSigners defines whose signature is required
func (m *MsgManageTHORName) GetSigners() []cosmos.AccAddress {
	return []cosmos.AccAddress{m.Signer}
}
