package core

import (
	"fmt"
	rand "math/rand/v2"
	"time"

	"gitlab.com/thorchain/thornode/v3/api/types"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/thornode"
	. "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/types"
	"gitlab.com/thorchain/thornode/v3/x/thorchain"
)

////////////////////////////////////////////////////////////////////////////////////////
// ChurnActor
////////////////////////////////////////////////////////////////////////////////////////

// ChurnActor assumes that the mocknet was started with multiple nodes.
type ChurnActor struct {
	Actor
}

func NewChurnActor(rng *rand.Rand) *Actor {
	a := &ChurnActor{
		Actor: *NewActor("Churn", rng),
	}
	a.Timeout = 35 * time.Minute // Match XMR post-churn timeout. FROST keygen can take up to 300s plus churn/vault rotation time.

	a.Ops = append(a.Ops, a.startChurn)
	a.Ops = append(a.Ops, a.waitForChurnComplete)

	return &a.Actor
}

////////////////////////////////////////////////////////////////////////////////////////
// Ops
////////////////////////////////////////////////////////////////////////////////////////

func (a *ChurnActor) startChurn(config *OpConfig) OpResult {
	// should be one active node
	nodes, err := thornode.GetNodes()
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get nodes")
		return OpResult{
			Continue: false,
		}
	}
	activeNodes := map[string]bool{}
	for _, node := range nodes {
		if node.Status == types.NodeStatus_Active.String() {
			activeNodes[node.NodeAddress] = true
		}
	}
	if len(activeNodes) > 1 {
		return OpResult{
			Continue: false,
			Finish:   true,
			Error:    fmt.Errorf("should be one active node before churn"),
		}
	}

	if result := waitForXMRFrostChurnReadiness(a.Log(), config); !result.Continue {
		return result
	}

	// Broadcast the same churn Mimirs used by the XMR post-churn actor. The
	// plain bootstrap cluster starts with a one-node active vault, so unhalting
	// alone is not enough for tests that need a multi-member vault.
	node := config.NodeUsers[0]
	if !node.Acquire() {
		a.Log().Error().Msg("failed to acquire node lock")
		return OpResult{
			Continue: false,
		}
	}
	defer node.Release()
	accAddr, err := node.PubKey(common.THORChain).GetThorAddress()
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get thor address")
		return OpResult{
			Continue: false,
		}
	}

	for _, item := range []struct {
		key   string
		value int64
	}{
		{key: "NUMBEROFNEWNODESPERCHURN", value: xmrPostChurnNewNodesPerChurn},
		{key: "CHURNRETRYINTERVAL", value: xmrPostChurnChurnRetryInterval},
		{key: "HALTCHURNING", value: 0},
	} {
		mimir := thorchain.NewMsgMimir(item.key, item.value, accAddr)
		txid, err := node.Thorchain.Broadcast(mimir)
		if err != nil {
			a.Log().Error().Err(err).Str("key", item.key).Int64("value", item.value).Msg("failed to broadcast churn mimir")
			return OpResult{
				Continue: false,
			}
		}
		a.Log().Info().
			Str("txid", txid.String()).
			Str("key", item.key).
			Int64("value", item.value).
			Msg("broadcasted churn mimir")
	}

	return OpResult{
		Continue: true,
	}
}

func (a *ChurnActor) waitForChurnComplete(config *OpConfig) OpResult {
	network, err := thornode.GetNetwork()
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get network")
		return OpResult{
			Continue: false,
		}
	}

	nodes, err := thornode.GetNodes()
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get nodes")
		return OpResult{
			Continue: false,
		}
	}
	activeNodes := 0
	for _, node := range nodes {
		if node.Status == types.NodeStatus_Active.String() {
			activeNodes++
		}
	}
	if activeNodes <= 1 {
		a.Log().Info().Msg("waiting for churn to start")
		return OpResult{
			Continue: false,
		}
	}

	if !network.VaultsMigrating {
		a.Log().Info().Msg("churn ended")
		return OpResult{
			Continue: true,
		}
	}

	a.Log().Info().Msg("waiting for churn to end")
	return OpResult{
		Continue: false,
	}
}
