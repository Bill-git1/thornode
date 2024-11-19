//go:build stagenet
// +build stagenet

package thorchain

import "gitlab.com/thorchain/thornode/v3/common/cosmos"

func migrateStoreV136(ctx cosmos.Context, mgr *Mgrs) {
	defer func() {
		if err := recover(); err != nil {
			ctx.Logger().Error("fail to migrate store to v136", "error", err)
		}
	}()

	restoreTotalCollateral(ctx, mgr)
}
