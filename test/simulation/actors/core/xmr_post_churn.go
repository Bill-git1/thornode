package core

import (
	"fmt"
	"math/big"
	rand "math/rand/v2"
	"time"

	atypes "gitlab.com/thorchain/thornode/v3/api/types"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
	openapi "gitlab.com/thorchain/thornode/v3/openapi/gen"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/thornode"
	. "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/types"
	"gitlab.com/thorchain/thornode/v3/x/thorchain"
)

type XMRPostChurnActor struct {
	SwapActor

	inboundBefore common.Address
	inboundAfter  common.Address
}

const (
	xmrPostChurnNewNodesPerChurn = int64(3)
	xmrPostChurnLeadBlocks       = int64(10)
	// Keep this above the 300s FROST keygen timeout, but low enough that a
	// missed local keygen gets a retry before the actor deadline. This matches
	// the mocknet ChurnRetryInterval override; 360 would also clear the timeout,
	// but using one value avoids a misleading reviewer-only delta.
	xmrPostChurnRetryInterval = int64(365)
)

func NewXMRPostChurnActor(rng *rand.Rand) *Actor {
	a := &XMRPostChurnActor{
		SwapActor: SwapActor{
			Actor: *NewActor("XMRPostChurn BTC.BTC => XMR.XMR", rng),
			from:  common.BTCAsset,
			to:    common.XMRAsset,
		},
	}
	a.Timeout = 35 * time.Minute

	a.Ops = append(a.Ops, a.captureActiveInbound)
	a.Ops = append(a.Ops, a.startChurn)
	a.Ops = append(a.Ops, a.waitForVaultRotation)
	a.Ops = append(a.Ops, a.acquireUser)
	a.Ops = append(a.Ops, a.getQuote)
	a.Ops = append(a.Ops, a.sendSwap)
	a.Ops = append(a.Ops, a.verifyOutbound)

	return &a.Actor
}

func (a *XMRPostChurnActor) captureActiveInbound(config *OpConfig) OpResult {
	inboundAddress, _, err := thornode.GetInboundAddress(common.XMRChain)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get active XMR inbound address")
		return OpResult{Continue: false}
	}

	a.inboundBefore = inboundAddress
	a.Log().Info().Stringer("inbound_before", inboundAddress).Msg("captured pre-churn XMR inbound address")
	return OpResult{Continue: true}
}

func (a *XMRPostChurnActor) startChurn(config *OpConfig) OpResult {
	nodes, err := thornode.GetNodes()
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get nodes")
		return OpResult{Continue: false}
	}

	activeNodes := map[string]bool{}
	for _, node := range nodes {
		if node.Status == atypes.NodeStatus_Active.String() {
			activeNodes[node.NodeAddress] = true
		}
	}
	if len(activeNodes) > 1 {
		a.Log().Info().Msg("cluster already churned; waiting for XMR vault rotation")
		return OpResult{Continue: true}
	}

	if result := waitForXMRFrostChurnReadiness(a.Log(), config); !result.Continue {
		return result
	}

	node := config.NodeUsers[0]
	if !node.Acquire() {
		a.Log().Warn().Msg("failed to acquire node lock")
		return OpResult{Continue: false}
	}
	defer node.Release()

	accAddr, err := node.PubKey(common.THORChain).GetThorAddress()
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get node thor address")
		return OpResult{Continue: false}
	}

	currentHeight, err := node.Thorchain.GetBlockHeight()
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get thorchain block height")
		return OpResult{Continue: false}
	}
	vaults, err := thornode.GetVaults()
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get active vaults")
		return OpResult{Continue: false}
	}
	lastChurnHeight, ok := xmrPostChurnLastActiveVaultHeight(vaults)
	if !ok {
		a.Log().Error().Msg("failed to find active vault status height")
		return OpResult{Continue: false}
	}
	churnInterval := xmrPostChurnScheduledInterval(currentHeight, lastChurnHeight)
	txid, err := a.broadcastMimir(node, accAddr, "CHURNINTERVAL", churnInterval)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to broadcast churn interval mimir")
		return OpResult{Continue: false}
	}
	a.Log().Info().
		Int64("value", churnInterval).
		Int64("current_height", currentHeight).
		Int64("last_churn_height", lastChurnHeight).
		Str("txid", txid.String()).
		Msg("broadcasted churn interval mimir")

	txid, err = a.broadcastMimir(node, accAddr, constants.ChurnRetryInterval.String(), xmrPostChurnRetryInterval)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to broadcast churn retry interval mimir")
		return OpResult{Continue: false}
	}
	a.Log().Info().
		Int64("value", xmrPostChurnRetryInterval).
		Str("txid", txid.String()).
		Msg("broadcasted churn retry interval mimir")

	txid, err = a.broadcastMimir(node, accAddr, constants.NumberOfNewNodesPerChurn.String(), xmrPostChurnNewNodesPerChurn)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to broadcast node expansion mimir")
		return OpResult{Continue: false}
	}
	a.Log().Info().
		Int64("value", xmrPostChurnNewNodesPerChurn).
		Str("txid", txid.String()).
		Msg("broadcasted node expansion mimir")

	txid, err = a.broadcastMimir(node, accAddr, "HaltChurning", 0)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to broadcast churn mimir")
		return OpResult{Continue: false}
	}

	a.Log().Info().Str("txid", txid.String()).Msg("broadcasted churn mimir")
	return OpResult{Continue: true}
}

func (a *XMRPostChurnActor) waitForVaultRotation(config *OpConfig) OpResult {
	nodes, err := thornode.GetNodes()
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get nodes")
		return OpResult{Continue: false}
	}

	activeNodes := 0
	statusCounts := map[string]int{}
	for _, node := range nodes {
		statusCounts[node.Status]++
		if node.Status == atypes.NodeStatus_Active.String() {
			activeNodes++
		}
	}
	if activeNodes <= 1 {
		a.Log().Info().
			Int("active_nodes", activeNodes).
			Interface("node_statuses", statusCounts).
			Msg("waiting for churn to activate additional nodes")
		return OpResult{Continue: false}
	}

	inboundAddress, _, err := thornode.GetInboundAddress(common.XMRChain)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get XMR inbound address")
		return OpResult{Continue: false}
	}
	if inboundAddress.Equals(a.inboundBefore) {
		a.Log().Info().Stringer("inbound_address", inboundAddress).Msg("waiting for XMR inbound address rotation")
		return OpResult{Continue: false}
	}

	vaults, err := thornode.GetVaults()
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get asgard vaults")
		return OpResult{Continue: false}
	}
	if !xmrVaultRotationReady(vaults, a.inboundBefore, inboundAddress, activeNodes) {
		a.Log().Info().Stringer("inbound_address", inboundAddress).Msg("waiting for active XMR vault funding")
		return OpResult{Continue: false}
	}

	snapshot, err := getActiveXMRVaultBalance(config, nil)
	if err != nil {
		a.Log().Info().Err(err).Msg("waiting for active XMR vault spendable balance")
		return OpResult{Continue: false}
	}
	if !xmrPostChurnReady(vaults, a.inboundBefore, inboundAddress, activeNodes, snapshot) {
		a.Log().Info().
			Stringer("vault_pubkey", snapshot.pubKey).
			Stringer("total", snapshot.total).
			Stringer("spendable", snapshot.spendable).
			Msg("waiting for active XMR vault spendable balance")
		return OpResult{Continue: false}
	}

	a.inboundAfter = inboundAddress
	a.Log().Info().
		Stringer("inbound_before", a.inboundBefore).
		Stringer("inbound_after", a.inboundAfter).
		Msg("XMR active vault rotated")
	return OpResult{Continue: true}
}

func xmrPostChurnReady(vaults []openapi.Vault, inboundBefore, inboundAfter common.Address, activeNodes int, snapshot xmrVaultBalanceSnapshot) bool {
	return xmrVaultRotationReady(vaults, inboundBefore, inboundAfter, activeNodes) && !snapshot.spendable.IsZero()
}

func xmrVaultRotationReady(vaults []openapi.Vault, inboundBefore, inboundAfter common.Address, activeNodes int) bool {
	if activeNodes <= 1 {
		return false
	}
	if inboundAfter.IsEmpty() || inboundAfter.Equals(inboundBefore) {
		return false
	}
	for _, vault := range vaults {
		if vault.Status != thorchain.ActiveVault.String() {
			continue
		}
		if !vaultHasAddress(vault, common.XMRChain, inboundAfter) {
			continue
		}
		if !vaultHasPositiveCoin(vault, common.XMRAsset) {
			continue
		}
		return true
	}
	return false
}

func xmrPostChurnScheduledInterval(currentHeight, lastChurnHeight int64) int64 {
	interval := currentHeight - lastChurnHeight + xmrPostChurnLeadBlocks
	if interval < xmrPostChurnLeadBlocks {
		return xmrPostChurnLeadBlocks
	}
	return interval
}

func xmrPostChurnLastActiveVaultHeight(vaults []openapi.Vault) (int64, bool) {
	var lastChurnHeight int64
	found := false
	for _, vault := range vaults {
		if vault.Status != thorchain.ActiveVault.String() || vault.StatusSince == nil {
			continue
		}
		if !found || *vault.StatusSince > lastChurnHeight {
			lastChurnHeight = *vault.StatusSince
			found = true
		}
	}
	return lastChurnHeight, found
}

func vaultHasAddress(vault openapi.Vault, chain common.Chain, address common.Address) bool {
	for _, vaultAddress := range vault.Addresses {
		if vaultAddress.Chain == chain.String() && common.Address(vaultAddress.Address).Equals(address) {
			return true
		}
	}
	return false
}

func vaultHasPositiveCoin(vault openapi.Vault, asset common.Asset) bool {
	for _, coin := range vault.Coins {
		if coin.Asset != asset.String() {
			continue
		}
		amount, ok := new(big.Int).SetString(coin.Amount, 10)
		return ok && amount.Sign() > 0
	}
	return false
}

func (a *XMRPostChurnActor) broadcastMimir(node *User, accAddr cosmos.AccAddress, key string, value int64) (common.TxID, error) {
	mimir := thorchain.NewMsgMimir(key, value, accAddr)
	txid, err := node.Thorchain.Broadcast(mimir)
	if err != nil {
		return common.TxID(""), fmt.Errorf("broadcast %s=%d: %w", key, value, err)
	}
	return txid, nil
}
