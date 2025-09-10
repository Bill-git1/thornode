package thorchain

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	btcchaincfg "github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcutil"
	dogechaincfg "github.com/eager7/dogd/chaincfg"
	"github.com/eager7/dogutil"
	bchchaincfg "github.com/gcash/bchd/chaincfg"
	"github.com/gcash/bchutil"
	ltcchaincfg "github.com/ltcsuite/ltcd/chaincfg"
	"github.com/ltcsuite/ltcutil"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/config"
	"gitlab.com/thorchain/thornode/v3/constants"
	mem "gitlab.com/thorchain/thornode/v3/x/thorchain/memo"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

// -------------------------------------------------------------------------------------
// Config
// -------------------------------------------------------------------------------------

const (
	quoteWarning         = "Do not cache this response. Do not send funds after the expiry."
	quoteExpiration      = 15 * time.Minute
	ethBlockRewardAndFee = 3 * 1e18
)

var nullLogger = log.NewNopLogger()

// -------------------------------------------------------------------------------------
// Helpers
// -------------------------------------------------------------------------------------

// getQuoteRecommendedMinAmountFeeMultiplier returns from config the multiplier on the
// outbound fee for the source and destination chains, used to determine the min
// recommended swap amount that should be respected by clients to avoid outbounds and
// refunds being swallowed.
// It falls back to hardcoded default (3) if config field has not been set yet.
func getQuoteRecommendedMinAmountFeeMultiplier() uint64 {
	// Attempt to get from config
	if configMult := config.GetThornode().QuoteRecommendedMinAmountFeeMultiplier; configMult > 0 {
		return configMult
	}

	// Fallback: hardcoded default of 3
	return 3
}

func quoteParseAddress(ctx cosmos.Context, mgr *Mgrs, addrString string, chain common.Chain) (common.Address, error) {
	if addrString == "" {
		return common.NoAddress, nil
	}

	// attempt to parse a raw address
	addr, err := common.NewAddress(addrString)
	if err == nil {
		return addr, nil
	}

	// attempt to lookup a thorname address
	name, err := mgr.Keeper().GetTHORName(ctx, addrString)
	if err != nil {
		return common.NoAddress, fmt.Errorf("unable to parse address: %w", err)
	}

	// find the address for the correct chain
	for _, alias := range name.Aliases {
		if alias.Chain.Equals(chain) {
			return alias.Address, nil
		}
	}

	return common.NoAddress, fmt.Errorf("no thorname alias for chain %s", chain)
}

// parseMultipleAffiliateParams - attempts to parse one or more affiliates + affiliate
// bps from slash-separated strings. skips any that are invalid
func parseMultipleAffiliateParams(ctx cosmos.Context, mgr *Mgrs, affiliateParam, bpParam string) ([]string, []sdkmath.Uint, sdkmath.Uint, error) {
	affParams := make([]string, 0)
	affiliateBps := make([]sdkmath.Uint, 0)
	totalBps := sdkmath.ZeroUint()

	// Split by slash to get individual values, filter out empty strings
	var affiliateParams []string
	var bpParams []string

	if affiliateParam != "" {
		parts := strings.Split(affiliateParam, "/")
		for _, part := range parts {
			if strings.TrimSpace(part) != "" {
				affiliateParams = append(affiliateParams, strings.TrimSpace(part))
			}
		}
	}

	if bpParam != "" {
		parts := strings.Split(bpParam, "/")
		for _, part := range parts {
			if strings.TrimSpace(part) != "" {
				bpParams = append(bpParams, strings.TrimSpace(part))
			}
		}
	}

	// If there is only one bps defined, but multiple affiliates, apply the bps to all affiliates
	switch {
	case len(bpParams) == 1 && len(affiliateParams) == 0:
		// Handle specific case: BPS provided but no affiliate
		return nil, nil, sdkmath.ZeroUint(), fmt.Errorf("BPS value specified but no affiliate: (%d affiliates, %d BPS values)", len(affiliateParams), len(bpParams))
	case len(bpParams) != len(affiliateParams):
		// Handle general mismatch case
		return nil, nil, sdkmath.ZeroUint(), fmt.Errorf("mismatch between number of affiliates (%d) and BPS values (%d)", len(affiliateParams), len(bpParams))
	}

	if len(affiliateParams) > 0 {
		for i, p := range affiliateParams {
			var currentBpParam string
			switch {
			case len(bpParams) == 0:
				continue // Skip if no BPS values at all
			case i >= len(bpParams):
				// This should not happen after the validation above, but keep as safety
				continue
			default:
				currentBpParam = bpParams[i]
			}

			bps, err := cosmos.ParseUint(currentBpParam)
			if err != nil {
				continue
			}

			affParams = append(affParams, p)
			affiliateBps = append(affiliateBps, bps)
			totalBps = totalBps.Add(bps)
		}
	}

	// If there is a mismatch between the number of affiliates and affiliateBps, return an error
	if len(affParams) != len(affiliateBps) {
		return nil, nil, sdkmath.ZeroUint(), fmt.Errorf("mismatch between number of affiliates and affiliate bps")
	}

	if totalBps.GT(sdkmath.NewUint(1000)) {
		return nil, nil, sdkmath.ZeroUint(), fmt.Errorf("total affiliate fee must not be more than 1000 bps")
	}

	return affParams, affiliateBps, totalBps, nil
}

func hasSuffixMatch(suffix string, values []string) bool {
	for _, value := range values {
		if strings.HasSuffix(value, suffix) {
			return true
		}
	}
	return false
}

// quoteConvertAsset - converts amount to target asset using THORChain pools
func quoteConvertAsset(ctx cosmos.Context, mgr *Mgrs, fromAsset common.Asset, amount sdkmath.Uint, toAsset common.Asset) (sdkmath.Uint, error) {
	// no conversion necessary
	if fromAsset.Equals(toAsset) {
		return amount, nil
	}

	// convert to rune
	if !fromAsset.IsRune() {
		// get the fromPool for the from asset
		fromPool, err := mgr.Keeper().GetPool(ctx, fromAsset.GetLayer1Asset())
		if err != nil {
			return sdkmath.ZeroUint(), fmt.Errorf("failed to get pool: %w", err)
		}

		// ensure pool exists
		if fromPool.IsEmpty() {
			return sdkmath.ZeroUint(), fmt.Errorf("pool does not exist")
		}

		amount = fromPool.AssetValueInRune(amount)
	}

	// convert to target asset
	if !toAsset.IsRune() {

		toPool, err := mgr.Keeper().GetPool(ctx, toAsset.GetLayer1Asset())
		if err != nil {
			return sdkmath.ZeroUint(), fmt.Errorf("failed to get pool: %w", err)
		}

		// ensure pool exists
		if toPool.IsEmpty() {
			return sdkmath.ZeroUint(), fmt.Errorf("pool does not exist")
		}

		amount = toPool.RuneValueInAsset(amount)
	}

	return amount, nil
}

func quoteReverseFuzzyAsset(ctx cosmos.Context, mgr *Mgrs, asset common.Asset) (common.Asset, error) {
	// get all pools
	pools, err := mgr.Keeper().GetPools(ctx)
	if err != nil {
		return asset, fmt.Errorf("failed to get pools: %w", err)
	}

	// return the asset if no symbol to shorten
	aSplit := strings.Split(asset.Symbol.String(), "-")
	if len(aSplit) == 1 {
		return asset, nil
	}

	// find all other assets that match the chain and ticker
	// (without exactly matching the symbol)
	addressMatches := []string{}
	for _, p := range pools {
		if p.IsAvailable() && !p.IsEmpty() && !p.Asset.IsSyntheticAsset() &&
			!p.Asset.Symbol.Equals(asset.Symbol) &&
			p.Asset.Chain.Equals(asset.Chain) && p.Asset.Ticker.Equals(asset.Ticker) {
			pSplit := strings.Split(p.Asset.Symbol.String(), "-")
			if len(pSplit) != 2 {
				return asset, fmt.Errorf("ambiguous match: %s", p.Asset.Symbol)
			}
			addressMatches = append(addressMatches, pSplit[1])
		}
	}

	if len(addressMatches) == 0 { // if only one match, drop the address
		asset.Symbol = common.Symbol(asset.Ticker)
	} else { // find the shortest unique suffix of the asset symbol
		address := aSplit[1]

		for i := len(address) - 1; i > 0; i-- {
			if !hasSuffixMatch(address[i:], addressMatches) {
				asset.Symbol = common.Symbol(
					fmt.Sprintf("%s-%s", asset.Ticker, address[i:]),
				)
				break
			}
		}
	}

	return asset, nil
}

// NOTE: streamingQuantity > 0 is a precondition.
func quoteSimulateSwap(ctx cosmos.Context, mgr *Mgrs, amount sdkmath.Uint, msg *MsgSwap, streamingQuantity uint64) (
	res *types.QueryQuoteSwapResponse, emitAmount, outboundFeeAmount sdkmath.Uint, err error,
) {
	// should be unreachable
	if streamingQuantity == 0 {
		return nil, sdkmath.ZeroUint(), sdkmath.ZeroUint(), fmt.Errorf("streaming quantity must be greater than zero")
	}

	msg.Tx.Coins[0].Amount = msg.Tx.Coins[0].Amount.QuoUint64(streamingQuantity)

	// simulate the swap
	events, err := simulate(ctx, mgr, msg)
	if err != nil {
		return nil, sdkmath.ZeroUint(), sdkmath.ZeroUint(), err
	}

	// extract events
	var swaps []map[string]string
	var fee map[string]string
	for _, e := range events {
		switch e.Type {
		case "swap":
			swaps = append(swaps, eventMap(e))
		case "fee":
			fee = eventMap(e)
		}
	}

	finalSwap := swaps[len(swaps)-1]

	// parse outbound fee from event
	outboundFeeCoin, err := common.ParseCoin(fee["coins"])
	if err != nil {
		return nil, sdkmath.ZeroUint(), sdkmath.ZeroUint(), fmt.Errorf("unable to parse outbound fee coin: %w", err)
	}
	outboundFeeAmount = outboundFeeCoin.Amount

	// parse outbound amount from event
	emitCoin, err := common.ParseCoin(finalSwap["emit_asset"])
	if err != nil {
		return nil, sdkmath.ZeroUint(), sdkmath.ZeroUint(), fmt.Errorf("unable to parse emit coin: %w", err)
	}
	emitAmount = emitCoin.Amount.MulUint64(streamingQuantity)

	// sum the liquidity fees and convert to target asset
	liquidityFee := sdkmath.ZeroUint()
	for _, s := range swaps {
		liquidityFee = liquidityFee.Add(sdkmath.NewUintFromString(s["liquidity_fee_in_rune"]))
	}
	var targetPool types.Pool
	if !msg.TargetAsset.IsRune() {
		targetPool, err = mgr.Keeper().GetPool(ctx, msg.TargetAsset.GetLayer1Asset())
		if err != nil {
			return nil, sdkmath.ZeroUint(), sdkmath.ZeroUint(), fmt.Errorf("unable to get pool: %w", err)
		}
		liquidityFee = targetPool.RuneValueInAsset(liquidityFee)
	}
	liquidityFee = liquidityFee.MulUint64(streamingQuantity)

	// compute slip based on emit amount instead of slip in event to handle double swap
	slippageBps := liquidityFee.MulUint64(10000).Quo(emitAmount.Add(liquidityFee))

	// build fees
	totalFees := liquidityFee.Add(outboundFeeAmount)
	fees := types.QuoteFees{
		Asset:       msg.TargetAsset.String(),
		Liquidity:   liquidityFee.String(),
		Outbound:    outboundFeeAmount.String(),
		Total:       totalFees.String(),
		SlippageBps: slippageBps.BigInt().Int64(),
		TotalBps:    totalFees.MulUint64(10000).Quo(emitAmount.Add(totalFees)).BigInt().Int64(),
	}

	// build response from simulation result events
	return &types.QueryQuoteSwapResponse{
		ExpectedAmountOut: emitAmount.String(),
		Fees:              &fees,
	}, emitAmount, outboundFeeAmount, nil
}

func convertThorchainAmountToWei(amt *big.Int) *big.Int {
	return big.NewInt(0).Mul(amt, big.NewInt(common.One*100))
}

func quoteInboundInfo(ctx cosmos.Context, mgr *Mgrs, amount sdkmath.Uint, chain common.Chain, asset common.Asset) (address, router common.Address, confirmations int64, err error) {
	// If inbound chain is THORChain there is no inbound address
	if chain.IsTHORChain() {
		address = common.NoAddress
		router = common.NoAddress
	} else {
		// get the most secure vault for inbound
		active, err := mgr.Keeper().GetAsgardVaultsByStatus(ctx, ActiveVault)
		if err != nil {
			return common.NoAddress, common.NoAddress, 0, err
		}
		constAccessor := mgr.GetConstants()
		signingTransactionPeriod := constAccessor.GetInt64Value(constants.SigningTransactionPeriod)
		vault := mgr.Keeper().GetMostSecure(ctx, active, signingTransactionPeriod)
		address, err = vault.GetAddress(chain)
		if err != nil {
			return common.NoAddress, common.NoAddress, 0, err
		}

		router = common.NoAddress
		if chain.IsEVM() {
			router = vault.GetContract(chain).Router
		}
	}

	// estimate the inbound confirmation count blocks: ceil(amount/coinbase * conf adjustment)
	confMul, err := mgr.Keeper().GetMimirWithRef(ctx, constants.MimirTemplateConfMultiplierBasisPoints, chain.String())
	if confMul < 0 || err != nil {
		confMul = int64(constants.MaxBasisPts)
	}
	if chain.DefaultCoinbase() > 0 {
		confValue := common.GetUncappedShare(cosmos.NewUint(uint64(confMul)), cosmos.NewUint(constants.MaxBasisPts), cosmos.NewUint(uint64(chain.DefaultCoinbase())*common.One))
		confirmations = amount.Quo(confValue).BigInt().Int64()
		if !amount.Mod(confValue).IsZero() {
			confirmations++
		}
	} else if chain.Equals(common.ETHChain) {
		// copying logic from getBlockRequiredConfirmation of ethereum.go
		// convert amount to ETH
		gasAssetAmount, err := quoteConvertAsset(ctx, mgr, asset, amount, chain.GetGasAsset())
		if err != nil {
			return common.NoAddress, common.NoAddress, 0, fmt.Errorf("unable to convert asset: %w", err)
		}
		gasAssetAmountWei := convertThorchainAmountToWei(gasAssetAmount.BigInt())
		confValue := common.GetUncappedShare(cosmos.NewUint(uint64(confMul)), cosmos.NewUint(constants.MaxBasisPts), cosmos.NewUintFromBigInt(big.NewInt(ethBlockRewardAndFee)))
		confirmations = int64(cosmos.NewUintFromBigInt(gasAssetAmountWei).MulUint64(2).Quo(confValue).Uint64())
	}

	// max confirmation adjustment for btc and eth
	if chain.Equals(common.BTCChain) || chain.Equals(common.ETHChain) {
		maxConfirmations, err := mgr.Keeper().GetMimirWithRef(ctx, constants.MimirTemplateMaxConfirmations, chain.String())
		if maxConfirmations < 0 || err != nil {
			maxConfirmations = 0
		}
		if maxConfirmations > 0 && confirmations > maxConfirmations {
			confirmations = maxConfirmations
		}
	}

	// min confirmation adjustment
	confFloor := map[common.Chain]int64{
		common.ETHChain:  2,
		common.DOGEChain: 2,
		common.BASEChain: 12, // NOTE: additional inconsistent lag since we scan the "safe" block
		common.TRONChain: 3,  // TODO: Discuss, if enough
	}
	if floor := confFloor[chain]; confirmations < floor {
		confirmations = floor
	}

	return address, router, confirmations, nil
}

func quoteOutboundInfo(ctx cosmos.Context, mgr *Mgrs, coin common.Coin) (int64, error) {
	toi := TxOutItem{
		Memo: "OUT:-",
		Coin: coin,
	}
	outboundHeight, _, err := mgr.txOutStore.CalcTxOutHeight(ctx, mgr.GetVersion(), toi)
	if err != nil {
		return 0, err
	}
	return outboundHeight - ctx.BlockHeight(), nil
}

// -------------------------------------------------------------------------------------
// Swap
// -------------------------------------------------------------------------------------

// calculateMinSwapAmount returns the recommended minimum swap amount The recommended
// min swap amount is: - MAX(
//
//	  outbound_fee(src_chain) * 4,
//	  outbound_fee(dest_chain) * 4,
//	  (native_tx_fee_rune * 2) * 10,000 / affiliateBps
//	)
//
// The reason the base value is the MAX of the outbound fees of each chain is because if
// the swap is refunded the input amount will need to cover the outbound fee of the
// source chain. A 3x buffer is applied because outbound fees can spike quickly, meaning
// the original input amount could be less than the new outbound fee. If this happens
// and the swap is refunded, the refund will fail, and the user will lose the entire
// input amount.
func calculateMinSwapAmount(ctx cosmos.Context, mgr *Mgrs, fromAsset, toAsset common.Asset) (cosmos.Uint, error) {
	srcOutboundFee, err := mgr.GasMgr().GetAssetOutboundFee(ctx, fromAsset, false)
	if err != nil {
		return cosmos.ZeroUint(), fmt.Errorf("fail to get outbound fee for source chain gas asset %s: %w", fromAsset, err)
	}
	destOutboundFee, err := mgr.GasMgr().GetAssetOutboundFee(ctx, toAsset, false)
	if err != nil {
		return cosmos.ZeroUint(), fmt.Errorf("fail to get outbound fee for destination chain gas asset %s: %w", toAsset, err)
	}

	if fromAsset.GetChain().IsTHORChain() && toAsset.GetChain().IsTHORChain() {
		// If this is a purely THORChain swap, no need to give a 3x buffer since outbound fees do not change
		// 2x buffer should suffice
		return srcOutboundFee.Mul(cosmos.NewUint(2)), nil
	}

	destInSrcAsset, err := quoteConvertAsset(ctx, mgr, toAsset, destOutboundFee, fromAsset)
	if err != nil {
		return cosmos.ZeroUint(), fmt.Errorf("fail to convert dest fee to src asset %w", err)
	}

	minSwapAmount := srcOutboundFee
	if destInSrcAsset.GT(srcOutboundFee) {
		minSwapAmount = destInSrcAsset
	}

	minSwapAmount = minSwapAmount.Mul(cosmos.NewUint(getQuoteRecommendedMinAmountFeeMultiplier()))

	return minSwapAmount, nil
}

func (qs queryServer) queryQuoteSwap(ctx cosmos.Context, req *types.QueryQuoteSwapRequest) (*types.QueryQuoteSwapResponse, error) {
	// validate required parameters
	if len(req.FromAsset) == 0 {
		return nil, fmt.Errorf("missing from_asset parameter")
	}

	if len(req.ToAsset) == 0 {
		return nil, fmt.Errorf("missing to_asset parameter")
	}

	if len(req.Amount) == 0 {
		return nil, fmt.Errorf("missing Amount parameter")
	}

	if len(req.ToleranceBps) > 0 && len(req.LiquidityToleranceBps) > 0 {
		return nil, fmt.Errorf("must only include one of: tolerance_bps or liquidity_tolerance_bps")
	}

	// parse assets
	fromAsset, err := common.NewAssetWithShortCodes(qs.mgr.GetVersion(), req.FromAsset)
	if err != nil {
		return nil, fmt.Errorf("bad from asset: %w", err)
	}
	fromAsset = fuzzyAssetMatch(ctx, qs.mgr.Keeper(), fromAsset)
	toAsset, err := common.NewAssetWithShortCodes(qs.mgr.GetVersion(), req.ToAsset)
	if err != nil {
		return nil, fmt.Errorf("bad to asset: %w", err)
	}
	toAsset = fuzzyAssetMatch(ctx, qs.mgr.Keeper(), toAsset)

	// parse amount
	amount, err := cosmos.ParseUint(req.Amount)
	if err != nil {
		return nil, fmt.Errorf("bad amount: %w", err)
	}

	if amount.LT(fromAsset.Chain.DustThreshold()) {
		return nil, fmt.Errorf("amount less than dust threshold")
	}

	// check if the amount is less than the minimum swap amount
	minSwapAmount, err := calculateMinSwapAmount(ctx, qs.mgr, fromAsset, toAsset)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate min swap amount: %w", err)
	}

	// parse streaming interval
	streamingInterval := uint64(0) // default value
	if len(req.StreamingInterval) > 0 {
		streamingInterval, err = strconv.ParseUint(req.StreamingInterval, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("bad streaming interval amount: %w", err)
		}
	}
	streamingQuantity := uint64(0) // default value
	if len(req.StreamingQuantity) > 0 {
		streamingQuantity, err = strconv.ParseUint(req.StreamingQuantity, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("bad streaming quantity amount: %w", err)
		}
	}
	swp := StreamingSwap{
		Interval: streamingInterval,
		Deposit:  amount,
	}
	maxSwapQuantity, err := getMaxSwapQuantity(ctx, qs.mgr, fromAsset, toAsset, swp)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate max streaming swap quantity: %w", err)
	}

	// cap the streaming quantity to the max swap quantity
	if streamingQuantity > maxSwapQuantity {
		streamingQuantity = maxSwapQuantity
	}

	// if from asset is a synth, transfer asset to asgard module
	if fromAsset.IsSyntheticAsset() {
		// mint required coins to asgard so swap can be simulated
		err = qs.mgr.Keeper().MintToModule(ctx, ModuleName, common.NewCoin(fromAsset, amount))
		if err != nil {
			return nil, fmt.Errorf("failed to mint coins to module: %w", err)
		}

		err = qs.mgr.Keeper().SendFromModuleToModule(ctx, ModuleName, AsgardName, common.NewCoins(common.NewCoin(fromAsset, amount)))
		if err != nil {
			return nil, fmt.Errorf("failed to send coins to asgard: %w", err)
		}
	}

	// trade assets must have from address on the source tx
	fromChain := fromAsset.Chain
	if fromAsset.IsSyntheticAsset() || fromAsset.IsDerivedAsset() || fromAsset.IsTradeAsset() || fromAsset.IsSecuredAsset() {
		fromChain = common.THORChain
	}
	fromPubkey := types.GetRandomPubkeyForChain(fromChain)
	fromAddress, err := fromPubkey.GetAddress(fromChain)
	if err != nil {
		return nil, fmt.Errorf("bad from address: %w", err)
	}

	// if from asset is a trade asset, create fake balance
	if fromAsset.IsTradeAsset() {
		thorAddr, err := fromPubkey.GetThorAddress()
		if err != nil {
			return nil, fmt.Errorf("failed to get thor address: %w", err)
		}
		_, err = qs.mgr.TradeAccountManager().Deposit(ctx, fromAsset, amount, thorAddr, common.NoAddress, common.BlankTxID)
		if err != nil {
			return nil, fmt.Errorf("failed to deposit trade asset: %w", err)
		}
	}

	// parse destination address or generate a random one
	sendMemo := true
	var destination common.Address
	if len(req.Destination) > 0 {
		destination, err = quoteParseAddress(ctx, qs.mgr, req.Destination, toAsset.Chain)
		if err != nil {
			return nil, fmt.Errorf("bad destination address: %w", err)
		}

	} else {
		chain := common.THORChain
		if !toAsset.IsSyntheticAsset() {
			chain = toAsset.Chain
		}

		destination, err = types.GetRandomPubkeyForChain(chain).GetAddress(chain)
		if err != nil {
			return nil, fmt.Errorf("failed to generate address: %w", err)
		}
		sendMemo = false // do not send memo if destination was random
	}

	// parse tolerance basis points
	limit := sdkmath.ZeroUint()
	liquidityToleranceBps := sdkmath.ZeroUint()
	if len(req.ToleranceBps) > 0 {
		// validate tolerance basis points
		toleranceBasisPoints, err := sdkmath.ParseUint(req.ToleranceBps)
		if err != nil {
			return nil, fmt.Errorf("bad tolerance basis points: %w", err)
		}
		if toleranceBasisPoints.GT(sdkmath.NewUint(10000)) {
			return nil, fmt.Errorf("tolerance basis points must be less than 10000")
		}

		// convert to a limit of target asset amount assuming zero fees and slip
		feelessEmit, err := quoteConvertAsset(ctx, qs.mgr, fromAsset, amount, toAsset)
		if err != nil {
			return nil, err
		}

		limit = feelessEmit.MulUint64(10000 - toleranceBasisPoints.Uint64()).QuoUint64(10000)
	} else if len(req.LiquidityToleranceBps) > 0 {
		liquidityToleranceBps, err = sdkmath.ParseUint(req.LiquidityToleranceBps)
		if err != nil {
			return nil, fmt.Errorf("bad liquidity tolerance basis points: %w", err)
		}
		if liquidityToleranceBps.GTE(sdkmath.NewUint(10000)) {
			return nil, fmt.Errorf("liquidity tolerance basis points must be less than 10000")
		}
	}

	// custom refund addr
	refundAddress := common.NoAddress
	if len(req.RefundAddress) > 0 {
		refundAddress, err = quoteParseAddress(ctx, qs.mgr, req.RefundAddress, fromAsset.Chain)
		if err != nil {
			return nil, fmt.Errorf("bad refund address: %w", err)
		}
	}

	// parse affiliate params
	affiliates, affiliateBps, totalBps, err := parseMultipleAffiliateParams(ctx, qs.mgr, req.Affiliate, req.AffiliateBps)
	if err != nil {
		return nil, fmt.Errorf("bad affiliate params: %w", err)
	}

	// always attempt to shorten the to asset to fuzzy match
	fuzzyToAsset, err := quoteReverseFuzzyAsset(ctx, qs.mgr, toAsset)
	memoToAsset := toAsset
	if err == nil {
		memoToAsset = fuzzyToAsset
	}

	// create the memo
	memo := &SwapMemo{
		MemoBase: mem.MemoBase{
			TxType: TxSwap,
			Asset:  memoToAsset,
		},
		Destination:           destination,
		SlipLimit:             limit,
		Affiliates:            affiliates,
		AffiliatesBasisPoints: affiliateBps,
		AffiliateBasisPoints:  totalBps,
		StreamInterval:        streamingInterval,
		StreamQuantity:        streamingQuantity,
		RefundAddress:         refundAddress,
	}
	memoString := memo.ShortString()

	// if from asset is a trade asset, create fake balance
	if fromAsset.IsTradeAsset() {
		thorAddr, err := fromPubkey.GetThorAddress()
		if err != nil {
			return nil, fmt.Errorf("failed to get thor address: %w", err)
		}
		_, err = qs.mgr.TradeAccountManager().Deposit(ctx, fromAsset, amount, thorAddr, common.NoAddress, common.BlankTxID)
		if err != nil {
			return nil, fmt.Errorf("failed to deposit trade asset: %w", err)
		}
	}

	// if from asset is a secured asset, create fake balance
	if fromAsset.IsSecuredAsset() {
		thorAddr, err := fromPubkey.GetThorAddress()
		if err != nil {
			return nil, fmt.Errorf("failed to get thor address: %w", err)
		}
		_, err = qs.mgr.SecuredAssetManager().Deposit(ctx, fromAsset.GetLayer1Asset(), amount, thorAddr, common.NoAddress, common.BlankTxID)
		if err != nil {
			return nil, fmt.Errorf("failed to deposit secured asset: %w", err)
		}
	}

	// create the swap message
	msg := &types.MsgSwap{
		Tx: common.Tx{
			ID:          common.BlankTxID,
			Chain:       fromAsset.Chain,
			FromAddress: fromAddress,
			ToAddress:   common.NoopAddress,
			Coins: []common.Coin{
				{
					Asset:  fromAsset,
					Amount: amount,
				},
			},
			Gas: []common.Coin{{
				Asset:  common.RuneAsset(),
				Amount: sdkmath.NewUint(1),
			}},
			Memo: memoString,
		},
		TargetAsset:          toAsset,
		TradeTarget:          limit,
		Destination:          destination,
		AffiliateAddress:     common.NoAddress,
		AffiliateBasisPoints: cosmos.ZeroUint(),
	}

	// simulate the swap
	res, emitAmount, outboundFeeAmount, err := quoteSimulateSwap(ctx, qs.mgr, amount, msg, 1)
	if err != nil {
		if errors.Is(err, ErrNotEnoughToPayFee) {
			return nil, fmt.Errorf("amount less than min swap amount (recommended_min_amount_in: %s)", minSwapAmount.String())
		}
		return nil, fmt.Errorf("failed to simulate swap: %w", err)
	}

	// if we're using a streaming swap, calculate emit amount by a sub-swap amount instead
	// of the full amount, then multiply the result by the swap count
	if streamingInterval > 0 && streamingQuantity == 0 {
		streamingQuantity = maxSwapQuantity
	}
	if streamingInterval > 0 && streamingQuantity > 0 {
		msg.TradeTarget = msg.TradeTarget.QuoUint64(streamingQuantity)
		// simulate the swap
		var streamRes *types.QueryQuoteSwapResponse
		streamRes, emitAmount, _, err = quoteSimulateSwap(ctx, qs.mgr, amount, msg, streamingQuantity)
		if err != nil {
			if errors.Is(err, ErrNotEnoughToPayFee) {
				return nil, fmt.Errorf("amount less than min swap amount (recommended_min_amount_in: %s)", minSwapAmount.String())
			}
			return nil, fmt.Errorf("failed to simulate swap: %w", err)
		}
		res.Fees = streamRes.Fees
	}

	// TODO: After UIs have transitioned everything below the message definition above
	// should reduce to the following:
	//
	// if streamingInterval > 0 && streamingQuantity == 0 {
	//   streamingQuantity = maxSwapQuantity
	// }
	// if streamingInterval > 0 && streamingQuantity > 0 {
	//   msg.TradeTarget = msg.TradeTarget.QuoUint64(streamingQuantity)
	// }
	// res, emitAmount, outboundFeeAmount, err := quoteSimulateSwap(ctx, mgr, amount, msg, streamingQuantity)
	// if err != nil {
	//   return quoteErrorResponse(fmt.Errorf("failed to simulate swap: %w", err))
	// }

	totalAffFee := cosmos.ZeroUint()
	// attempt each affiliate fee, skipping those that won't succeed
	if len(affiliates) > 0 && len(affiliateBps) > 0 {
		// Attempt each affiliate swap
		for _, bps := range affiliateBps {
			if bps.IsZero() {
				continue
			}
			affAmt := common.GetSafeShare(bps, cosmos.NewUint(10000), emitAmount)
			totalAffFee = totalAffFee.Add(affAmt)
		}
	}
	// Update fees with affiliate fee & re-calculate total fee bps
	res.Fees.Affiliate = totalAffFee.String()
	totalFees, err := sdkmath.ParseUint(res.Fees.Total)
	if err != nil {
		return nil, fmt.Errorf("failed to parse total fees: %w", err)
	}
	totalFees = totalFees.Add(totalAffFee)
	res.Fees.Total = totalFees.String()
	res.Fees.TotalBps = totalFees.MulUint64(10000).Quo(emitAmount.Add(totalFees)).BigInt().Int64()
	emitAmount = emitAmount.Sub(totalAffFee)

	// check invariant
	if emitAmount.LT(outboundFeeAmount) {
		return nil, fmt.Errorf("invariant broken: emit %s less than outbound fee %s", emitAmount, outboundFeeAmount)
	}

	// the amount out will deduct the outbound fee
	res.ExpectedAmountOut = emitAmount.Sub(outboundFeeAmount).String()

	// add liquidty_tolerance_bps to the memo
	if liquidityToleranceBps.GT(sdkmath.ZeroUint()) {
		outputLimit := emitAmount.Sub(outboundFeeAmount).MulUint64(10000 - liquidityToleranceBps.Uint64()).QuoUint64(10000)
		memo.SlipLimit = outputLimit
		memoString = memo.ShortString()
	}

	// shorten the memo if necessary
	if !fromAsset.IsNative() && len(memoString) > fromAsset.GetChain().MaxMemoLength() {
		// this is the shortest we can make it
		maxMemoLength := fromAsset.GetChain().MaxMemoLength()
		if fromChain.IsUTXO() && req.Extended {
			maxMemoLength = constants.MaxMemoSizeUtxoExtended
		}
		if len(memoString) > maxMemoLength {
			return nil, fmt.Errorf("generated memo too long for source chain")
		}
	}

	maxQ := int64(maxSwapQuantity)
	res.MaxStreamingQuantity = maxQ
	var streamSwapBlocks int64
	if streamingQuantity > 0 {
		streamSwapBlocks = int64(streamingInterval) * int64(streamingQuantity-1)
	}
	res.StreamingSwapBlocks = streamSwapBlocks
	res.StreamingSwapSeconds = streamSwapBlocks * common.THORChain.ApproximateBlockMilliseconds() / 1000

	// estimate the inbound info
	inboundAddress, routerAddress, inboundConfirmations, err := quoteInboundInfo(ctx, qs.mgr, amount, fromAsset.GetChain(), fromAsset)
	if err != nil {
		return nil, err
	}
	res.InboundAddress = inboundAddress.String()
	if inboundConfirmations > 0 {
		res.InboundConfirmationBlocks = inboundConfirmations
		res.InboundConfirmationSeconds = inboundConfirmations * msg.Tx.Chain.ApproximateBlockMilliseconds() / 1000
	}

	res.OutboundDelayBlocks = 0
	res.OutboundDelaySeconds = 0
	if !toAsset.Chain.IsTHORChain() {
		// estimate the outbound info
		outboundDelay, err := quoteOutboundInfo(ctx, qs.mgr, common.Coin{Asset: toAsset, Amount: emitAmount})
		if err != nil {
			return nil, err
		}
		res.OutboundDelayBlocks = outboundDelay
		res.OutboundDelaySeconds = outboundDelay * common.THORChain.ApproximateBlockMilliseconds() / 1000
	}

	totalSeconds := res.OutboundDelaySeconds
	// TODO: can outbound delay seconds be negative?
	if res.StreamingSwapSeconds != 0 && res.OutboundDelaySeconds < res.StreamingSwapSeconds {
		totalSeconds = res.StreamingSwapSeconds
	}
	if inboundConfirmations > 0 {
		totalSeconds += res.InboundConfirmationSeconds
	}
	res.TotalSwapSeconds = totalSeconds

	// send memo if the destination was provided
	if sendMemo {
		res.Memo = memoString
	}

	// set info fields
	if fromAsset.Chain.IsEVM() {
		res.Router = routerAddress.String()
	}
	if !fromAsset.Chain.DustThreshold().IsZero() {
		res.DustThreshold = fromAsset.Chain.DustThreshold().String()
	}

	res.Notes = fromAsset.GetChain().InboundNotes()
	if outboundNotes := toAsset.Chain.OutboundNotes(); outboundNotes != "" {
		res.Notes += " " + outboundNotes
	}
	res.Warning = quoteWarning
	res.Expiry = time.Now().Add(quoteExpiration).Unix()
	res.RecommendedMinAmountIn = minSwapAmount.String()

	// set inbound recommended gas for non-native swaps
	if !fromAsset.Chain.IsTHORChain() {
		inboundGas := qs.mgr.GasMgr().GetGasRate(ctx, fromAsset.Chain)
		res.RecommendedGasRate = inboundGas.String()
		res.GasRateUnits, _ = fromAsset.Chain.GetGasUnits()
	}

	if !fromChain.IsUTXO() || !req.Extended {
		return res, nil
	}

	network := common.CurrentChainNetwork
	parts := splitMemo(memoString)
	vout := make([]*types.Vout, len(parts))

	for i, part := range parts {
		if i == 0 {
			vout[i] = &types.Vout{
				Type:   "op_return",
				Data:   part,
				Amount: 0,
			}
			continue
		}

		data, err := hex.DecodeString(part)
		if err != nil {
			return nil, err
		}

		var address string
		amount := fromChain.P2WPKHOutputValue()

		switch fromChain {
		case common.BTCChain:
			params := &btcchaincfg.MainNetParams
			if network == common.MockNet {
				params = &btcchaincfg.RegressionNetParams
			}
			hash, err := btcutil.NewAddressWitnessPubKeyHash(data, params)
			if err != nil {
				return nil, err
			}
			address = hash.String()
		case common.LTCChain:
			params := &ltcchaincfg.MainNetParams
			if network == common.MockNet {
				params = &ltcchaincfg.RegressionNetParams
			}
			hash, err := ltcutil.NewAddressWitnessPubKeyHash(data, params)
			if err != nil {
				return nil, err
			}
			address = hash.String()
		case common.DOGEChain:
			params := &dogechaincfg.MainNetParams
			if network == common.MockNet {
				params = &dogechaincfg.RegressionNetParams
			}
			hash, err := dogutil.NewAddressPubKeyHash(data, params)
			if err != nil {
				return nil, err
			}
			address = hash.String()
		case common.BCHChain:
			params := &bchchaincfg.MainNetParams
			if network == common.MockNet {
				params = &bchchaincfg.RegressionNetParams
			}
			hash, err := bchutil.NewAddressPubKeyHash(data, params)
			if err != nil {
				return nil, err
			}
			address = hash.String()
		default:
			return nil, fmt.Errorf("chain not supported")
		}

		vout[i] = &types.Vout{
			Type:   "address",
			Data:   address,
			Amount: amount,
		}
	}

	res.Vout = vout

	return res, nil
}

// splitMemo converts an arbitrary string into a hex string and splits that
// into one or more parts, with the first part being 80 bytes and every other
// part 20 bytes, appending zero to the last part until it matches 20 bytes.
// It is used for sending memos longer than 80 bytes on UTXO chains.
func splitMemo(memo string) []string {
	chunks := []string{}

	encoded := hex.EncodeToString([]byte(memo))

	// OP_RETURN data part: use first 79 chars + "^"
	// calculation uses hex encoded data representation (bytes * 2)
	if len(encoded) > 160 {
		chunks = append(chunks, encoded[:158]+"5e") // 0x5e == "^"
		encoded = encoded[158:]
	} else {
		chunks = append(chunks, encoded)
		encoded = ""
	}

	// encode remaining memo data into "fake addresses" of 20 bytes each
	for len(encoded) > 0 {
		index := min(len(encoded), 40)
		chunk := encoded[0:index]
		encoded = encoded[index:]
		for len(chunk) < 40 {
			chunk += "00"
		}
		chunks = append(chunks, chunk)
	}

	return chunks
}
