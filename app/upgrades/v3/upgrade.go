package v3

import (
	"context"
	"fmt"

	storetypes "cosmossdk.io/store/types"
	upgradetypes "cosmossdk.io/x/upgrade/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	consensustypes "github.com/cosmos/cosmos-sdk/x/consensus/types"
	minttypes "github.com/cosmos/cosmos-sdk/x/mint/types"
	paramstypes "github.com/cosmos/cosmos-sdk/x/params/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"gitlab.com/thorchain/thornode/v3/app/upgrades"
	keeperv1 "gitlab.com/thorchain/thornode/v3/x/thorchain/keeper/v1"
)

// UpgradeName is the name of this specific software upgrade used on-chain.
const UpgradeName = "3.0.0"

// NewUpgrade constructor
func NewUpgrade() upgrades.Upgrade {
	return upgrades.Upgrade{
		UpgradeName:          UpgradeName,
		CreateUpgradeHandler: CreateUpgradeHandler,
		StoreUpgrades: storetypes.StoreUpgrades{
			Added: []string{
				consensustypes.ModuleName, // add consensus module store
			},
			Deleted: []string{},
		},
	}
}

func CreateUpgradeHandler(
	mm upgrades.ModuleManager,
	configurator module.Configurator,
	ak *upgrades.AppKeepers,
) upgradetypes.UpgradeHandler {
	return func(goCtx context.Context, _ upgradetypes.Plan, fromVM module.VersionMap) (module.VersionMap, error) {
		// We do not set module versions in v2.136.0 or earlier, must set them manually
		fromVM["auth"] = 2
		fromVM["bank"] = 2
		fromVM["genutil"] = 1
		fromVM["params"] = 1
		fromVM["thorchain"] = 1
		fromVM["upgrade"] = 1

		// set param key table for params module migration
		// ref: https://github.com/cosmos/cosmos-sdk/pull/12363/files
		for _, subspace := range ak.ParamsKeeper.GetSubspaces() {
			subspace := subspace
			var keyTable paramstypes.KeyTable

			switch subspace.Name() {

			// cosmos-sdk modules
			case authtypes.ModuleName:
				keyTable = authtypes.ParamKeyTable() //nolint:staticcheck
			case banktypes.ModuleName:
				keyTable = banktypes.ParamKeyTable() //nolint:staticcheck
			case stakingtypes.ModuleName:
				keyTable = stakingtypes.ParamKeyTable() //nolint:staticcheck
			case minttypes.ModuleName:
				keyTable = minttypes.ParamKeyTable() //nolint:staticcheck
			}

			if !subspace.HasKeyTable() {
				subspace.WithKeyTable(keyTable)
			}
		}

		// Active validator versions need to be updated since consensus
		// on the new version is required to resume the chain.
		// This is a THORChain specific upgrade step that should be
		// done in every upgrade handler and before any thorchain module migrations.
		ctx := sdk.UnwrapSDKContext(goCtx)
		if err := keeperv1.UpdateActiveValidatorVersions(ctx, ak.ThorchainKeeper, UpgradeName); err != nil {
			return nil, fmt.Errorf("failed to update active validator versions: %w", err)
		}

		// Perform SDK module migrations
		return mm.RunMigrations(goCtx, configurator, fromVM)
	}
}
