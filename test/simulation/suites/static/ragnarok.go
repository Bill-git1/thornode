package static

import (
	"fmt"

	"gitlab.com/thorchain/thornode/test/simulation/actors"
	"gitlab.com/thorchain/thornode/test/simulation/pkg/thornode"
	. "gitlab.com/thorchain/thornode/test/simulation/pkg/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// Ragnarok
////////////////////////////////////////////////////////////////////////////////////////

func Ragnarok() *Actor {
	a := &Actor{
		Name: "Ragnarok",
	}

	// ragnarok all pools
	for _, chain := range Chains {
		a.Children = append(a.Children, actors.NewRagnarokPoolActor(chain.GetGasAsset()))
	}

	// verify pool removals
	a.Append(&Actor{
		Name: "Ragnarok-Verify",
		Ops: []Op{
			func(config *OpConfig) OpResult {
				pools, err := thornode.GetPools()
				if err != nil {
					return OpResult{Finish: true, Error: err}
				}

				// no chains should have pools
				if len(pools) != 0 {
					return OpResult{
						Finish: true,
						Error:  fmt.Errorf("found %d pools after ragnarok", len(pools)),
					}
				}

				return OpResult{Finish: true}
			},
		},
	})

	return a
}
