package suites

import (
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/test/simulation/actors/core"
	. "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// Savers
////////////////////////////////////////////////////////////////////////////////////////

func Savers() *Actor {
	a := NewActor("Savers")

	// add savers for all pools
	for _, chain := range common.AllChains {
		if chain == common.THORChain {
			continue
		}

		// add saver
		saver := core.NewSaverActor(chain.GetGasAsset(), 500) // 5% of asset depth
		a.Children[saver] = true

		// TODO: uncomment when non-gas asset savers are allowed
		// add token savers
		// if !chain.IsEVM() {
		// continue
		// }
		// for asset := range evm.Tokens(chain) {
		// saver := actors.NewSaverActor(asset, 500) // 5% of asset depth
		// a.Append(saver)
		// }
	}

	return a
}
