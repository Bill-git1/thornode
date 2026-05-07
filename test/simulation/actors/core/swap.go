package core

import (
	"fmt"
	"math/big"
	rand "math/rand/v2"
	"regexp"
	"strings"
	"time"

	sdkmath "cosmossdk.io/math"

	ecommon "github.com/ethereum/go-ethereum/common"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	openapi "gitlab.com/thorchain/thornode/v3/openapi/gen"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/evm"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/thornode"
	. "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// SwapActor
////////////////////////////////////////////////////////////////////////////////////////

type SwapActor struct {
	Actor

	user *User

	// starting balances
	from        common.Asset
	fromAddress common.Address
	fromBalance sdkmath.Uint
	to          common.Asset
	toAddress   common.Address
	toBalance   sdkmath.Uint

	swapAmount sdkmath.Uint
	swapTxID   string

	// expected range for received amount, including outbound fee
	requiredOutboundSpend sdkmath.Uint
	minExpected           sdkmath.Uint
	maxExpected           sdkmath.Uint

	targetXMRSwapSlotAcquired bool
}

var (
	recommendedMinAmountInPattern = regexp.MustCompile(`recommended_min_amount_in:\s*(\d+)`)
	targetXMRSwapSlot             = make(chan struct{}, 1)
)

func NewSwapActor(from, to common.Asset, rng *rand.Rand) *Actor {
	a := &SwapActor{
		Actor: *NewActor(fmt.Sprintf("Swap %s => %s", from, to), rng),
		from:  from,
		to:    to,
	}
	a.Timeout = swapActorTimeout(from, to)

	// lock an account with from balance
	a.Ops = append(a.Ops, a.acquireUser)

	// generate swap quote
	a.Ops = append(a.Ops, a.getQuote)

	// send swap inbound
	if from.Chain.IsEVM() && !from.IsGasAsset() {
		a.Ops = append(a.Ops, a.sendTokenSwap)
	} else {
		a.Ops = append(a.Ops, a.sendSwap)
	}

	// verify the swap within expected range
	a.Ops = append(a.Ops, a.verifyOutbound)

	return &a.Actor
}

func swapActorTimeout(from, to common.Asset) time.Duration {
	if from.Chain.Equals(common.XMRChain) || to.Chain.Equals(common.XMRChain) {
		return 8 * time.Minute
	}
	if from.Chain.Equals(common.SOLChain) || to.Chain.Equals(common.SOLChain) {
		return 4 * time.Minute
	}
	if to.Chain.Equals(common.TRONChain) {
		return 6 * time.Minute
	}
	return 2 * time.Minute
}

func getRecommendedMinAmountIn(quote openapi.QuoteSwapResponse) (sdkmath.Uint, bool) {
	if quote.RecommendedMinAmountIn == nil || *quote.RecommendedMinAmountIn == "" {
		return sdkmath.ZeroUint(), false
	}
	return sdkmath.NewUintFromString(*quote.RecommendedMinAmountIn), true
}

func getRecommendedMinAmountInFromError(err error) (sdkmath.Uint, bool) {
	matches := recommendedMinAmountInPattern.FindStringSubmatch(err.Error())
	if len(matches) != 2 {
		return sdkmath.ZeroUint(), false
	}
	return sdkmath.NewUintFromString(matches[1]), true
}

func retryAmountAtRecommendedMin(currentAmount, minAmount sdkmath.Uint) sdkmath.Uint {
	base := minAmount
	if currentAmount.GT(minAmount) {
		base = currentAmount
	}

	buffer := base.QuoUint64(100)
	if buffer.IsZero() {
		buffer = sdkmath.OneUint()
	}
	return base.Add(buffer)
}

func getSwapQuoteWithRecommendedMin(from, to common.Asset, amount sdkmath.Uint, destination common.Address, extended bool) (openapi.QuoteSwapResponse, sdkmath.Uint, error) {
	currentAmount := amount
	for attempt := 0; attempt < 4; attempt++ {
		quote, err := thornode.GetSwapQuote(from, to, currentAmount, destination, extended)
		if err != nil {
			minAmount, ok := getRecommendedMinAmountInFromError(err)
			if ok {
				currentAmount = retryAmountAtRecommendedMin(currentAmount, minAmount)
				continue
			}
			return openapi.QuoteSwapResponse{}, currentAmount, err
		}

		minAmount, ok := getRecommendedMinAmountIn(quote)
		if ok && currentAmount.LT(minAmount) {
			currentAmount = minAmount
			continue
		}

		return quote, currentAmount, nil
	}

	return openapi.QuoteSwapResponse{}, currentAmount, fmt.Errorf("failed to settle swap quote amount within recommended minimum")
}

func swapSpendableBalance(asset common.Asset, balance sdkmath.Uint) sdkmath.Uint {
	if balance.IsZero() {
		return sdkmath.ZeroUint()
	}

	if !asset.IsGasAsset() {
		return balance
	}

	// Preserve enough of the gas asset balance to avoid draining the account below
	// chain dust/reserve requirements while still allowing late-stage swaps to use
	// whatever balance remains.
	reserve := asset.Chain.DustThreshold()
	fivePercent := balance.QuoUint64(20)
	if fivePercent.GT(reserve) {
		reserve = fivePercent
	}
	if !balance.GT(reserve) {
		return sdkmath.ZeroUint()
	}

	return balance.Sub(reserve)
}

func swapAmountForBalance(asset common.Asset, target, balance sdkmath.Uint) sdkmath.Uint {
	spendable := swapSpendableBalance(asset, balance)
	if spendable.IsZero() {
		return sdkmath.ZeroUint()
	}
	if spendable.LT(target) {
		return spendable
	}
	return target
}

func accountAssetAmount(account *common.Account, asset common.Asset) sdkmath.Uint {
	if account == nil {
		return sdkmath.ZeroUint()
	}

	coin := account.Coins.GetCoin(asset)
	if coin.Amount.IsNil() {
		return sdkmath.ZeroUint()
	}

	return coin.Amount
}

func swapQuoteExpectedRange(quote openapi.QuoteSwapResponse) (sdkmath.Uint, sdkmath.Uint, sdkmath.Uint) {
	// store expected range to fail if received amount is outside 10% tolerance
	quoteOut := sdkmath.NewUintFromString(quote.ExpectedAmountOut)
	tolerance := quoteOut.QuoUint64(10)
	if quote.Fees.Outbound != nil {
		outboundFee := sdkmath.NewUintFromString(*quote.Fees.Outbound)
		quoteOut = quoteOut.Add(outboundFee)

		// handle 3x gas rate fluctuation (add 2x outbound fee to tolerance)
		tolerance = tolerance.Add(outboundFee.MulUint64(2))
	}
	return quoteOut, common.SafeSub(quoteOut, tolerance), quoteOut.Add(tolerance)
}

func setQuoteExpectations(a *SwapActor, quote openapi.QuoteSwapResponse) {
	a.requiredOutboundSpend, a.minExpected, a.maxExpected = swapQuoteExpectedRange(quote)
}

func outboundAmountPlusGas(out openapi.Tx, action openapi.TxOutItem, to common.Asset) (sdkmath.Uint, error) {
	outAmountPlusGas := cosmos.NewUintFromString(out.Coins[0].Amount)

	gasCoins := out.Gas
	if len(gasCoins) == 0 {
		gasCoins = action.MaxGas
	}

	for _, gasCoin := range gasCoins {
		if gasCoin.Asset == to.String() {
			outAmountPlusGas = outAmountPlusGas.Add(cosmos.NewUintFromString(gasCoin.Amount))
			continue
		}

		gasAssetValue, err := thornode.ConvertAssetAmount(gasCoin, to.String())
		if err != nil {
			return sdkmath.ZeroUint(), err
		}
		outAmountPlusGas = outAmountPlusGas.Add(gasAssetValue)
	}

	return outAmountPlusGas, nil
}

func (a *SwapActor) updateSwapAmount(next sdkmath.Uint, reason string) error {
	if next.GT(a.fromBalance) {
		return fmt.Errorf("%s %s exceeds user balance %s", reason, next, a.fromBalance)
	}

	if !next.Equal(a.swapAmount) {
		a.Log().Info().
			Stringer("before", a.swapAmount).
			Stringer("after", next).
			Str("reason", reason).
			Msg("adjusted swap amount")
	}

	a.swapAmount = next
	return nil
}

func (a *SwapActor) releaseUserSelection() {
	if a.user != nil {
		a.user.Release()
	}
	a.user = nil
	a.fromAddress = common.NoAddress
	a.fromBalance = sdkmath.ZeroUint()
	a.toAddress = common.NoAddress
	a.toBalance = sdkmath.ZeroUint()
	a.swapTxID = ""
	a.requiredOutboundSpend = sdkmath.ZeroUint()
	a.minExpected = sdkmath.ZeroUint()
	a.maxExpected = sdkmath.ZeroUint()
}

func (a *SwapActor) acquireTargetXMRSwapSlot(config *OpConfig) OpResult {
	if !a.to.Chain.Equals(common.XMRChain) || a.targetXMRSwapSlotAcquired {
		return OpResult{Continue: true}
	}

	select {
	case targetXMRSwapSlot <- struct{}{}:
		a.targetXMRSwapSlotAcquired = true
		a.Log().Info().Msg("acquired target XMR swap slot")
		return OpResult{Continue: true}
	default:
		a.Log().Info().Msg("waiting for target XMR swap slot")
		return OpResult{Continue: false}
	}
}

func (a *SwapActor) releaseTargetXMRSwapSlot() {
	if !a.targetXMRSwapSlotAcquired {
		return
	}

	<-targetXMRSwapSlot
	a.targetXMRSwapSlotAcquired = false
	a.Log().Info().Msg("released target XMR swap slot")
}

func (a *SwapActor) acquireUserForAmount(config *OpConfig, targetAmount sdkmath.Uint, allowPartial bool) OpResult {
	a.swapAmount = targetAmount
	a.fromBalance = sdkmath.ZeroUint()
	a.toBalance = sdkmath.ZeroUint()

	var selectedUser *User
	selectedAvailableBalance := sdkmath.ZeroUint()
	selectedSpendableBalance := sdkmath.ZeroUint()
	selectedSwapAmount := sdkmath.ZeroUint()
	lockedUsers := 0
	availableUsers := 0
	insufficientBalanceUsers := 0
	accountErrorUsers := 0

	for _, user := range config.Users {
		// skip users already being used
		if !user.Acquire() {
			lockedUsers++
			continue
		}
		availableUsers++

		cl := a.Log().With().
			Str("user", user.Name()).
			Stringer("from", a.from).
			Stringer("to", a.to).
			Logger()
		a.SetLogger(cl)

		// skip users that don't have from asset balance
		fromAcct, err := user.ChainClients[a.from.Chain].GetAccount(nil)
		if err != nil {
			a.Log().Error().Err(err).Msg("failed to get from account")
			accountErrorUsers++
			user.Release()
			continue
		}
		fromBalance := accountAssetAmount(fromAcct, a.from)
		spendableBalance := swapSpendableBalance(a.from, fromBalance)
		candidateAmount := swapAmountForBalance(a.from, targetAmount, fromBalance)
		if candidateAmount.IsZero() {
			a.Log().Error().Msg("user has insufficient from balance")
			insufficientBalanceUsers++
			user.Release()
			continue
		}
		if !allowPartial && spendableBalance.LT(targetAmount) {
			a.Log().Info().
				Stringer("required_amount", targetAmount).
				Stringer("available_balance", spendableBalance).
				Msg("user balance below required swap amount")
			insufficientBalanceUsers++
			user.Release()
			continue
		}

		// Prefer the user with the most spendable balance so late-stage swaps can
		// keep progressing even after earlier actors have partially drained assets.
		if selectedUser != nil && !spendableBalance.GT(selectedSpendableBalance) {
			user.Release()
			continue
		}

		if selectedUser != nil {
			selectedUser.Release()
		}
		selectedUser = user
		selectedAvailableBalance = spendableBalance
		selectedSpendableBalance = spendableBalance
		selectedSwapAmount = candidateAmount
	}

	if selectedUser != nil {
		cl := a.Log().With().
			Str("user", selectedUser.Name()).
			Stringer("from", a.from).
			Stringer("to", a.to).
			Logger()
		a.SetLogger(cl)
		a.toBalance = sdkmath.ZeroUint()

		toAcct, err := selectedUser.ChainClients[a.to.Chain].GetAccount(nil)
		if err != nil {
			a.Log().Warn().Err(err).Msg("failed to get to account")
			selectedUser.Release()
			return OpResult{
				Continue: false,
			}
		}

		a.user = selectedUser
		a.fromBalance = selectedAvailableBalance

		// get l1 address to store in state context
		fromAddress, err := selectedUser.PubKey(a.from.Chain).GetAddress(a.from.Chain)
		if err != nil {
			a.Log().Fatal().Err(err).Msg("failed to get L1 address")
		}
		a.fromAddress = fromAddress
		toAddress, err := selectedUser.PubKey(a.to.Chain).GetAddress(a.to.Chain)
		if err != nil {
			a.Log().Fatal().Err(err).Msg("failed to get L1 address")
		}
		a.toAddress = toAddress

		// get to asset balance for tracking
		a.toBalance = accountAssetAmount(toAcct, a.to)

		if err := a.updateSwapAmount(selectedSwapAmount, "available user balance"); err != nil {
			a.releaseUserSelection()
			return OpResult{
				Error:  err,
				Finish: true,
			}
		}

		a.Log().Info().Msg("acquired user")
		return OpResult{
			Continue: true,
		}
	}

	if !allowPartial && availableUsers > 0 && lockedUsers == 0 && accountErrorUsers == 0 && insufficientBalanceUsers == availableUsers {
		a.Log().Info().
			Int("available_users", availableUsers).
			Stringer("target_amount", targetAmount).
			Msg("skipping swap because required amount exceeds every available user balance")
		return OpResult{
			Finish: true,
		}
	}

	// Remain pending if selection may become possible after another actor releases
	// a user or a transient account read recovers.
	a.Log().Info().
		Int("available_users", availableUsers).
		Int("locked_users", lockedUsers).
		Int("account_error_users", accountErrorUsers).
		Stringer("target_amount", targetAmount).
		Bool("allow_partial", allowPartial).
		Msg("waiting for user with sufficient balance")
	return OpResult{
		Continue: false,
	}
}

func (a *SwapActor) getQuoteWithUserRetry(config *OpConfig) (openapi.QuoteSwapResponse, OpResult) {
	for attempt := 0; attempt < 4; attempt++ {
		if a.user == nil {
			result := a.acquireUserForAmount(config, a.swapAmount, false)
			if !result.Continue || result.Finish {
				a.releaseTargetXMRSwapSlot()
				return openapi.QuoteSwapResponse{}, result
			}
		}

		// XMR outbounds carry high fees relative to the small mocknet pools.
		// Hold the slot from quote through finalisation, but not while selecting
		// a source user, so an unsatisfiable quote cannot block all XMR targets.
		if result := a.acquireTargetXMRSwapSlot(config); !result.Continue || result.Finish {
			a.releaseUserSelection()
			return openapi.QuoteSwapResponse{}, result
		}

		quote, quoteAmount, err := getSwapQuoteWithRecommendedMin(a.from, a.to, a.swapAmount, a.toAddress, a.from.Chain.IsUTXO())
		if err != nil {
			if quoteAmount.GT(a.fromBalance) {
				a.Log().Info().
					Stringer("required_amount", quoteAmount).
					Stringer("user_balance", a.fromBalance).
					Msg("quote minimum exceeds selected user balance, retrying user selection")
				a.releaseUserSelection()
				a.releaseTargetXMRSwapSlot()
				a.swapAmount = quoteAmount
				continue
			}

			if !quoteAmount.Equal(a.swapAmount) {
				if updateErr := a.updateSwapAmount(quoteAmount, "recommended minimum inbound amount"); updateErr != nil {
					a.releaseUserSelection()
					a.releaseTargetXMRSwapSlot()
					return openapi.QuoteSwapResponse{}, OpResult{
						Error:  updateErr,
						Finish: true,
					}
				}
				continue
			}

			a.Log().Error().Err(err).Str("amount", a.swapAmount.String()).Msg("failed to get swap quote")
			a.releaseUserSelection()
			a.releaseTargetXMRSwapSlot()
			return openapi.QuoteSwapResponse{}, OpResult{
				Continue: false,
			}
		}

		if quoteAmount.GT(a.fromBalance) {
			a.Log().Info().
				Stringer("required_amount", quoteAmount).
				Stringer("user_balance", a.fromBalance).
				Msg("quote minimum exceeds selected user balance, retrying user selection")
			a.releaseUserSelection()
			a.releaseTargetXMRSwapSlot()
			a.swapAmount = quoteAmount
			continue
		}

		if err := a.updateSwapAmount(quoteAmount, "recommended minimum inbound amount"); err != nil {
			a.releaseUserSelection()
			a.releaseTargetXMRSwapSlot()
			return openapi.QuoteSwapResponse{}, OpResult{
				Error:  err,
				Finish: true,
			}
		}

		return quote, OpResult{
			Continue: true,
		}
	}

	a.Log().Info().
		Stringer("amount", a.swapAmount).
		Msg("waiting for user that can satisfy the quoted minimum amount")
	a.releaseUserSelection()
	a.releaseTargetXMRSwapSlot()
	return openapi.QuoteSwapResponse{}, OpResult{
		Continue: false,
	}
}

////////////////////////////////////////////////////////////////////////////////////////
// Ops
////////////////////////////////////////////////////////////////////////////////////////

func (a *SwapActor) acquireUser(config *OpConfig) OpResult {
	// swap 0.25% of from pool depth
	pool, err := thornode.GetPool(a.from)
	if err != nil {
		return OpResult{
			Continue: false,
		}
	}
	targetAmount := sdkmath.NewUintFromString(pool.BalanceAsset).QuoUint64(400)
	return a.acquireUserForAmount(config, targetAmount, true)
}

func (a *SwapActor) getQuote(config *OpConfig) OpResult {
	quote, result := a.getQuoteWithUserRetry(config)
	if !result.Continue || result.Finish {
		if result.Finish {
			a.releaseTargetXMRSwapSlot()
		}
		return result
	}
	setQuoteExpectations(a, quote)
	return OpResult{
		Continue: true,
	}
}

func (a *SwapActor) sendSwap(config *OpConfig) OpResult {
	if result := a.ensureXMRVaultSpendable(config, a.requiredOutboundSpend); !result.Continue || result.Finish {
		return result
	}

	// get inbound address
	inboundAddr, _, err := thornode.GetInboundAddress(a.from.Chain)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get inbound address")
		return OpResult{
			Continue: false,
		}
	}

	// if on a utxo chain, shorten the to asset to fuzzy match
	to := a.to.String()
	if a.from.Chain.IsUTXO() && !a.to.IsGasAsset() {
		to = strings.Split(to, "-")[0]
	}

	// create tx out
	memo := fmt.Sprintf("=:%s:%s", to, a.toAddress)
	tx := SimTx{
		Chain:     a.from.Chain,
		ToAddress: inboundAddr,
		Coin:      common.NewCoin(a.from, a.swapAmount),
		Memo:      memo,
	}

	client := a.user.ChainClients[a.from.Chain]

	txid, err := SignAndBroadcastTx(client, tx)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to broadcast tx")
		return OpResult{
			Continue: false,
		}
	}
	a.swapTxID = txid

	a.Log().Info().Str("txid", txid).Msg("broadcasted swap tx")
	return OpResult{
		Continue: true,
	}
}

func (a *SwapActor) sendTokenSwap(config *OpConfig) OpResult {
	if result := a.ensureXMRVaultSpendable(config, a.requiredOutboundSpend); !result.Continue || result.Finish {
		return result
	}

	// get router address
	inboundAddr, routerAddr, err := thornode.GetInboundAddress(a.from.Chain)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get inbound address")
		return OpResult{
			Continue: false,
		}
	}
	if routerAddr == nil {
		a.Log().Error().Msg("failed to get router address")
		return OpResult{
			Continue: false,
		}
	}

	token := evm.Tokens(a.from.Chain)[a.from]

	// convert amount to token decimals
	factor := big.NewInt(1).Exp(big.NewInt(10), big.NewInt(int64(token.Decimals)), nil)
	tokenAmount := a.swapAmount.Mul(cosmos.NewUintFromBigInt(factor))
	tokenAmount = tokenAmount.QuoUint64(common.One)

	// approve the router
	eRouterAddr := ecommon.HexToAddress(routerAddr.String())
	tx := SimContractTx{
		Chain:    a.from.Chain,
		Contract: common.Address(token.Address),
		ABI:      evm.ERC20ABI(),
		Method:   "approve",
		Args:     []interface{}{eRouterAddr, tokenAmount.BigInt()},
	}

	iClient := a.user.ChainClients[a.from.Chain]
	client, ok := iClient.(*evm.Client)
	if !ok {
		a.Log().Fatal().Msg("failed to get evm client")
	}

	// sign approve transaction
	signed, err := client.SignContractTx(tx)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to sign tx")
		return OpResult{
			Continue: false,
		}
	}

	// broadcast approve transaction
	txid, err := client.BroadcastTx(signed)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to broadcast tx")
		return OpResult{
			Continue: false,
		}
	}
	a.Log().Info().Str("txid", txid).Msg("broadcasted router approve tx")

	// call depositWithExpiry
	memo := fmt.Sprintf("=:%s:%s", a.to, a.toAddress)
	expiry := time.Now().Add(time.Hour).Unix()
	eInboundAddr := ecommon.HexToAddress(inboundAddr.String())
	eTokenAddr := ecommon.HexToAddress(token.Address)
	tx = SimContractTx{
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

	// sign deposit transaction
	signed, err = client.SignContractTx(tx)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to sign tx")
		return OpResult{
			Continue: false,
		}
	}

	// broadcast deposit transaction
	txid, err = client.BroadcastTx(signed)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to broadcast tx")
		return OpResult{
			Continue: false,
		}
	}

	a.swapTxID = txid

	a.Log().Info().Str("txid", txid).Msg("broadcasted swap tx")
	return OpResult{
		Continue: true,
	}
}

func (a *SwapActor) verifyOutbound(config *OpConfig) OpResult {
	// get swap stages
	stages, err := thornode.GetTxStages(a.swapTxID)
	if err != nil {
		a.Log().Warn().Err(err).Msg("failed to get tx stages")
		return OpResult{
			Continue: false,
		}
	}

	// wait for outbound to be marked complete
	if stages.SwapFinalised != nil && stages.SwapFinalised.Completed {
		a.releaseTargetXMRSwapSlot()
	}
	if stages.OutboundSigned == nil || !stages.OutboundSigned.Completed {
		return OpResult{
			Continue: false,
		}
	}

	// get tx details
	details, err := thornode.GetTxDetails(a.swapTxID)
	if err != nil {
		a.Log().Warn().Err(err).Msg("failed to get tx details")
		return OpResult{
			Continue: false,
		}
	}

	// verify exactly one out transaction
	if len(details.OutTxs) != 1 {
		return OpResult{
			Error:  fmt.Errorf("expected exactly one out transaction"),
			Finish: true,
		}
	}

	// verify exactly one action
	if len(details.Actions) != 1 {
		return OpResult{
			Error:  fmt.Errorf("expected exactly one action"),
			Finish: true,
		}
	}

	// verify action is not refund
	// For memoless outbounds, Memo may be nil but OriginalMemo contains the actual memo
	actionMemo := ""
	if details.Actions[0].Memo != nil {
		actionMemo = *details.Actions[0].Memo
	} else if details.Actions[0].OriginalMemo != nil {
		actionMemo = *details.Actions[0].OriginalMemo
	}
	if strings.HasPrefix(actionMemo, "REFUND:") {
		return OpResult{
			Error:  fmt.Errorf("expected action to not be a refund"),
			Finish: true,
		}
	}

	// verify outbound amount + max gas within expected range
	action := details.Actions[0]
	out := details.OutTxs[0]
	outAmountPlusGas, err := outboundAmountPlusGas(out, action, a.to)
	if err != nil {
		a.Log().Warn().Err(err).Msg("failed to convert asset")
		return OpResult{
			Continue: false,
		}
	}

	// retrieve L1 balance
	toAcct, err := a.user.ChainClients[a.to.Chain].GetAccount(nil)
	if err != nil {
		a.Log().Warn().Err(err).Msg("failed to get to account")
		return OpResult{
			Continue: false,
		}
	}

	// check received amount
	currentToBalance := accountAssetAmount(toAcct, a.to)
	received := common.SafeSub(currentToBalance, a.toBalance)
	a.Log().Info().
		Stringer("received", received).
		Stringer("currentToBalance", currentToBalance).
		Stringer("outAmountPlusGas", outAmountPlusGas).
		Stringer("minExpected", a.minExpected).
		Stringer("maxExpected", a.maxExpected).
		Msg("swap complete")

	// fail if received amount is outside expected range
	if outAmountPlusGas.LT(a.minExpected) || outAmountPlusGas.GT(a.maxExpected) {
		currentQuote, quoteErr := thornode.GetSwapQuote(a.from, a.to, a.swapAmount, a.toAddress, a.from.Chain.IsUTXO())
		if quoteErr != nil {
			a.Log().Warn().Err(quoteErr).Msg("failed to refresh quote after original expectation drifted")
			err = fmt.Errorf("out amount plus gas outside tolerance")
		} else {
			_, currentMinExpected, currentMaxExpected := swapQuoteExpectedRange(currentQuote)
			a.Log().Info().
				Stringer("currentMinExpected", currentMinExpected).
				Stringer("currentMaxExpected", currentMaxExpected).
				Msg("refreshed quote after original expectation drifted")
			if outAmountPlusGas.LT(currentMinExpected) || outAmountPlusGas.GT(currentMaxExpected) {
				err = fmt.Errorf("out amount plus gas outside tolerance")
			}
		}
	}

	// release user
	a.releaseTargetXMRSwapSlot()
	a.user.Release()

	return OpResult{
		Finish: true,
		Error:  err,
	}
}
