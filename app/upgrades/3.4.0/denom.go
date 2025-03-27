package v3_4_0

import (
	storetypes "cosmossdk.io/store/types"

	"gitlab.com/thorchain/thornode/v3/app/upgrades"
	"gitlab.com/thorchain/thornode/v3/app/upgrades/standard"
	denomtypes "gitlab.com/thorchain/thornode/v3/x/denom/types"
)

// NewUpgrade constructor
func NewUpgrade() upgrades.Upgrade {
	return upgrades.Upgrade{
		UpgradeName:          "3.4.0",
		CreateUpgradeHandler: standard.CreateUpgradeHandler,
		StoreUpgrades: storetypes.StoreUpgrades{
			Added: []string{
				denomtypes.ModuleName,
			},
			Deleted: []string{},
		},
	}
}
