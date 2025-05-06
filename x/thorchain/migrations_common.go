package thorchain

import (
	"sort"
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"gitlab.com/thorchain/thornode/v3/constants"
)

// Migrate4to5 migrates from version 4 to 5.
func (m Migrator) ClearObsoleteMimirs(ctx sdk.Context) error {
	// Loads the manager for this migration (we are in the x/upgrade's preblock)
	// Note, we do not require the manager loaded for this migration, but it is okay
	// to load it earlier and this is the pattern for migrations to follow.
	if err := m.mgr.LoadManagerIfNecessary(ctx); err != nil {
		return err
	}

	// Issue #2112, clearing obsolete Mimir keys.

	toClear := func(key string) bool {
		upperKey := strings.ToUpper(key)
		return (strings.Contains(upperKey, "BNB") && !strings.Contains(upperKey, "BSC")) || // Do not clear BSC-BNB keys.
			strings.Contains(upperKey, "TERRA") ||
			strings.Contains(upperKey, "YGG") ||
			strings.EqualFold(key, "MaxConfirmations") || // Only effective with -<Chain> .
			strings.EqualFold(key, "ConfMultiplierBasisPoints") || // Only effective with -<Chain> .
			strings.EqualFold(key, "SystemIncomeBurnRateBp") // Only Bps effective, not Bp.
	}

	iterNode := m.mgr.Keeper().GetNodeMimirIterator(ctx)
	defer iterNode.Close()
	for ; iterNode.Valid(); iterNode.Next() {
		key := trimKeyPrefix(iterNode.Key())

		if !toClear(key) {
			continue
		}

		// As with PurgeOperationalNodeMimirs,
		// not emitting individual EventSetNodeMimir events.
		m.mgr.Keeper().DeleteNodeMimirs(ctx, key)
	}

	iter := m.mgr.Keeper().GetMimirIterator(ctx)
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		key := trimKeyPrefix(iter.Key())

		if !toClear(key) {
			continue
		}

		if err := m.mgr.Keeper().DeleteMimir(ctx, key); err != nil {
			ctx.Logger().Error("fail to delete mimir", "key", key, "error", err)
			continue
		}

		// As with Admin key deletion, emit a deletion event.
		mimirEvent := NewEventSetMimir(strings.ToUpper(key), "-1")
		if err := m.mgr.EventMgr().EmitEvent(ctx, mimirEvent); err != nil {
			ctx.Logger().Error("fail to emit set_mimir event", "error", err)
		}
	}

	return nil
}

func (m Migrator) UpdateMimirsForBlockTime(ctx sdk.Context) {
	keeper := m.mgr.Keeper()

	// Get constants derived from ThorchainBlockTime
	c := constants.BlockTimeConstants()

	keys := make([]constants.ConstantName, 0, len(c))
	// analyze-ignore(map-iteration)
	for k := range c {
		keys = append(keys, k)
	}

	// Sort the keys to ensure consistent ordering
	sort.Slice(keys, func(i, j int) bool {
		return keys[i] < keys[j]
	})

	for _, k := range keys {
		mimirKey := k.String()
		existing, _ := keeper.GetMimir(ctx, mimirKey)
		if existing == -1 {
			// Mimir not set, don't need to set since it will come from the constants
			continue
		}
		keeper.SetMimir(ctx, mimirKey, c[k])
	}
}
