package suites

import (
	"fmt"

	"github.com/rs/zerolog/log"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/test/simulation/actors/core"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/evm"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/thornode"
	. "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/types"
)

var SimChains = []common.Chain{
	common.AVAXChain,
	common.BASEChain,
	common.BCHChain,
	common.BTCChain,
	common.DOGEChain,
	common.ETHChain,
	common.GAIAChain,
	common.LTCChain,
	common.XRPChain,
}

////////////////////////////////////////////////////////////////////////////////////////
// Bootstrap
////////////////////////////////////////////////////////////////////////////////////////

func Bootstrap() *Actor {
	a := NewActor("Bootstrap")

	pools, err := thornode.GetPools()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to get pools")
	}

	// bootstrap pools for all chains
	count := 0
	for _, chain := range SimChains {
		count++
		a.Children[core.NewDualLPActor(chain.GetGasAsset())] = true
	}

	// create token pools
	tokenPools := NewActor("Bootstrap-TokenPools")
	for _, chain := range common.AllChains {
		if !chain.IsEVM() {
			continue
		}
		// BSC & SOL not compatible with sim tests
		if chain.Equals(common.BSCChain) || chain.Equals(common.SOLChain) {
			continue
		}
		count++

		for asset := range evm.Tokens(chain) {
			tokenPools.Children[core.NewDualLPActor(asset)] = true
		}
	}
	a.Append(tokenPools)

	// verify pools
	verify := NewActor("Bootstrap-Verify")
	verify.Ops = append(verify.Ops, func(config *OpConfig) OpResult {
		pools, err = thornode.GetPools()
		if err != nil {
			return OpResult{Finish: true, Error: err}
		}

		// all pools should be available
		for _, pool := range pools {
			if pool.Status != "Available" {
				return OpResult{
					Finish: true,
					Error:  fmt.Errorf("pool %s not available", pool.Asset),
				}
			}
		}

		// all chains should have pools
		if len(pools) != count {
			return OpResult{
				Finish: true,
				Error:  fmt.Errorf("expected %d pools, got %d", count, len(pools)),
			}
		}

		return OpResult{Finish: true}
	},
	)
	a.Append(verify)

	return a
}
