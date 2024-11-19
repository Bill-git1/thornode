package types

import (
	"crypto/sha256"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"google.golang.org/protobuf/proto"

	"gitlab.com/thorchain/thornode/v3/api/types"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
)

var (
	_ sdk.Msg              = &MsgSolvency{}
	_ sdk.HasValidateBasic = &MsgSolvency{}
	_ sdk.LegacyMsg        = &MsgSolvency{}
)

// NewMsgSolvency create a new MsgSolvency
func NewMsgSolvency(chain common.Chain, pubKey common.PubKey, coins common.Coins, height int64, signer cosmos.AccAddress) (*MsgSolvency, error) {
	input := fmt.Sprintf("%s|%s|%s|%d", chain, pubKey, coins, height)
	id, err := common.NewTxID(fmt.Sprintf("%X", sha256.Sum256([]byte(input))))
	if err != nil {
		return nil, fmt.Errorf("fail to create msg solvency hash")
	}
	return &MsgSolvency{
		Id:     id,
		Chain:  chain,
		PubKey: pubKey,
		Coins:  coins,
		Height: height,
		Signer: signer,
	}, nil
}

// ValidateBasic implements HasValidateBasic
// ValidateBasic is now ran in the message service router handler for messages that
// used to be routed using the external handler and only when HasValidateBasic is implemented.
// No versioning is used there.
func (m *MsgSolvency) ValidateBasic() error {
	if m.Id.IsEmpty() {
		return cosmos.ErrUnknownRequest("invalid id")
	}
	if m.Chain.IsEmpty() {
		return cosmos.ErrUnknownRequest("chain can't be empty")
	}
	if m.PubKey.IsEmpty() {
		return cosmos.ErrUnknownRequest("pubkey is empty")
	}
	if m.Height <= 0 {
		return cosmos.ErrUnknownRequest("block height is invalid")
	}
	if m.Signer.Empty() {
		return cosmos.ErrUnauthorized("invalid sender")
	}
	return nil
}

// GetSigners Implements Msg.
func (m *MsgSolvency) GetSigners() []cosmos.AccAddress {
	return []cosmos.AccAddress{m.Signer}
}

func MsgSolvencyCustomGetSigners(m proto.Message) ([][]byte, error) {
	msg, ok := m.(*types.MsgSolvency)
	if !ok {
		return nil, fmt.Errorf("can't cast as MsgSolvency: %T", m)
	}
	return [][]byte{msg.Signer}, nil
}
