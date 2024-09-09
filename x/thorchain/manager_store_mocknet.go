//go:build mocknet && !regtest
// +build mocknet,!regtest

package thorchain

import "gitlab.com/thorchain/thornode/common/cosmos"

func migrateStoreV136(ctx cosmos.Context, mgr *Mgrs) {}
