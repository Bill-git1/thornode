package thorchain

import (
	"github.com/cometbft/cometbft/crypto"

	"gitlab.com/thorchain/thornode/common/cosmos"
)

type nodeAddressValidatorAddressPair struct {
	nodeAddress      cosmos.AccAddress
	validatorAddress crypto.Address
}
