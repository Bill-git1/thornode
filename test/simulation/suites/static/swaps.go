package static

import (
	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/test/simulation/actors"
	. "gitlab.com/thorchain/thornode/test/simulation/pkg/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// Swaps
////////////////////////////////////////////////////////////////////////////////////////

func Swaps() *Actor {
	a := NewActor("Swaps")

	// check every gas asset swap route
	for _, sourceChain := range common.AllChains {
		// skip thorchain and deprecated chains
		switch sourceChain {
		case common.THORChain, common.BNBChain, common.TERRAChain:
			continue
		}

		for _, targetChain := range common.AllChains {
			// skip thorchain and deprecated chains
			switch targetChain {
			case common.THORChain, common.BNBChain, common.TERRAChain:
				continue
			}

			// skip swap to self
			if sourceChain.Equals(targetChain) {
				continue
			}

			a.Children[actors.NewSwapActor(sourceChain.GetGasAsset(), targetChain.GetGasAsset())] = true
		}
	}

	return a
}
