package thorchain

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"math/big"
	"strings"
	"time"

	"gitlab.com/thorchain/thornode/v3/config"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"github.com/cosmos/cosmos-sdk/telemetry"
	"github.com/hashicorp/go-metrics"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

const (
	maxDeviation = 1
)

type PriceFeedQuorumBatchHandler struct {
	mgr             Manager
	requiredSymbols []string
	requiredVersion []byte
}

// NewPriceFeedQuorumBatchHandler create a new instance of PriceFeedQuorumBatchHandler
func NewPriceFeedQuorumBatchHandler(mgr Manager) PriceFeedQuorumBatchHandler {
	symbols := mgr.GetConstants().GetStringValue(constants.RequiredPriceFeeds)

	hash := sha256.Sum256([]byte(symbols))

	return PriceFeedQuorumBatchHandler{
		mgr:             mgr,
		requiredSymbols: strings.Split(symbols, ","),
		requiredVersion: hash[:8],
	}
}

// Run is the main entry point of PriceFeedQuorumBatchHandler
func (h PriceFeedQuorumBatchHandler) Run(ctx cosmos.Context, m cosmos.Msg) (*cosmos.Result, error) {
	msg, ok := m.(*types.MsgPriceFeedQuorumBatch)
	if !ok {
		return nil, errInvalidMessage
	}

	err := h.validate(ctx, *msg)
	if err != nil {
		ctx.Logger().Error("MsgPriceFeedQuorumBatch failed validation", "error", err)
		return nil, err
	}

	result, err := h.handle(ctx, *msg)
	if err != nil {
		ctx.Logger().Error("fail to handle MsgPriceFeedQuorumBatch message", "error", err)
	}
	return result, err
}

func (h PriceFeedQuorumBatchHandler) validate(_ cosmos.Context, msg types.MsgPriceFeedQuorumBatch) error {
	return msg.ValidateBasic()
}

func (h PriceFeedQuorumBatchHandler) handle(ctx cosmos.Context, msg types.MsgPriceFeedQuorumBatch) (*cosmos.Result, error) {
	// ignore price feed tx, if prices have already been processed to avoid
	// to prevent reorder attacks
	iterator := h.mgr.Keeper().GetPriceIterator(ctx)
	defer iterator.Close()
	if iterator.Valid() {
		ctx.Logger().Error("price feed already processed")
		return &cosmos.Result{}, nil
	}

	activeNodes, err := h.mgr.Keeper().ListActiveValidators(ctx)
	if err != nil {
		err = wrapError(ctx, err, "fail to list active validators")
		return nil, err
	}

	bondAddresses := map[string]string{}
	timestamps := []int64{}

	pricesByBondAddress := map[string]map[string]*big.Float{}

	for _, node := range activeNodes {
		nodeAddress := node.NodeAddress.String()
		bondAddress := node.BondAddress.String()

		bondAddresses[nodeAddress] = bondAddress

		_, found := pricesByBondAddress[bondAddress]
		if !found {
			pricesByBondAddress[bondAddress] = map[string]*big.Float{}
		}
	}

	// [BTC][thor1...]*big.Float{}
	pricesByAsset := map[string]map[string]*big.Float{}
	for _, symbol := range h.requiredSymbols {
		pricesByAsset[symbol] = map[string]*big.Float{}
	}

	trackPricePerNode := config.GetThornode().Telemetry.PricePerNode

	for _, qpf := range msg.QuoPriceFeeds {
		pf, err := h.handleQuorumPriceFeed(ctx, qpf, activeNodes)
		if err != nil {
			ctx.Logger().Error("fail to handle QuorumPriceFeed", "error", err)
			continue
		}

		timestamps = append(timestamps, qpf.PriceFeed.Time)

		nodeAddress := pf.Node.String()
		bondAddress, found := bondAddresses[nodeAddress]
		for i, symbol := range h.requiredSymbols {
			rate := pf.Rates[i]
			// remove invalid rates
			if rate.Amount == 0 {
				continue
			}
			pricesByAsset[symbol][nodeAddress] = rate.BigFloat()
			if found {
				pricesByBondAddress[bondAddress][symbol] = rate.BigFloat()
			}
		}
	}

	// Verify and calculate on chain price

	for _, symbol := range h.requiredSymbols {
		priceByNode := pricesByAsset[symbol]

		allPricesForSymbol := []*big.Float{}

		for _, node := range activeNodes {
			nodeAddress := node.NodeAddress.String()
			price, found := priceByNode[nodeAddress]
			if !found {
				// add last price from different node with the same bond address
				bondAddress := node.BondAddress.String()
				price, found = pricesByBondAddress[bondAddress][symbol]
				if !found {
					continue
				}
				priceByNode[nodeAddress] = new(big.Float).Copy(price)
			}

			allPricesForSymbol = append(allPricesForSymbol, price)

			if trackPricePerNode {
				h.telemetryPrice(symbol, nodeAddress, price)
			}
		}

		if !HasSuperMajority(len(priceByNode), len(activeNodes)) {
			ctx.Logger().Error(
				"not enough prices for asset", "asset", symbol,
				"num_prices", len(priceByNode), "num_nodes", len(activeNodes))
			continue
		}

		deviation, median, err := common.MedianAbsoluteDeviation(allPricesForSymbol)
		if err != nil {
			ctx.Logger().Error("fail to compute deviation", "err", err)
			continue
		}

		margin := new(big.Float).Mul(deviation, big.NewFloat(maxDeviation))

		lowerBound := new(big.Float).Sub(median, margin)
		upperBound := new(big.Float).Add(median, margin)

		h.telemetryBounds(symbol, upperBound, lowerBound)

		oraclePrice := OraclePrice{
			Symbol: symbol,
			Price:  median.Text('f', -1),
		}

		err = h.mgr.Keeper().SetPrice(ctx, oraclePrice)
		if err != nil {
			ctx.Logger().Error("fail to set price", "err", err)
		}

		h.telemetryPrice(symbol, "final", median)

		priceEvent := NewEventOraclePrice(symbol, oraclePrice.Price)
		err = h.mgr.EventMgr().EmitEvent(ctx, priceEvent)
		if err != nil {
			ctx.Logger().Error("fail to emit price event", "error", err)
		}
	}

	h.telemetryLatency(time.UnixMilli(common.GetMedianInt64(timestamps)))

	return &cosmos.Result{}, nil
}

func (h PriceFeedQuorumBatchHandler) handleQuorumPriceFeed(
	ctx cosmos.Context,
	qpf *common.QuorumPriceFeed,
	active NodeAccounts,
) (*PriceFeed, error) {
	pf := qpf.PriceFeed

	if !bytes.Equal(pf.Version, h.requiredVersion) {
		return nil, fmt.Errorf("version does not match required version")
	}

	if len(pf.Rates) != len(h.requiredSymbols) {
		return nil, fmt.Errorf("amount of rates does not match required symbols")
	}

	signBz, err := pf.GetSignablePayload()
	if err != nil {
		return nil, fmt.Errorf("fail to get signable price feed payload: %v", err)
	}

	atts := qpf.Attestations

	if len(atts) == 0 {
		return nil, fmt.Errorf("no attestation found")
	}

	if len(atts) > 1 {
		return nil, fmt.Errorf("found more than one attestation")
	}

	att := atts[0]

	_, err = verifyQuorumAttestation(active, signBz, att)
	if err != nil {
		return nil, fmt.Errorf("fail to verify quorum price feed attestation: %v", err)
	}

	pk := secp256k1.PubKey{Key: att.PubKey}
	address := cosmos.AccAddress(pk.Address())

	timestamp := time.UnixMilli(pf.Time)

	if timestamp.Before(ctx.HeaderInfo().Time) {
		return nil, fmt.Errorf("price feed too old: %s", timestamp)
	}

	return &PriceFeed{
		Node:  address,
		Rates: pf.Rates,
	}, nil
}

func (h PriceFeedQuorumBatchHandler) telemetryBounds(
	symbol string,
	upper, lower *big.Float,
) {
	items := []struct {
		Type  string
		Value *big.Float
	}{
		{Type: "upper", Value: upper},
		{Type: "lower", Value: lower},
	}

	for _, item := range items {
		value, _ := item.Value.Float32()
		telemetry.SetGaugeWithLabels(
			[]string{"thornode", "oracle_price_bounds"},
			value,
			[]metrics.Label{
				telemetry.NewLabel("symbol", symbol),
				telemetry.NewLabel("type", item.Type),
			},
		)
	}
}

func (h PriceFeedQuorumBatchHandler) telemetryPrice(
	symbol, node string,
	price *big.Float,
) {
	v, _ := price.Float32()
	telemetry.SetGaugeWithLabels(
		[]string{"thornode", "oracle_price"},
		v,
		[]metrics.Label{
			telemetry.NewLabel("symbol", symbol),
			telemetry.NewLabel("node", node),
		},
	)
}

func (h PriceFeedQuorumBatchHandler) telemetryLatency(
	timestamp time.Time,
) {
	latency := time.Since(timestamp).Seconds()
	telemetry.SetGaugeWithLabels(
		[]string{"thornode", "price_feed_latency"},
		float32(latency),
		[]metrics.Label{},
	)
}
