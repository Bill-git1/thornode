package tcysmartcontract

import (
	"gitlab.com/thorchain/thornode/v3/common"
)

func IsTCYSmartContractAddress(address common.Address) bool {
	return address.String() == TCYSmartContractAddress
}

func GetTCYSmartContractAddress() (common.Address, error) {
	return common.NewAddress(TCYSmartContractAddress)
}
