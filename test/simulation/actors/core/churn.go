package core

import (
	"fmt"
	"time"

	"gitlab.com/thorchain/thornode/v3/api/types"
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

func NewChurnActor() *Actor {
	a := &ChurnActor{
		Actor: *NewActor("Churn"),
	}
	a.Timeout = 5 * time.Minute

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

	// send mimir to unhalt churn
	node := config.NodeUsers[0]
	if !node.Acquire() {
		a.Log().Error().Msg("failed to acquire node lock")
		return OpResult{
			Continue: false,
		}
	}
	defer node.Release()
	accAddr, err := node.PubKey().GetThorAddress()
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get thor address")
		return OpResult{
			Continue: false,
		}
	}

	mimir := thorchain.NewMsgMimir("HALTCHURNING", 0, accAddr)
	txid, err := node.Thorchain.Broadcast(mimir)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to broadcast mimir")
		return OpResult{
			Continue: false,
		}
	}
	a.Log().Info().Str("txid", txid.String()).Msg("broadcasted mimir")

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
