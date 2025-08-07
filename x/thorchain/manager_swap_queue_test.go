package thorchain

import (
	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/common"
)

type ManagerSwapQueueSuite struct{}

var _ = Suite(&ManagerSwapQueueSuite{})

// TestGenTradePair tests the genTradePair helper function
func (s *ManagerSwapQueueSuite) TestGenTradePair(c *C) {
	// Test creating a trade pair
	source := common.BTCAsset
	target := common.ETHAsset

	pair := genTradePair(source, target)

	c.Assert(pair.source.Equals(source), Equals, true)
	c.Assert(pair.target.Equals(target), Equals, true)
}

// TestTradePairString tests the String method of tradePair
func (s *ManagerSwapQueueSuite) TestTradePairString(c *C) {
	// Test various trade pair string representations
	testCases := []struct {
		source   common.Asset
		target   common.Asset
		expected string
	}{
		{common.BTCAsset, common.ETHAsset, "BTC.BTC>ETH.ETH"},
		{common.RuneAsset(), common.BTCAsset, "THOR.RUNE>BTC.BTC"},
		{common.ETHAsset, common.RuneAsset(), "ETH.ETH>THOR.RUNE"},
		{common.BTCAsset.GetSyntheticAsset(), common.ETHAsset, "BTC/BTC>ETH.ETH"},
	}

	for _, tc := range testCases {
		pair := genTradePair(tc.source, tc.target)
		c.Assert(pair.String(), Equals, tc.expected)
	}
}

// TestTradePairHasRune tests the HasRune method of tradePair
func (s *ManagerSwapQueueSuite) TestTradePairHasRune(c *C) {
	// Test pairs with RUNE
	runeToEth := genTradePair(common.RuneAsset(), common.ETHAsset)
	c.Assert(runeToEth.HasRune(), Equals, true)

	ethToRune := genTradePair(common.ETHAsset, common.RuneAsset())
	c.Assert(ethToRune.HasRune(), Equals, true)

	// Test pairs without RUNE
	btcToEth := genTradePair(common.BTCAsset, common.ETHAsset)
	c.Assert(btcToEth.HasRune(), Equals, false)

	// Test with synthetic assets (should not be considered RUNE)
	synthBtcToEth := genTradePair(common.BTCAsset.GetSyntheticAsset(), common.ETHAsset)
	c.Assert(synthBtcToEth.HasRune(), Equals, false)
}

// TestTradePairEquals tests the Equals method of tradePair
func (s *ManagerSwapQueueSuite) TestTradePairEquals(c *C) {
	// Test equal pairs
	pair1 := genTradePair(common.BTCAsset, common.ETHAsset)
	pair2 := genTradePair(common.BTCAsset, common.ETHAsset)
	c.Assert(pair1.Equals(pair2), Equals, true)

	// Test unequal pairs - different source
	pair3 := genTradePair(common.RuneAsset(), common.ETHAsset)
	c.Assert(pair1.Equals(pair3), Equals, false)

	// Test unequal pairs - different target
	pair4 := genTradePair(common.BTCAsset, common.RuneAsset())
	c.Assert(pair1.Equals(pair4), Equals, false)

	// Test unequal pairs - both different
	pair5 := genTradePair(common.RuneAsset(), common.RuneAsset())
	c.Assert(pair1.Equals(pair5), Equals, false)

	// Test with synthetic assets
	pair6 := genTradePair(common.BTCAsset.GetSyntheticAsset(), common.ETHAsset)
	pair7 := genTradePair(common.BTCAsset.GetSyntheticAsset(), common.ETHAsset)
	c.Assert(pair6.Equals(pair7), Equals, true)
	c.Assert(pair1.Equals(pair6), Equals, false) // BTC != synth BTC
}

// TestAppendNewPair tests appending a new pair to an empty list
func (s *ManagerSwapQueueSuite) TestAppendNewPair(c *C) {
	// Start with empty tradePairs
	pairs := tradePairs{}
	c.Assert(len(pairs), Equals, 0)

	// Append a new pair
	newPair := genTradePair(common.BTCAsset, common.ETHAsset)
	pairs = pairs.Append(newPair)

	// Verify the pair was added
	c.Assert(len(pairs), Equals, 1)
	c.Assert(pairs[0].Equals(newPair), Equals, true)
}

// TestAppendDuplicatePair tests that duplicate pairs are not added
func (s *ManagerSwapQueueSuite) TestAppendDuplicatePair(c *C) {
	// Create tradePairs with one pair
	pair := genTradePair(common.BTCAsset, common.ETHAsset)
	pairs := tradePairs{pair}
	c.Assert(len(pairs), Equals, 1)

	// Try to append the same pair
	pairs = pairs.Append(pair)

	// Verify list length remains 1 (no duplicate added)
	c.Assert(len(pairs), Equals, 1)
	c.Assert(pairs[0].Equals(pair), Equals, true)
}

// TestAppendMultiplePairs tests appending multiple different pairs
func (s *ManagerSwapQueueSuite) TestAppendMultiplePairs(c *C) {
	// Start with empty tradePairs
	pairs := tradePairs{}

	// Define test pairs
	pair1 := genTradePair(common.BTCAsset, common.ETHAsset)
	pair2 := genTradePair(common.ETHAsset, common.RuneAsset())
	pair3 := genTradePair(common.RuneAsset(), common.BTCAsset)
	pair4 := genTradePair(common.BTCAsset.GetSyntheticAsset(), common.ETHAsset)

	// Append all pairs
	pairs = pairs.Append(pair1)
	pairs = pairs.Append(pair2)
	pairs = pairs.Append(pair3)
	pairs = pairs.Append(pair4)

	// Verify all pairs were added
	c.Assert(len(pairs), Equals, 4)
	c.Assert(pairs[0].Equals(pair1), Equals, true)
	c.Assert(pairs[1].Equals(pair2), Equals, true)
	c.Assert(pairs[2].Equals(pair3), Equals, true)
	c.Assert(pairs[3].Equals(pair4), Equals, true)
}

// TestAppendWithExistingPairs tests appending to a non-empty list with duplicates
func (s *ManagerSwapQueueSuite) TestAppendWithExistingPairs(c *C) {
	// Start with some existing pairs
	pair1 := genTradePair(common.BTCAsset, common.ETHAsset)
	pair2 := genTradePair(common.ETHAsset, common.RuneAsset())
	pairs := tradePairs{pair1, pair2}
	c.Assert(len(pairs), Equals, 2)

	// Try to append duplicate of pair1
	pairs = pairs.Append(pair1)
	c.Assert(len(pairs), Equals, 2) // Should remain 2

	// Append a new pair
	pair3 := genTradePair(common.RuneAsset(), common.BTCAsset)
	pairs = pairs.Append(pair3)
	c.Assert(len(pairs), Equals, 3)

	// Try to append duplicate of pair2
	pairs = pairs.Append(pair2)
	c.Assert(len(pairs), Equals, 3) // Should remain 3

	// Verify the final state
	c.Assert(pairs[0].Equals(pair1), Equals, true)
	c.Assert(pairs[1].Equals(pair2), Equals, true)
	c.Assert(pairs[2].Equals(pair3), Equals, true)
}

// TestAppendEdgeCases tests various edge cases
func (s *ManagerSwapQueueSuite) TestAppendEdgeCases(c *C) {
	pairs := tradePairs{}

	// Test with RUNE pairs
	runeToBtc := genTradePair(common.RuneAsset(), common.BTCAsset)
	btcToRune := genTradePair(common.BTCAsset, common.RuneAsset())
	pairs = pairs.Append(runeToBtc)
	pairs = pairs.Append(btcToRune)
	c.Assert(len(pairs), Equals, 2)

	// Test with synthetic assets
	synthBtcToEth := genTradePair(common.BTCAsset.GetSyntheticAsset(), common.ETHAsset)
	ethToSynthBtc := genTradePair(common.ETHAsset, common.BTCAsset.GetSyntheticAsset())
	pairs = pairs.Append(synthBtcToEth)
	pairs = pairs.Append(ethToSynthBtc)
	c.Assert(len(pairs), Equals, 4)

	// Test pairs with same source but different targets
	btcToEth := genTradePair(common.BTCAsset, common.ETHAsset)
	btcToAtom := genTradePair(common.BTCAsset, common.ATOMAsset)
	pairs = pairs.Append(btcToEth)
	pairs = pairs.Append(btcToAtom)
	c.Assert(len(pairs), Equals, 6)

	// Test pairs with same target but different sources
	ethToBtc := genTradePair(common.ETHAsset, common.BTCAsset)
	atomToBtc := genTradePair(common.ATOMAsset, common.BTCAsset)
	pairs = pairs.Append(ethToBtc)
	pairs = pairs.Append(atomToBtc)
	c.Assert(len(pairs), Equals, 8)
}

// TestAppendPreservesOrder tests that append maintains order
func (s *ManagerSwapQueueSuite) TestAppendPreservesOrder(c *C) {
	pairs := tradePairs{}

	// Append pairs in specific order
	pair1 := genTradePair(common.BTCAsset, common.ETHAsset)
	pair2 := genTradePair(common.ETHAsset, common.ATOMAsset)
	pair3 := genTradePair(common.ATOMAsset, common.RuneAsset())
	pair4 := genTradePair(common.RuneAsset(), common.BTCAsset)

	pairs = pairs.Append(pair1)
	pairs = pairs.Append(pair2)
	pairs = pairs.Append(pair3)
	pairs = pairs.Append(pair4)

	// Verify order is maintained
	c.Assert(len(pairs), Equals, 4)
	c.Assert(pairs[0].Equals(pair1), Equals, true)
	c.Assert(pairs[1].Equals(pair2), Equals, true)
	c.Assert(pairs[2].Equals(pair3), Equals, true)
	c.Assert(pairs[3].Equals(pair4), Equals, true)

	// Try to append duplicates in different order
	pairs = pairs.Append(pair3)
	pairs = pairs.Append(pair1)

	// Order should remain the same, no new items added
	c.Assert(len(pairs), Equals, 4)
	c.Assert(pairs[0].Equals(pair1), Equals, true)
	c.Assert(pairs[1].Equals(pair2), Equals, true)
	c.Assert(pairs[2].Equals(pair3), Equals, true)
	c.Assert(pairs[3].Equals(pair4), Equals, true)
}

// TestAppendWithEmptyAssets tests behavior with empty assets
func (s *ManagerSwapQueueSuite) TestAppendWithEmptyAssets(c *C) {
	pairs := tradePairs{}

	// Create pairs with empty assets
	emptyAsset := common.Asset{}

	// Empty source, valid target
	pair1 := genTradePair(emptyAsset, common.BTCAsset)
	pairs = pairs.Append(pair1)
	c.Assert(len(pairs), Equals, 1)

	// Valid source, empty target
	pair2 := genTradePair(common.ETHAsset, emptyAsset)
	pairs = pairs.Append(pair2)
	c.Assert(len(pairs), Equals, 2)

	// Both empty
	pair3 := genTradePair(emptyAsset, emptyAsset)
	pairs = pairs.Append(pair3)
	c.Assert(len(pairs), Equals, 3)

	// Try to add duplicate empty pair
	pairs = pairs.Append(pair3)
	c.Assert(len(pairs), Equals, 3) // Should remain 3

	// Verify the pairs
	c.Assert(pairs[0].source.IsEmpty(), Equals, true)
	c.Assert(pairs[0].target.Equals(common.BTCAsset), Equals, true)
	c.Assert(pairs[1].source.Equals(common.ETHAsset), Equals, true)
	c.Assert(pairs[1].target.IsEmpty(), Equals, true)
	c.Assert(pairs[2].source.IsEmpty(), Equals, true)
	c.Assert(pairs[2].target.IsEmpty(), Equals, true)
}

// TestAppendConcurrentScenario simulates a scenario where multiple pairs might be added
func (s *ManagerSwapQueueSuite) TestAppendConcurrentScenario(c *C) {
	// This tests that Append returns a new slice rather than modifying in place
	originalPairs := tradePairs{
		genTradePair(common.BTCAsset, common.ETHAsset),
		genTradePair(common.ETHAsset, common.RuneAsset()),
	}

	// Make a copy of the original length
	originalLen := len(originalPairs)

	// Append to create a new slice
	newPairs := originalPairs.Append(genTradePair(common.RuneAsset(), common.BTCAsset))

	// Verify original is unchanged
	c.Assert(len(originalPairs), Equals, originalLen)
	c.Assert(len(newPairs), Equals, originalLen+1)

	// Verify the new pair is only in newPairs
	c.Assert(newPairs[2].source.Equals(common.RuneAsset()), Equals, true)
	c.Assert(newPairs[2].target.Equals(common.BTCAsset), Equals, true)
}
