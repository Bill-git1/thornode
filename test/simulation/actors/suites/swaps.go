package suites

import (
	rand "math/rand/v2"

	"gitlab.com/thorchain/thornode/v3/common"
	acommon "gitlab.com/thorchain/thornode/v3/test/simulation/actors/common"
	"gitlab.com/thorchain/thornode/v3/test/simulation/actors/core"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/evm"
	. "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// MemolessSwaps
////////////////////////////////////////////////////////////////////////////////////////

func MemolessSwaps(rng *rand.Rand) *Actor {
	a := NewActor("Memoless Swaps", rng)

	// memoless swaps
	simChains1e8 := []common.Chain{}
	for _, chain := range acommon.SimChains {
		if chain.GetGasAssetDecimal() == 8 {
			simChains1e8 = append(simChains1e8, chain)
		}
	}

	for _, t := range []core.MemolessType{core.MemolessTypeRef, core.MemolessTypeAmount} {
		for _, chain := range simChains1e8 {
			// choose a random (other) pool to swap to
			j := rng.IntN(len(acommon.SimChains))
			for chain.Equals(acommon.SimChains[j]) {
				j = rng.IntN(len(acommon.SimChains))
			}
			a.Children[core.NewSwapMemolessActor(chain.GetGasAsset(), acommon.SimChains[j].GetGasAsset(), t, rng)] = true
		}
	}

	return a
}

////////////////////////////////////////////////////////////////////////////////////////
// Swaps
////////////////////////////////////////////////////////////////////////////////////////

func Swaps(rng *rand.Rand) *Actor {
	a := NewActor("Swaps", rng)

	// gather all pools we expect to swap through
	swapPools := []common.Asset{}
	for _, chain := range acommon.SimChains {
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
		// always swap SOL <> BTC
		if pool.Chain == common.SOLChain {
			a.Children[core.NewSwapActor(pool, common.BTCAsset, rng)] = true
			a.Children[core.NewSwapActor(common.BTCAsset, pool, rng)] = true
		}

		// choose a random (other) pool to swap to
		j := rng.IntN(len(swapPools))
		for j == i {
			j = rng.IntN(len(swapPools))
		}
		a.Children[core.NewSwapActor(pool, swapPools[j], rng)] = true

		// choose a new random (other) pool to swap from
		j = rng.IntN(len(swapPools))
		for j == i {
			j = rng.IntN(len(swapPools))
		}
		a.Children[core.NewSwapActor(swapPools[j], pool, rng)] = true
	}

	return a
}
