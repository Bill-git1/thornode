package core

import (
	"fmt"
	"strconv"
	"time"

	sdkmath "cosmossdk.io/math"
	"github.com/rs/zerolog/log"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/constants"
	openapi "gitlab.com/thorchain/thornode/v3/openapi/gen"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/thornode"
	. "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/types"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// SwapMemolessActor
////////////////////////////////////////////////////////////////////////////////////////

//go:generate stringer -type=MemolessType
type MemolessType int

const (
	MemolessTypeRef    MemolessType = iota // "r:<ref>" memo
	MemolessTypeAmount                     // reference encoded in amount
)

type SwapMemolessActor struct {
	SwapActor

	memolessType     MemolessType
	registeredMemo   string
	registrationTxID common.TxID
	memolessRef      uint64
	swapMemo         string
	signedSwap       []byte
}

func NewSwapMemolessActor(from, to common.Asset, memolessType MemolessType) *Actor {
	a := &SwapMemolessActor{
		SwapActor: SwapActor{
			Actor: *NewActor(fmt.Sprintf("SwapMemoless %s => %s", from, to)),
			from:  from,
			to:    to,
		},
		memolessType: memolessType,
	}

	a.Ops = append(a.Ops, a.updateLogContext)

	// lock an account with from balance
	a.Ops = append(a.Ops, a.acquireUser)

	// generate swap quote
	a.Ops = append(a.Ops, a.getQuote)

	// TODO: support tokens
	if !from.IsGasAsset() {
		log.Fatal().Msg("simulation only implemented for gas assets")
	}

	// TODO: support assets with less than 1e8 precision
	if from.Chain.GetGasAssetDecimal() != 8 {
		log.Fatal().Msg("simulation only implemented for assets with at least 1e8 precision")
	}

	switch memolessType {
	case MemolessTypeRef:
		a.Ops = append(a.Ops, a.registerSwap)
		a.Ops = append(a.Ops, a.getMemolessRef)
		a.Ops = append(a.Ops, a.prepareSwapMemolessByRef)
		a.Ops = append(a.Ops, a.signSwap)
		a.Ops = append(a.Ops, a.broadcastSwap)
	case MemolessTypeAmount:
		a.Ops = append(a.Ops, a.registerSwap)
		a.Ops = append(a.Ops, a.getMemolessRef)
		a.Ops = append(a.Ops, a.prepareSwapMemolessByAmount)
		a.Ops = append(a.Ops, a.signSwap)
		a.Ops = append(a.Ops, a.broadcastSwap)
	}

	// verify the swap within expected range
	a.Ops = append(a.Ops, a.verifyOutbound)

	return &a.Actor
}

////////////////////////////////////////////////////////////////////////////////////////
// Ops
////////////////////////////////////////////////////////////////////////////////////////

func (a *SwapMemolessActor) updateLogContext(config *OpConfig) OpResult {
	a.SetLogger(log.With().Stringer("type", a.memolessType).Logger())
	return OpResult{
		Continue: true,
	}
}

func (a *SwapMemolessActor) registerSwap(config *OpConfig) OpResult {
	// build the swap
	coin := common.NewCoin(common.RuneNative, sdkmath.ZeroUint())
	a.registeredMemo = fmt.Sprintf("=:%s:%s", a.to, a.toAddress)
	registerMemo := fmt.Sprintf("REFERENCE:%s:%s", a.from, a.registeredMemo)

	a.Log().Info().
		Str("registerMemo", registerMemo).
		Msg("registering memoless swap")

	thorAddress, err := a.user.PubKey(common.THORChain).GetAddress(common.THORChain)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get thor address")
		return OpResult{
			Continue: false,
			Error:    err,
		}
	}
	thorAccAddr, err := thorAddress.AccAddress()
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get thor acc address")
		return OpResult{
			Continue: false,
			Error:    err,
		}
	}
	deposit := types.NewMsgDeposit(common.NewCoins(coin), registerMemo, thorAccAddr)
	a.Log().Info().Interface("deposit", deposit).Msg("registering memoless swap")

	// broadcast the registration with blocking to wait for block commitment
	a.registrationTxID, err = a.user.Thorchain.BroadcastWithBlocking(deposit)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to broadcast tx")
		return OpResult{
			Continue: false,
		}
	}

	a.Log().Info().
		Stringer("txid", a.registrationTxID).
		Str("memo", registerMemo).
		Msg("broadcasted register tx")
	return OpResult{
		Continue: true,
	}
}

func (a *SwapMemolessActor) getMemolessRef(config *OpConfig) OpResult {
	// Retry logic to handle race condition between tx broadcast and state query
	// The transaction may take 1-2 blocks to be fully committed to state
	maxAttempts := 60 // 60 attempts * 500ms = 30 seconds timeout
	var referenceMemo openapi.ReferenceMemoResponse
	var err error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		referenceMemo, err = thornode.GetMemoHash(a.registrationTxID.String())
		if err != nil {
			a.Log().Debug().Err(err).Int("attempt", attempt+1).Msg("failed to get reference memo, retrying")
			time.Sleep(500 * time.Millisecond)
			continue
		}

		if referenceMemo.Memo == "" {
			a.Log().Debug().Int("attempt", attempt+1).Msg("reference memo not found yet, retrying")
			time.Sleep(500 * time.Millisecond)
			continue
		}

		// Found the memo, break out of retry loop
		break
	}

	// Final check after all retries
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get reference memo after retries")
		return OpResult{
			Continue: false,
			Error:    err,
		}
	}

	if referenceMemo.Memo == "" {
		a.Log().Error().Stringer("registrationTxID", a.registrationTxID).Msg("reference memo not found after retries")
		return OpResult{
			Continue: false,
			Error:    fmt.Errorf("reference memo not found after retries"),
		}
	}

	if referenceMemo.Memo != a.registeredMemo {
		a.Log().Error().
			Str("expected", a.registeredMemo).
			Str("actual", referenceMemo.Memo).
			Msg("reference memo does not match")
		return OpResult{
			Continue: false,
			Error:    fmt.Errorf("reference memo does not match"),
		}
	}

	a.memolessRef, err = strconv.ParseUint(referenceMemo.Reference, 10, 64)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to parse memoless ref")
		return OpResult{
			Continue: false,
			Error:    err,
		}
	}

	a.Log().Info().Uint64("ref", a.memolessRef).Msg("got memoless ref")
	return OpResult{
		Continue: true,
	}
}

func (a *SwapMemolessActor) prepareSwapMemolessByRef(config *OpConfig) OpResult {
	// encode reference memo with leading zeros
	modulusStr := fmt.Sprintf("%d", a.amountModulus())
	memolessRefFormatString := fmt.Sprintf("r:%%0%dd", len(modulusStr)-1)
	a.swapMemo = fmt.Sprintf(memolessRefFormatString, a.memolessRef)
	a.Log().Info().Str("memo", a.swapMemo).Msg("prepared memoless swap memo")
	return OpResult{
		Continue: true,
	}
}

func (a *SwapMemolessActor) prepareSwapMemolessByAmount(config *OpConfig) OpResult {
	// encode the memoless ref in the amount
	modulus := a.amountModulus()
	a.Log().Info().Uint64("modulus", modulus).Msg("preparing memoless by amount")
	before := a.swapAmount
	a.swapAmount = a.swapAmount.Sub(a.swapAmount.Mod(sdkmath.NewUint(modulus))) // clear last digits
	a.swapAmount = a.swapAmount.Add(sdkmath.NewUint(a.memolessRef))
	a.Log().Info().
		Stringer("before", before).
		Stringer("after", a.swapAmount).
		Msg("encoded memoless ref in amount")

	return OpResult{
		Continue: true,
	}
}

func (a *SwapMemolessActor) signSwap(config *OpConfig) OpResult {
	// get inbound address
	inboundAddr, _, err := thornode.GetInboundAddress(a.from.Chain)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get inbound address")
		return OpResult{
			Continue: false,
		}
	}

	// create tx
	tx := SimTx{
		Chain:     a.from.Chain,
		ToAddress: inboundAddr,
		Coin:      common.NewCoin(a.from, a.swapAmount),
		Memo:      a.swapMemo,
	}

	// sign transaction
	client := a.user.ChainClients[a.from.Chain]
	a.signedSwap, err = client.SignTx(tx)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to sign tx")
		return OpResult{
			Continue: false,
		}
	}

	return OpResult{
		Continue: true,
	}
}

func (a *SwapMemolessActor) broadcastSwap(config *OpConfig) OpResult {
	// broadcast transaction
	client := a.user.ChainClients[a.from.Chain]
	var err error
	a.swapTxID, err = client.BroadcastTx(a.signedSwap)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to broadcast tx")
		return OpResult{
			Continue: false,
		}
	}

	a.Log().Info().
		Str("txid", a.swapTxID).
		Stringer("amount", a.swapAmount).
		Str("memo", a.swapMemo).
		Msg("broadcasted swap tx")
	return OpResult{
		Continue: true,
	}
}

////////////////////////////////////////////////////////////////////////////////////////
// Helpers
////////////////////////////////////////////////////////////////////////////////////////

func (a *SwapMemolessActor) amountModulus() uint64 {
	memolessRefCount := constants.NewConstantValue().GetInt64Value(constants.MemolessTxnRefCount)
	txnRefLength := len(fmt.Sprintf("%d", memolessRefCount))
	var modulus uint64 = 1
	for i := 0; i < txnRefLength; i++ {
		modulus *= 10
	}
	return modulus
}
