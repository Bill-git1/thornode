//go:build stagenet
// +build stagenet

package thorchain

import "gitlab.com/thorchain/thornode/v3/config"

// ADMINS hard coded admin address
var ADMINS = []string{}

func init() {
	config.Init()
	ADMINS = config.GetThornode().StagenetAdminAddresses
}
