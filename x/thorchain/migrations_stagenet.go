//go:build stagenet
// +build stagenet

package thorchain

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	v2 "gitlab.com/thorchain/thornode/v3/x/thorchain/migrations/v2"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

// Migrator is a struct for handling in-place store migrations.
type Migrator struct {
	mgr *Mgrs
}

// NewMigrator returns a new Migrator.
func NewMigrator(mgr *Mgrs) Migrator {
	return Migrator{mgr: mgr}
}

// Migrate1to2 migrates from version 1 to 2.
func (m Migrator) Migrate1to2(ctx sdk.Context) error {
	// Loads the manager for this migration (we are in the x/upgrade's preblock)
	// Note, we do not require the manager loaded for this migration, but it is okay
	// to load it earlier and this is the pattern for migrations to follow.
	if err := m.mgr.LoadManagerIfNecessary(ctx); err != nil {
		return err
	}
	return v2.MigrateStore(ctx, m.mgr.storeKey)
}

// Migrate2to3 migrates from version 2 to 3.
func (m Migrator) Migrate2to3(ctx sdk.Context) error {
	return nil
}

// Migrate3to4 migrates from version 3 to 4 - adds BASE chain and router to currently
// active vault.
func (m Migrator) Migrate3to4(ctx sdk.Context) error {
	// active vault missing base
	vaultPubkey, err := common.NewPubKey("sthorpub1addwnpepqw354sg3dj8xffqjyznnmqh7rzfv528vr5wdsh3644485hzll6a2zf203v6")
	if err != nil {
		return fmt.Errorf("fail to parse vault pubkey: %w", err)
	}

	// base chain router
	baseRouterAddr, err := common.NewAddress("0xe36dcbf3c0284f756935811d9b9e80829d39bdc5")
	if err != nil {
		return fmt.Errorf("fail to parse base router address: %w", err)
	}

	// get vault
	vault, err := m.mgr.Keeper().GetVault(ctx, vaultPubkey)
	if err != nil {
		return fmt.Errorf("fail to get vault: %w", err)
	}

	// add base chain and router
	vault.Chains = append(vault.Chains, common.BASEChain.String())
	vault.Routers = append(vault.Routers, types.ChainContract{
		Chain:  common.BASEChain,
		Router: baseRouterAddr,
	})

	// store updated vault
	err = m.mgr.Keeper().SetVault(ctx, vault)
	if err != nil {
		return fmt.Errorf("fail to set vault: %w", err)
	}

	return nil
}

// Migrate3to4 migrates from version 4 to 5
func (m Migrator) Migrate4to5(ctx sdk.Context) error {
	// Loads the manager for this migration (we are in the x/upgrade's preblock)
	// Note, we do not require the manager loaded for this migration, but it is okay
	// to load it earlier and this is the pattern for migrations to follow.
	if err := m.mgr.LoadManagerIfNecessary(ctx); err != nil {
		return err
	}

	totalTCYCoin := common.NewCoin(common.TCY, cosmos.NewUint(210_000_000_00000000))
	err := m.mgr.Keeper().MintToModule(ctx, ModuleName, totalTCYCoin)
	if err != nil {
		return err
	}

	// Claims 1_800_000_00000000
	claimingModuleCoin := common.NewCoin(common.TCY, totalTCYCoin.Amount.Sub(cosmos.NewUint(1_800_000_00000000)))
	err = m.mgr.Keeper().SendFromModuleToModule(ctx, ModuleName, TCYClaimingName, common.NewCoins(claimingModuleCoin))
	if err != nil {
		return err
	}

	// 210M minus claims: 1_800_000_00000000
	treasuryCoin := common.NewCoin(common.TCY, totalTCYCoin.Amount.Sub(claimingModuleCoin.Amount))
	treasuryAddress, err := common.NewAddress("sthor1hjpct8pd9d48vyqltaqunltwx9twm57l9e8tjr")
	if err != nil {
		return err
	}

	treasuryAccAddress, err := treasuryAddress.AccAddress()
	if err != nil {
		return err
	}

	err = m.mgr.Keeper().SendFromModuleToAccount(ctx, TCYClaimingName, treasuryAccAddress, common.NewCoins(treasuryCoin))
	if err != nil {
		return err
	}

	err = setTCYClaims(ctx, m.mgr)
	if err != nil {
		return err
	}

	return m.ClearObsoleteMimirs(ctx)
}
