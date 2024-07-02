package types

import (
	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/common/cosmos"
	"gitlab.com/thorchain/thornode/constants"
)

// NewMsgRunePoolDeposit create new MsgRunePoolDeposit message
func NewMsgRunePoolDeposit(signer cosmos.AccAddress, tx common.Tx) *MsgRunePoolDeposit {
	return &MsgRunePoolDeposit{
		Signer: signer,
		Tx:     tx,
	}
}

// Route should return the router key of the module
func (m *MsgRunePoolDeposit) Route() string { return RouterKey }

// Type should return the action
func (m MsgRunePoolDeposit) Type() string { return "rune_pool_deposit" }

// ValidateBasic runs stateless checks on the message
func (m *MsgRunePoolDeposit) ValidateBasic() error {
	if !m.Tx.Chain.Equals(common.THORChain) {
		return cosmos.ErrUnauthorized("chain must be THORChain")
	}
	if len(m.Tx.Coins) != 1 {
		return cosmos.ErrInvalidCoins("coins must be length 1 (RUNE)")
	}
	if !m.Tx.Coins[0].Asset.Chain.IsTHORChain() {
		return cosmos.ErrInvalidCoins("coin chain must be THORChain")
	}
	if !m.Tx.Coins[0].Asset.IsNativeRune() {
		return cosmos.ErrInvalidCoins("coin must be RUNE")
	}
	if m.Signer.Empty() {
		return cosmos.ErrInvalidAddress("signer must not be empty")
	}
	if m.Tx.Coins[0].Amount.IsZero() {
		return cosmos.ErrUnknownRequest("coins must not be zero")
	}
	return nil
}

// GetSignBytes encodes the message for signing
func (m *MsgRunePoolDeposit) GetSignBytes() []byte {
	return cosmos.MustSortJSON(ModuleCdc.MustMarshalJSON(m))
}

// GetSigners defines whose signature is required
func (m *MsgRunePoolDeposit) GetSigners() []cosmos.AccAddress {
	return []cosmos.AccAddress{m.Signer}
}

// NewMsgRunePoolWithdraw create new MsgRunePoolWithdraw message
func NewMsgRunePoolWithdraw(address common.Address, basisPoints cosmos.Uint, affAddr common.Address, affBps cosmos.Uint, signer cosmos.AccAddress) *MsgRunePoolWithdraw {
	return &MsgRunePoolWithdraw{
		Signer:               signer,
		BasisPoints:          basisPoints,
		AffiliateAddress:     affAddr,
		AffiliateBasisPoints: affBps,
	}
}

// Route should return the router key of the module
func (m *MsgRunePoolWithdraw) Route() string { return RouterKey }

// Type should return the action
func (m MsgRunePoolWithdraw) Type() string { return "rune_pool_withdraw" }

// ValidateBasic runs stateless checks on the message
func (m *MsgRunePoolWithdraw) ValidateBasic() error {
	if m.Signer.Empty() {
		return cosmos.ErrInvalidAddress("signer cannot be empty")
	}
	if m.BasisPoints.IsZero() || m.BasisPoints.GT(cosmos.NewUint(constants.MaxBasisPts)) {
		return cosmos.ErrUnknownRequest("invalid basis points")
	}
	return nil
}

// GetSignBytes encodes the message for signing
func (m *MsgRunePoolWithdraw) GetSignBytes() []byte {
	return cosmos.MustSortJSON(ModuleCdc.MustMarshalJSON(m))
}

// GetSigners defines whose signature is required
func (m *MsgRunePoolWithdraw) GetSigners() []cosmos.AccAddress {
	return []cosmos.AccAddress{m.Signer}
}
