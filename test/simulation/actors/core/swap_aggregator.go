package core

import (
	"fmt"
	"math/big"
	rand "math/rand/v2"
	"strings"
	"time"

	ecommon "github.com/ethereum/go-ethereum/common"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/evm"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/thornode"
	. "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// AggregatorSwapActor
////////////////////////////////////////////////////////////////////////////////////////

// AggregatorSwapActor tests swap-out through a DEX aggregator contract. The inbound
// swap targets an EVM gas asset, and the memo includes aggregator fields so THORChain
// queues the outbound with aggregator metadata (contract address, target asset, limit).
type AggregatorSwapActor struct {
	SwapActor

	// aggregator fields included in the memo
	aggregatorSuffix      string
	aggregatorTargetAsset string
	aggregatorTargetLimit string
}

// NewAggregatorSwapActor creates a swap actor that exercises the swap-out aggregator
// path. The destination asset must be an EVM gas asset so the aggregator contract can
// perform the final DEX leg.
func NewAggregatorSwapActor(from, to common.Asset, aggregatorSuffix, aggregatorTargetAsset, aggregatorTargetLimit string, rng *rand.Rand) *Actor {
	a := &AggregatorSwapActor{
		SwapActor: SwapActor{
			Actor: *NewActor(fmt.Sprintf("AggSwap %s => %s", from, to), rng),
			from:  from,
			to:    to,
		},
		aggregatorSuffix:      aggregatorSuffix,
		aggregatorTargetAsset: aggregatorTargetAsset,
		aggregatorTargetLimit: aggregatorTargetLimit,
	}

	a.Ops = append(a.Ops, a.acquireUser)
	a.Ops = append(a.Ops, a.getQuote)

	if from.Chain.IsEVM() && !from.IsGasAsset() {
		a.Ops = append(a.Ops, a.sendAggregatorTokenSwap)
	} else {
		a.Ops = append(a.Ops, a.sendAggregatorSwap)
	}

	a.Ops = append(a.Ops, a.verifyAggregatorOutbound)

	return &a.Actor
}

////////////////////////////////////////////////////////////////////////////////////////
// Ops
////////////////////////////////////////////////////////////////////////////////////////

// sendAggregatorSwap sends a swap with aggregator fields in the memo for native/gas
// asset sources (non-EVM-token).
func (a *AggregatorSwapActor) sendAggregatorSwap(config *OpConfig) OpResult {
	inboundAddr, _, err := thornode.GetInboundAddress(a.from.Chain)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get inbound address")
		return OpResult{Continue: false}
	}

	// shorten the to asset on UTXO chains
	to := a.to.String()
	if a.from.Chain.IsUTXO() && !a.to.IsGasAsset() {
		to = strings.Split(to, "-")[0]
	}

	memo := fmt.Sprintf("=:%s:%s::::%s:%s:%s",
		to, a.toAddress, a.aggregatorSuffix, a.aggregatorTargetAsset, a.aggregatorTargetLimit)

	tx := SimTx{
		Chain:     a.from.Chain,
		ToAddress: inboundAddr,
		Coin:      common.NewCoin(a.from, a.swapAmount),
		Memo:      memo,
	}

	client := a.user.ChainClients[a.from.Chain]

	signed, err := client.SignTx(tx)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to sign tx")
		return OpResult{Continue: false}
	}

	txid, err := client.BroadcastTx(signed)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to broadcast tx")
		return OpResult{Continue: false}
	}
	a.swapTxID = txid

	a.Log().Info().
		Str("txid", txid).
		Str("aggregator", a.aggregatorSuffix).
		Str("target_asset", a.aggregatorTargetAsset).
		Msg("broadcasted aggregator swap tx")
	return OpResult{Continue: true}
}

// sendAggregatorTokenSwap sends an EVM token swap with aggregator fields in the memo
// via the router's depositWithExpiry.
func (a *AggregatorSwapActor) sendAggregatorTokenSwap(config *OpConfig) OpResult {
	inboundAddr, routerAddr, err := thornode.GetInboundAddress(a.from.Chain)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get inbound address")
		return OpResult{Continue: false}
	}
	if routerAddr == nil {
		a.Log().Error().Msg("failed to get router address")
		return OpResult{Continue: false}
	}

	token := evm.Tokens(a.from.Chain)[a.from]

	// convert amount to token decimals
	factor := big.NewInt(1).Exp(big.NewInt(10), big.NewInt(int64(token.Decimals)), nil)
	tokenAmount := a.swapAmount.Mul(cosmos.NewUintFromBigInt(factor))
	tokenAmount = tokenAmount.QuoUint64(common.One)

	iClient := a.user.ChainClients[a.from.Chain]
	client, ok := iClient.(*evm.Client)
	if !ok {
		a.Log().Fatal().Msg("failed to get evm client")
	}

	// approve the router
	eRouterAddr := ecommon.HexToAddress(routerAddr.String())
	approveTx := SimContractTx{
		Chain:    a.from.Chain,
		Contract: common.Address(token.Address),
		ABI:      evm.ERC20ABI(),
		Method:   "approve",
		Args:     []interface{}{eRouterAddr, tokenAmount.BigInt()},
	}
	signed, err := client.SignContractTx(approveTx)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to sign approve tx")
		return OpResult{Continue: false}
	}
	txid, err := client.BroadcastTx(signed)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to broadcast approve tx")
		return OpResult{Continue: false}
	}
	a.Log().Info().Str("txid", txid).Msg("broadcasted router approve tx")

	// build aggregator memo and call depositWithExpiry
	memo := fmt.Sprintf("=:%s:%s::::%s:%s:%s",
		a.to, a.toAddress, a.aggregatorSuffix, a.aggregatorTargetAsset, a.aggregatorTargetLimit)
	expiry := time.Now().Add(time.Hour).Unix()
	eInboundAddr := ecommon.HexToAddress(inboundAddr.String())
	eTokenAddr := ecommon.HexToAddress(token.Address)
	depositTx := SimContractTx{
		Chain:    a.from.Chain,
		Contract: *routerAddr,
		ABI:      evm.RouterABI(),
		Method:   "depositWithExpiry",
		Args: []interface{}{
			eInboundAddr,
			eTokenAddr,
			tokenAmount.BigInt(),
			memo,
			big.NewInt(expiry),
		},
	}

	signed, err = client.SignContractTx(depositTx)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to sign deposit tx")
		return OpResult{Continue: false}
	}
	txid, err = client.BroadcastTx(signed)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to broadcast deposit tx")
		return OpResult{Continue: false}
	}
	a.swapTxID = txid

	a.Log().Info().
		Str("txid", txid).
		Str("aggregator", a.aggregatorSuffix).
		Str("target_asset", a.aggregatorTargetAsset).
		Msg("broadcasted aggregator token swap tx")
	return OpResult{Continue: true}
}

// verifyAggregatorOutbound waits for the outbound to complete and verifies aggregator
// metadata is present in the action.
func (a *AggregatorSwapActor) verifyAggregatorOutbound(config *OpConfig) OpResult {
	stages, err := thornode.GetTxStages(a.swapTxID)
	if err != nil {
		a.Log().Warn().Err(err).Msg("failed to get tx stages")
		return OpResult{Continue: false}
	}

	if stages.OutboundSigned == nil || !stages.OutboundSigned.Completed {
		return OpResult{Continue: false}
	}

	details, err := thornode.GetTxDetails(a.swapTxID)
	if err != nil {
		a.Log().Warn().Err(err).Msg("failed to get tx details")
		return OpResult{Continue: false}
	}

	if len(details.OutTxs) != 1 {
		a.user.Release()
		return OpResult{
			Error:  fmt.Errorf("expected exactly one out transaction, got %d", len(details.OutTxs)),
			Finish: true,
		}
	}

	if len(details.Actions) != 1 {
		a.user.Release()
		return OpResult{
			Error:  fmt.Errorf("expected exactly one action, got %d", len(details.Actions)),
			Finish: true,
		}
	}

	// verify action is not refund
	action := details.Actions[0]
	actionMemo := ""
	if action.Memo != nil {
		actionMemo = *action.Memo
	} else if action.OriginalMemo != nil {
		actionMemo = *action.OriginalMemo
	}
	if strings.HasPrefix(actionMemo, "REFUND:") {
		a.user.Release()
		return OpResult{
			Error:  fmt.Errorf("swap was refunded"),
			Finish: true,
		}
	}

	// verify aggregator metadata is present
	if !action.HasAggregator() || action.GetAggregator() == "" {
		a.user.Release()
		return OpResult{
			Error:  fmt.Errorf("action missing aggregator field"),
			Finish: true,
		}
	}

	// verify aggregator address ends with the suffix we sent
	if !strings.HasSuffix(action.GetAggregator(), a.aggregatorSuffix) {
		a.user.Release()
		return OpResult{
			Error: fmt.Errorf("aggregator address %q does not match suffix %q",
				action.GetAggregator(), a.aggregatorSuffix),
			Finish: true,
		}
	}

	// verify aggregator target asset matches what we sent
	if !action.HasAggregatorTargetAsset() || action.GetAggregatorTargetAsset() == "" {
		a.user.Release()
		return OpResult{
			Error:  fmt.Errorf("action missing aggregator_target_asset field"),
			Finish: true,
		}
	}
	if action.GetAggregatorTargetAsset() != a.aggregatorTargetAsset {
		a.user.Release()
		return OpResult{
			Error: fmt.Errorf("aggregator_target_asset %q does not match expected %q",
				action.GetAggregatorTargetAsset(), a.aggregatorTargetAsset),
			Finish: true,
		}
	}

	// verify aggregator target limit matches what we sent
	if a.aggregatorTargetLimit != "" {
		if !action.HasAggregatorTargetLimit() || action.GetAggregatorTargetLimit() == "" {
			a.user.Release()
			return OpResult{
				Error:  fmt.Errorf("action missing aggregator_target_limit field"),
				Finish: true,
			}
		}
		if action.GetAggregatorTargetLimit() != a.aggregatorTargetLimit {
			a.user.Release()
			return OpResult{
				Error: fmt.Errorf("aggregator_target_limit %q does not match expected %q",
					action.GetAggregatorTargetLimit(), a.aggregatorTargetLimit),
				Finish: true,
			}
		}
	}

	a.Log().Info().
		Str("aggregator", action.GetAggregator()).
		Str("aggregator_target_asset", action.GetAggregatorTargetAsset()).
		Str("aggregator_target_limit", action.GetAggregatorTargetLimit()).
		Msg("aggregator swap verified")

	a.user.Release()
	return OpResult{Finish: true}
}
