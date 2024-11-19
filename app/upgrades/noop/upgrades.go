package noop

import (
	"context"
	"fmt"

	storetypes "cosmossdk.io/store/types"
	upgradetypes "cosmossdk.io/x/upgrade/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"

	"gitlab.com/thorchain/thornode/v3/app/upgrades"
	keeperv1 "gitlab.com/thorchain/thornode/v3/x/thorchain/keeper/v1"
)

const UpgradeName = "0.0.0"

// NewUpgrade constructor
func NewUpgrade(semver string) upgrades.Upgrade {
	return upgrades.Upgrade{
		UpgradeName:          semver,
		CreateUpgradeHandler: CreateUpgradeHandler,
		StoreUpgrades: storetypes.StoreUpgrades{
			Added:   []string{},
			Deleted: []string{},
		},
	}
}

func CreateUpgradeHandler(
	mm upgrades.ModuleManager,
	configurator module.Configurator,
	ak *upgrades.AppKeepers,
) upgradetypes.UpgradeHandler {
	return func(goCtx context.Context, plan upgradetypes.Plan, fromVM module.VersionMap) (module.VersionMap, error) {
		// Perform SDK module migrations
		vm, err := mm.RunMigrations(goCtx, configurator, fromVM)
		if err != nil {
			return vm, err
		}

		ctx := sdk.UnwrapSDKContext(goCtx)

		// Active validator versions need to be updated since consensus
		// on the new version is required to resume the chain.
		// This is a THORChain specific upgrade step that should be
		// done in every upgrade handler.
		if err = keeperv1.UpdateActiveValidatorVersions(ctx, ak.ThorchainKeeper, UpgradeName); err != nil {
			return vm, fmt.Errorf("failed to update active validator versions: %w", err)
		}

		return vm, nil
	}
}
