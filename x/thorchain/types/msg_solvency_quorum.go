package types

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/protobuf/proto"

	"gitlab.com/thorchain/thornode/v3/api/types"
	"gitlab.com/thorchain/thornode/v3/common"
	cosmos "gitlab.com/thorchain/thornode/v3/common/cosmos"
)

var (
	_ sdk.Msg              = &MsgSolvencyQuorum{}
	_ sdk.HasValidateBasic = &MsgSolvencyQuorum{}
	_ sdk.LegacyMsg        = &MsgSolvencyQuorum{}
)

// NewMsgSolvencyQuorum is a constructor function for MsgSolvencyQuorum
func NewMsgSolvencyQuorum(solvency *common.QuorumSolvency, signer cosmos.AccAddress) (*MsgSolvencyQuorum, error) {
	var err error
	solvency.Solvency.Id, err = solvency.Solvency.Hash()
	if err != nil {
		return nil, fmt.Errorf("fail to create msg solvency hash: %w", err)
	}
	return &MsgSolvencyQuorum{
		QuoSolvency: solvency,
		Signer:      signer,
	}, nil
}

// ValidateBasic implements HasValidateBasic
// ValidateBasic is now ran in the message service router handler for messages that
// used to be routed using the external handler and only when HasValidateBasic is implemented.
// No versioning is used there.
func (m *MsgSolvencyQuorum) ValidateBasic() error {
	s := m.QuoSolvency.Solvency
	if s.Id.IsEmpty() {
		return cosmos.ErrUnknownRequest("invalid id")
	}
	id, err := s.Hash()
	if err != nil {
		return fmt.Errorf("fail to create msg solvency hash: %w", err)
	}
	if !s.Id.Equals(id) {
		return cosmos.ErrUnknownRequest("invalid id")
	}
	if s.Chain.IsEmpty() {
		return cosmos.ErrUnknownRequest("chain can't be empty")
	}
	if s.PubKey.IsEmpty() {
		return cosmos.ErrUnknownRequest("pubkey is empty")
	}
	if s.Height <= 0 {
		return cosmos.ErrUnknownRequest("block height is invalid")
	}
	if m.Signer.Empty() {
		return cosmos.ErrUnauthorized("invalid sender")
	}

	return nil
}

// GetSigners defines whose signature is required
func (m *MsgSolvencyQuorum) GetSigners() []cosmos.AccAddress {
	return quorumSignersCommon(m.QuoSolvency.Attestations)
}

func MsgSolvencyQuorumCustomGetSigners(m proto.Message) ([][]byte, error) {
	msg, ok := m.(*types.MsgSolvencyQuorum)
	if !ok {
		return nil, fmt.Errorf("can't cast as MsgSolvencyQuorum: %T", m)
	}

	return quorumSignersApiCommon(msg.QuoSolvency.Attestations), nil
}
