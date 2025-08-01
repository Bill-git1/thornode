package thorchain

import (
	"crypto/sha256"
	"math/big"
	"strings"
	"time"

	"github.com/cometbft/cometbft/crypto"
	"github.com/cometbft/cometbft/crypto/secp256k1"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
	. "gopkg.in/check.v1"
)

type HandlerPriceFeedQuorumBatchSuite struct {
	symbols []string
	version []byte
}

var _ = Suite(&HandlerPriceFeedQuorumBatchSuite{})

func (s *HandlerPriceFeedQuorumBatchSuite) SetUpSuite(c *C) {
	_, mgr := setupManagerForTest(c)
	symbols := mgr.GetConstants().GetStringValue(constants.RequiredPriceFeeds)

	hash := sha256.Sum256([]byte(symbols))

	s.symbols = strings.Split(symbols, ",")
	s.version = hash[:8]
}

func (s *HandlerPriceFeedQuorumBatchSuite) TestPriceFeedQuorumBatch(c *C) {
	testCases := []struct {
		Feeds   []map[string]float64
		Results map[string]string
	}{
		{
			// happy path: all values in reasonable range
			Feeds: []map[string]float64{
				{
					"BTC": 100123,
					"ETH": 2612,
				}, {
					"BTC": 100145,
					"ETH": 2616,
				}, {
					"BTC": 100160,
					"ETH": 2617,
				}, {
					"BTC": 100170,
					"ETH": 2618,
				},
			},
			Results: map[string]string{
				"BTC": "100152.5",
				"ETH": "2616.5",
			},
		},
		{
			// extreme values are filtered and not used for calculation
			Feeds: []map[string]float64{
				{
					"BTC": 100123,
					"ETH": 2612,
				}, {
					"BTC": 999999,
					"ETH": 2616,
				}, {
					"BTC": 100160,
					"ETH": 2617,
				}, {
					"BTC": 100170,
					"ETH": 0,
				},
			},
			Results: map[string]string{
				"BTC": "100165",
				"ETH": "2616",
			},
		},
		{
			// only zeros: any 0 price is discarded
			Feeds: []map[string]float64{
				{
					"BTC": 0,
					"ETH": 2612,
				}, {
					"BTC": 0,
					"ETH": 2617,
				},
			},
			Results: map[string]string{
				"ETH": "2614.5",
			},
		},
		{
			// valid values need simple majority
			Feeds: []map[string]float64{
				{
					"BTC": 100123,
					"ETH": 2612,
				}, {
					"BTC": 100145,
					"ETH": 2616,
				}, {
					"BTC": 100160,
				}, {
					"BNB": 669,
				},
			},
			Results: map[string]string{
				"BTC": "100145",
			},
		},
	}

	for _, tc := range testCases {
		ctx, mgr := setupManagerForTest(c)
		handler := NewPriceFeedQuorumBatchHandler(mgr)

		var feeds []*common.QuorumPriceFeed

		for _, values := range tc.Feeds {
			_, feed, node, err := s.setUp(values)
			c.Assert(err, IsNil)
			c.Assert(mgr.K.SetNodeAccount(ctx, node), IsNil)

			feeds = append(feeds, feed)
		}

		msg := NewMsgPriceFeedQuorumBatch(feeds, GetRandomBech32Addr())
		c.Assert(msg.QuoPriceFeeds, HasLen, len(tc.Feeds))

		_, err := handler.Run(ctx, msg)
		c.Assert(err, IsNil)

		results := map[string]string{}

		iterator := mgr.K.GetPriceIterator(ctx)
		for ; iterator.Valid(); iterator.Next() {
			var price OraclePrice
			err = mgr.K.Cdc().Unmarshal(iterator.Value(), &price)
			c.Assert(err, IsNil)
			results[price.Symbol] = price.Price
		}

		c.Assert(results, HasLen, len(tc.Results))
		for symbol, result := range results {
			expected, found := tc.Results[symbol]
			c.Assert(found, Equals, true)
			c.Assert(result, Equals, expected)
		}
	}
}

func (s *HandlerPriceFeedQuorumBatchSuite) TestMultipleTxs(c *C) {
	// only one price feed msg is processed per block
	// each subsequent msg must fail

	ctx, mgr := setupManagerForTest(c)
	handler := NewPriceFeedQuorumBatchHandler(mgr)

	priv, feed1, node, err := s.setUp(map[string]float64{"BTC": 110998})
	c.Assert(err, IsNil)

	feed2, err := s.newQuoPriceFeed(priv, map[string]float64{"BTC": 999999})
	c.Assert(err, IsNil)

	feed3, err := s.newQuoPriceFeed(priv, map[string]float64{"ETH": 2769})
	c.Assert(err, IsNil)

	c.Assert(mgr.K.SetNodeAccount(ctx, node), IsNil)

	addr := GetRandomBech32Addr()
	msg1 := NewMsgPriceFeedQuorumBatch([]*common.QuorumPriceFeed{feed1}, addr)
	msg2 := NewMsgPriceFeedQuorumBatch([]*common.QuorumPriceFeed{feed2}, addr)
	msg3 := NewMsgPriceFeedQuorumBatch([]*common.QuorumPriceFeed{feed3}, addr)

	// first message will be processed
	_, err = handler.Run(ctx, msg1)
	c.Assert(err, IsNil)

	_, err = handler.Run(ctx, msg2)
	c.Assert(err, IsNil)

	_, err = handler.Run(ctx, msg3)
	c.Assert(err, IsNil)

	price, err := mgr.K.GetPrice(ctx, "BTC")
	c.Assert(err, IsNil)
	c.Assert(price.Price, Equals, "110998")

	price, err = mgr.K.GetPrice(ctx, "ETH")
	c.Assert(err, NotNil)
}

func (s *HandlerPriceFeedQuorumBatchSuite) TestInactiveValidator(c *C) {
	// only feeds from active validators are processed, feeds from other
	// nodes are dropped

	ctx, mgr := setupManagerForTest(c)
	handler := NewPriceFeedQuorumBatchHandler(mgr)

	_, feed1, node, err := s.setUp(map[string]float64{"BTC": 111149})
	c.Assert(err, IsNil)

	_, feed2, _, err := s.setUp(map[string]float64{"BTC": 110998})
	c.Assert(err, IsNil)

	c.Assert(handler.mgr.Keeper().SetNodeAccount(ctx, node), IsNil)

	quorumPriceFeeds := []*common.QuorumPriceFeed{feed1, feed2}

	msg := types.NewMsgPriceFeedQuorumBatch(quorumPriceFeeds, GetRandomBech32Addr())
	c.Assert(msg.QuoPriceFeeds, HasLen, 2)

	_, err = handler.Run(ctx, msg)
	c.Assert(err, IsNil)

	price, err := handler.mgr.Keeper().GetPrice(ctx, "BTC")
	c.Assert(err, IsNil)
	c.Assert(price.Price, Equals, "111149")
}

func (s *HandlerPriceFeedQuorumBatchSuite) TestDuplicateFeed(c *C) {
	// only one feed per node is processed

	ctx, mgr := setupManagerForTest(c)
	handler := NewPriceFeedQuorumBatchHandler(mgr)

	_, feed1, node1, err := s.setUp(map[string]float64{"BTC": 111149})
	c.Assert(err, IsNil)

	_, feed2, node2, err := s.setUp(map[string]float64{"BTC": 110998})
	c.Assert(err, IsNil)

	c.Assert(handler.mgr.Keeper().SetNodeAccount(ctx, node1), IsNil)
	c.Assert(handler.mgr.Keeper().SetNodeAccount(ctx, node2), IsNil)

	quorumPriceFeeds := []*common.QuorumPriceFeed{feed1, feed2, feed1, feed1}

	msg := types.NewMsgPriceFeedQuorumBatch(quorumPriceFeeds, GetRandomBech32Addr())

	_, err = handler.Run(ctx, msg)
	c.Assert(err, IsNil)

	price, err := handler.mgr.Keeper().GetPrice(ctx, "BTC")
	c.Assert(err, IsNil)
	// median of [111149, 110998], duplicate feeds are dropped
	c.Assert(price.Price, Equals, "111073.5")
}

func (s *HandlerPriceFeedQuorumBatchSuite) TestSharedFeedByBondAddress(c *C) {
	// only one feed per node is processed

	ctx, mgr := setupManagerForTest(c)
	handler := NewPriceFeedQuorumBatchHandler(mgr)

	_, feed1, node1, err := s.setUp(map[string]float64{"BTC": 111149, "ETH": 2769})
	c.Assert(err, IsNil)

	_, feed2, node2, err := s.setUp(map[string]float64{"BTC": 110998, "ETH": 2785})
	c.Assert(err, IsNil)

	_, feed3, node3, err := s.setUp(map[string]float64{"BTC": 110963, "ETH": 2777})
	c.Assert(err, IsNil)

	_, feed4, node4, err := s.setUp(map[string]float64{"ETH": 2790})
	c.Assert(err, IsNil)
	node4.BondAddress = node3.BondAddress

	_, _, node5, err := s.setUp(nil)
	c.Assert(err, IsNil)
	node5.BondAddress = node3.BondAddress

	_, _, node6, err := s.setUp(nil)
	c.Assert(err, IsNil)

	c.Assert(handler.mgr.Keeper().SetNodeAccount(ctx, node1), IsNil)
	c.Assert(handler.mgr.Keeper().SetNodeAccount(ctx, node2), IsNil)
	c.Assert(handler.mgr.Keeper().SetNodeAccount(ctx, node3), IsNil)
	c.Assert(handler.mgr.Keeper().SetNodeAccount(ctx, node4), IsNil)
	c.Assert(handler.mgr.Keeper().SetNodeAccount(ctx, node5), IsNil)
	c.Assert(handler.mgr.Keeper().SetNodeAccount(ctx, node6), IsNil)

	quorumPriceFeeds := []*common.QuorumPriceFeed{feed1, feed2, feed3, feed4}

	msg := types.NewMsgPriceFeedQuorumBatch(quorumPriceFeeds, GetRandomBech32Addr())

	_, err = handler.Run(ctx, msg)
	c.Assert(err, IsNil)

	price, err := handler.mgr.Keeper().GetPrice(ctx, "BTC")
	c.Assert(err, IsNil)
	// median of [111149, 110998, 110963, 110963, 110963]
	// nodes3-5 have the same BTC value
	c.Assert(price.Price, Equals, "110963")

	price, err = handler.mgr.Keeper().GetPrice(ctx, "ETH")
	c.Assert(err, IsNil)
	// median of [2769, 2785, 2777, 2790, 2790]
	// nodes5 has the same ETH value as node 4
	c.Assert(price.Price, Equals, "2785")
}

func (s *HandlerPriceFeedQuorumBatchSuite) TestInvalidFeed(c *C) {
	ctx, mgr := setupManagerForTest(c)
	handler := NewPriceFeedQuorumBatchHandler(mgr)

	priv, _, node, err := s.setUp(nil)
	c.Assert(err, IsNil)

	c.Assert(handler.mgr.Keeper().SetNodeAccount(ctx, node), IsNil)

	rates, err := s.getRates(map[string]float64{"BTC": 111149})
	c.Assert(err, IsNil)

	testCases := []struct {
		PriceFeed common.PriceFeed
		Fail      bool
	}{
		{
			// wrong version
			PriceFeed: common.PriceFeed{
				Version: []byte("foo"),
				Time:    time.Now().UnixMilli(),
				Rates:   rates,
			},
		},
		{
			// timestamp too old
			PriceFeed: common.PriceFeed{
				Version: s.version,
				Time:    0,
				Rates:   rates,
			},
			Fail: true,
		},
		{
			// timestamp negative
			PriceFeed: common.PriceFeed{
				Version: s.version,
				Time:    -123456,
				Rates:   rates,
			},
			Fail: true,
		},
		{
			// different amount of rates than required
			PriceFeed: common.PriceFeed{
				Version: s.version,
				Time:    time.Now().UnixMilli(),
				Rates:   []*common.OraclePrice{},
			},
			Fail: true,
		},
	}

	for _, tc := range testCases {
		attestation, err := s.attestPriceFeed(priv, &tc.PriceFeed)
		c.Assert(err, IsNil)

		msg := types.NewMsgPriceFeedQuorumBatch([]*common.QuorumPriceFeed{{
			PriceFeed:    &tc.PriceFeed,
			Attestations: []*common.Attestation{attestation},
		}}, GetRandomBech32Addr())

		_, err = handler.Run(ctx, msg)
		if tc.Fail {
			c.Assert(err, NotNil)
		} else {
			c.Assert(err, IsNil)
		}
	}

	// btc price hasn't been set
	_, err = handler.mgr.Keeper().GetPrice(ctx, "BTC")
	c.Assert(err, NotNil)

	feed, err := s.newQuoPriceFeed(priv, map[string]float64{"BTC": 111149})
	c.Assert(err, IsNil)

	// multiple attestations per feed
	feed.Attestations = append(feed.Attestations, feed.Attestations...)

	msg := types.NewMsgPriceFeedQuorumBatch(
		[]*common.QuorumPriceFeed{feed}, GetRandomBech32Addr(),
	)

	_, err = handler.Run(ctx, msg)
	c.Assert(err, NotNil)

	// no attestation
	msg.QuoPriceFeeds[0].Attestations = []*common.Attestation{}

	_, err = handler.Run(ctx, feed)
	c.Assert(err, NotNil)

	// proof that it works
	feed, err = s.newQuoPriceFeed(priv, map[string]float64{"BTC": 111111})
	c.Assert(err, IsNil)

	msg = types.NewMsgPriceFeedQuorumBatch(
		[]*common.QuorumPriceFeed{feed}, GetRandomBech32Addr(),
	)

	_, err = handler.Run(ctx, msg)
	c.Assert(err, IsNil)

	price, err := mgr.K.GetPrice(ctx, "BTC")
	c.Assert(err, IsNil)
	c.Assert(price.Price, Equals, "111111")
}

func (s *HandlerPriceFeedQuorumBatchSuite) TestWrongMessage(c *C) {
	ctx, mgr := setupManagerForTest(c)
	handler := NewPriceFeedQuorumBatchHandler(mgr)

	msg := NewMsgTCYStake(GetRandomTx(), GetRandomBech32Addr())

	_, err := handler.Run(ctx, msg)
	c.Assert(err, NotNil)

	// no price
	iterator := mgr.K.GetPriceIterator(ctx)
	c.Assert(iterator.Valid(), Equals, false)
}

//  helpers
// ----------------------------------------------------------------------------

func (s *HandlerPriceFeedQuorumBatchSuite) getRates(
	prices map[string]float64,
) ([]*common.OraclePrice, error) {
	rates := make([]*common.OraclePrice, len(s.symbols))

	for i, symbol := range s.symbols {
		value, found := prices[symbol]
		if !found {
			value = 0
		}

		price, err := common.NewOraclePrice(big.NewFloat(value))
		if err != nil {
			return nil, err
		}
		rates[i] = price
	}
	return rates, nil
}

func (s *HandlerPriceFeedQuorumBatchSuite) newQuoPriceFeed(
	privKey crypto.PrivKey,
	prices map[string]float64,
) (*common.QuorumPriceFeed, error) {
	rates, err := s.getRates(prices)
	if err != nil {
		return nil, err
	}

	priceFeed := &common.PriceFeed{
		Version: s.version,
		Time:    time.Now().UnixMilli(),
		Rates:   rates,
	}

	attestation, err := s.attestPriceFeed(privKey, priceFeed)
	if err != nil {
		return nil, err
	}

	return &common.QuorumPriceFeed{
		PriceFeed:    priceFeed,
		Attestations: []*common.Attestation{attestation},
	}, nil
}

func (s *HandlerPriceFeedQuorumBatchSuite) attestPriceFeed(
	privKey crypto.PrivKey,
	priceFeed *common.PriceFeed,
) (*common.Attestation, error) {
	data, err := priceFeed.GetSignablePayload()
	if err != nil {
		return nil, err
	}

	signature, err := privKey.Sign(data)
	if err != nil {
		return nil, err
	}

	return &common.Attestation{
		PubKey:    privKey.PubKey().Bytes(),
		Signature: signature,
	}, nil
}

func (s *HandlerPriceFeedQuorumBatchSuite) getNodeAccount(
	privKey crypto.PrivKey,
) (NodeAccount, error) {
	pubKey := privKey.PubKey()

	nodeAddress := cosmos.AccAddress(pubKey.Address())

	commonPubKey, err := common.NewPubKeyFromCrypto(pubKey)
	if err != nil {
		return NodeAccount{}, err
	}

	return NewNodeAccount(
		nodeAddress,
		NodeActive,
		common.PubKeySet{
			Secp256k1: commonPubKey,
			Ed25519:   commonPubKey,
		},
		GetRandomBech32ConsensusPubKey(),
		cosmos.NewUint(common.One*100_000),
		GetRandomTHORAddress(),
		1,
	), nil
}

func (s *HandlerPriceFeedQuorumBatchSuite) setUp(
	prices map[string]float64,
) (crypto.PrivKey, *common.QuorumPriceFeed, NodeAccount, error) {
	privKey := secp256k1.GenPrivKey()

	quoPriceFeed, err := s.newQuoPriceFeed(privKey, prices)
	if err != nil {
		return nil, nil, NodeAccount{}, err
	}

	nodeAccount, err := s.getNodeAccount(privKey)
	if err != nil {
		return nil, nil, NodeAccount{}, err
	}

	return privKey, quoPriceFeed, nodeAccount, nil
}
