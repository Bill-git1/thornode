package suites

import (
	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/test/simulation/actors/core"
	. "gitlab.com/thorchain/thornode/test/simulation/pkg/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// Savers
////////////////////////////////////////////////////////////////////////////////////////

func Savers() *Actor {
	a := NewActor("Savers")

	// add savers for all pools
	for _, chain := range common.AllChains {
		// skip thorchain and deprecated chains
		switch chain {
		case common.THORChain, common.BNBChain, common.TERRAChain:
			continue
		}

		// add saver
		saver := core.NewSaverActor(chain.GetGasAsset(), 500) // 5% of asset depth
		a.Append(saver)

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
