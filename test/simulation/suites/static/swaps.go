package static

import (
	"gitlab.com/thorchain/thornode/test/simulation/actors"
	. "gitlab.com/thorchain/thornode/test/simulation/pkg/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// Swaps
////////////////////////////////////////////////////////////////////////////////////////

func Swaps() *Actor {
	a := &Actor{
		Name: "Swaps",
	}

	// check every gas asset swap route
	for _, sourceChain := range Chains {
		for _, targetChain := range Chains {
			// skip swap to self
			if sourceChain.Equals(targetChain) {
				continue
			}

			a.Children = append(
				a.Children, actors.NewSwapActor(sourceChain.GetGasAsset(), targetChain.GetGasAsset()),
			)
		}
	}

	return a
}
