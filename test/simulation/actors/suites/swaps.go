package suites

import (
	"math/rand"

	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/test/simulation/actors/core"
	"gitlab.com/thorchain/thornode/test/simulation/pkg/evm"
	. "gitlab.com/thorchain/thornode/test/simulation/pkg/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// Swaps
////////////////////////////////////////////////////////////////////////////////////////

func Swaps() *Actor {
	a := NewActor("Swaps")

	// gather all pools we expect to swap through
	swapPools := []common.Asset{}
	for _, chain := range common.AllChains {
		// skip thorchain and deprecated chains
		switch chain {
		case common.THORChain, common.BNBChain, common.TERRAChain:
			continue
		}

		swapPools = append(swapPools, chain.GetGasAsset())

		// add tokens to swap pools
		if !chain.IsEVM() {
			continue
		}
		for asset := range evm.Tokens(chain) {
			swapPools = append(swapPools, asset)
		}
	}

	// swap from each pool to one random one
	for i, pool := range swapPools {
		// choose a random (other) pool to swap to
		j := rand.Intn(len(swapPools))
		for j == i {
			j = rand.Intn(len(swapPools))
		}
		a.Children[core.NewSwapActor(pool, swapPools[j])] = true

		// choose a new random (other) pool to swap from
		j = rand.Intn(len(swapPools))
		for j == i {
			j = rand.Intn(len(swapPools))
		}
		a.Children[core.NewSwapActor(swapPools[j], pool)] = true
	}

	return a
}
