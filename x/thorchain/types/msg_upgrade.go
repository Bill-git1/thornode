package types

import (
	"github.com/blang/semver"
	"gitlab.com/thorchain/thornode/common/cosmos"
)

// NewMsgProposeUpgrade is a constructor function for NewMsgProposeUpgrade
func NewMsgProposeUpgrade(name string, height int64, info string, signer cosmos.AccAddress) *MsgProposeUpgrade {
	return &MsgProposeUpgrade{
		Name: name,
		Upgrade: Upgrade{
			Height: height,
			Info:   info,
		},
		Signer: signer,
	}
}

// Route should return the route key of the module
func (m *MsgProposeUpgrade) Route() string { return RouterKey }

// Type should return the action
func (m MsgProposeUpgrade) Type() string { return "propose_upgrade" }

// ValidateBasic runs stateless checks on the message
func (m *MsgProposeUpgrade) ValidateBasic() error {
	if m.Signer.Empty() {
		return cosmos.ErrInvalidAddress(m.Signer.String())
	}
	if len(m.Name) == 0 {
		return cosmos.ErrUnknownRequest("name cannot be empty")
	}
	if len(m.Name) > 100 {
		return cosmos.ErrUnknownRequest("name cannot be longer than 100 characters")
	}
	if _, err := semver.Parse(m.Name); err != nil {
		return cosmos.ErrUnknownRequest("name is not a valid semver")
	}
	if len(m.Upgrade.Info) > 2500 {
		return cosmos.ErrUnknownRequest("info cannot be longer than 2500 characters")
	}
	if m.Upgrade.Height == 0 {
		return cosmos.ErrUnknownRequest("height cannot be zero")
	}
	return nil
}

// GetSignBytes encodes the message for signing
func (m *MsgProposeUpgrade) GetSignBytes() []byte {
	return cosmos.MustSortJSON(ModuleCdc.MustMarshalJSON(m))
}

// GetSigners defines whose signature is required
func (m *MsgProposeUpgrade) GetSigners() []cosmos.AccAddress {
	return []cosmos.AccAddress{m.Signer}
}

// NewMsgApproveUpgrade is a constructor function for NewMsgApproveUpgrade
func NewMsgApproveUpgrade(name string, signer cosmos.AccAddress) *MsgApproveUpgrade {
	return &MsgApproveUpgrade{
		Name:   name,
		Signer: signer,
	}
}

// Route should return the route key of the module
func (m *MsgApproveUpgrade) Route() string { return RouterKey }

// Type should return the action
func (m MsgApproveUpgrade) Type() string { return "approve_upgrade" }

// ValidateBasic runs stateless checks on the message
func (m *MsgApproveUpgrade) ValidateBasic() error {
	if m.Signer.Empty() {
		return cosmos.ErrInvalidAddress(m.Signer.String())
	}
	if m.Name == "" {
		return cosmos.ErrUnknownRequest("name cannot be empty")
	}
	return nil
}

// GetSignBytes encodes the message for signing
func (m *MsgApproveUpgrade) GetSignBytes() []byte {
	return cosmos.MustSortJSON(ModuleCdc.MustMarshalJSON(m))
}

// GetSigners defines whose signature is required
func (m *MsgApproveUpgrade) GetSigners() []cosmos.AccAddress {
	return []cosmos.AccAddress{m.Signer}
}

// NewMsgRejectUpgrade is a constructor function for NewMsgRejectUpgrade
func NewMsgRejectUpgrade(name string, signer cosmos.AccAddress) *MsgRejectUpgrade {
	return &MsgRejectUpgrade{
		Name:   name,
		Signer: signer,
	}
}

// Route should return the route key of the module
func (m *MsgRejectUpgrade) Route() string { return RouterKey }

// Type should return the action
func (m MsgRejectUpgrade) Type() string { return "reject_upgrade" }

// ValidateBasic runs stateless checks on the message
func (m *MsgRejectUpgrade) ValidateBasic() error {
	if m.Signer.Empty() {
		return cosmos.ErrInvalidAddress(m.Signer.String())
	}
	if m.Name == "" {
		return cosmos.ErrUnknownRequest("name cannot be empty")
	}
	return nil
}

// GetSignBytes encodes the message for signing
func (m *MsgRejectUpgrade) GetSignBytes() []byte {
	return cosmos.MustSortJSON(ModuleCdc.MustMarshalJSON(m))
}

// GetSigners defines whose signature is required
func (m *MsgRejectUpgrade) GetSigners() []cosmos.AccAddress {
	return []cosmos.AccAddress{m.Signer}
}
