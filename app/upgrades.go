package app

import (
	"fmt"

	upgradetypes "cosmossdk.io/x/upgrade/types"

	"gitlab.com/thorchain/thornode/v3/app/upgrades"
	"gitlab.com/thorchain/thornode/v3/app/upgrades/standard"
)

// Upgrades list of chain upgrades
var Upgrades = []upgrades.Upgrade{
	// register non-standard upgrades here
}

// RegisterUpgradeHandlers registers the chain upgrade handlers
func (app *THORChainApp) RegisterUpgradeHandlers() {
	// setupLegacyKeyTables(&app.ParamsKeeper)
	if len(Upgrades) == 0 {
		// always have a unique upgrade registered for the current version to test in system tests
		Upgrades = append(Upgrades, standard.NewUpgrade(app.Version()))
	}

	keepers := upgrades.AppKeepers{
		ThorchainKeeper:       app.ThorchainKeeper,
		AccountKeeper:         &app.AccountKeeper,
		ParamsKeeper:          &app.ParamsKeeper,
		ConsensusParamsKeeper: &app.ConsensusParamsKeeper,
		Codec:                 app.appCodec,
		GetStoreKey:           app.GetKey,
	}
	// register all upgrade handlers
	for _, upgrade := range Upgrades {
		app.UpgradeKeeper.SetUpgradeHandler(
			upgrade.UpgradeName,
			upgrade.CreateUpgradeHandler(
				app.ModuleManager,
				app.configurator,
				&keepers,
			),
		)
	}

	upgradeInfo, err := app.UpgradeKeeper.ReadUpgradeInfoFromDisk()
	if err != nil {
		panic(fmt.Sprintf("failed to read upgrade info from disk %s", err))
	}

	if app.UpgradeKeeper.IsSkipHeight(upgradeInfo.Height) {
		return
	}

	// register store loader for current upgrade
	for _, upgrade := range Upgrades {
		if upgradeInfo.Name == upgrade.UpgradeName {
			app.SetStoreLoader(upgradetypes.UpgradeStoreLoader(upgradeInfo.Height, &upgrade.StoreUpgrades)) // nolint:gosec
			break
		}
	}
}
