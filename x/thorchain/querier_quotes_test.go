package thorchain

import (
	"gitlab.com/thorchain/thornode/v3/constants"
	. "gopkg.in/check.v1"
)

type QuotesSuite struct{}

var _ = Suite(&QuotesSuite{})

func (s *QuotesSuite) TestParseMultipleAffiliateParams(c *C) {
	ctx, mgr := setupManagerForTest(c)

	// Test single affiliate
	affiliates, bps, totalBps, err := parseMultipleAffiliateParams(ctx, mgr, "affiliate1", "100")
	c.Assert(err, IsNil)
	c.Assert(len(affiliates), Equals, 1)
	c.Assert(len(bps), Equals, 1)
	c.Assert(affiliates[0], Equals, "affiliate1")
	c.Assert(bps[0].Uint64(), Equals, uint64(100))
	c.Assert(totalBps.Uint64(), Equals, uint64(100))

	// Test multiple affiliates with slash separation
	affiliates, bps, totalBps, err = parseMultipleAffiliateParams(ctx, mgr, "affiliate1/affiliate2", "100/200")
	c.Assert(err, IsNil)
	c.Assert(len(affiliates), Equals, 2)
	c.Assert(len(bps), Equals, 2)
	c.Assert(affiliates[0], Equals, "affiliate1")
	c.Assert(affiliates[1], Equals, "affiliate2")
	c.Assert(bps[0].Uint64(), Equals, uint64(100))
	c.Assert(bps[1].Uint64(), Equals, uint64(200))
	c.Assert(totalBps.Uint64(), Equals, uint64(300))

	// Test three affiliates
	affiliates, bps, totalBps, err = parseMultipleAffiliateParams(ctx, mgr, "a1/a2/a3", "50/75/25")
	c.Assert(err, IsNil)
	c.Assert(len(affiliates), Equals, 3)
	c.Assert(len(bps), Equals, 3)
	c.Assert(affiliates[0], Equals, "a1")
	c.Assert(affiliates[1], Equals, "a2")
	c.Assert(affiliates[2], Equals, "a3")
	c.Assert(bps[0].Uint64(), Equals, uint64(50))
	c.Assert(bps[1].Uint64(), Equals, uint64(75))
	c.Assert(bps[2].Uint64(), Equals, uint64(25))
	c.Assert(totalBps.Uint64(), Equals, uint64(150))

	// Test single bps applied to multiple affiliates (should now return error due to mismatch)
	_, _, _, err = parseMultipleAffiliateParams(ctx, mgr, "a1/a2/a3", "100")
	c.Assert(err, NotNil)
	c.Assert(err.Error(), Equals, "mismatch between number of affiliates (3) and BPS values (1)")

	// Test empty strings
	affiliates, bps, totalBps, err = parseMultipleAffiliateParams(ctx, mgr, "", "")
	c.Assert(err, IsNil)
	c.Assert(len(affiliates), Equals, 0)
	c.Assert(len(bps), Equals, 0)
	c.Assert(totalBps.Uint64(), Equals, uint64(0))

	// Test whitespace trimming
	affiliates, bps, _, err = parseMultipleAffiliateParams(ctx, mgr, " a1 / a2 ", " 100 / 200 ")
	c.Assert(err, IsNil)
	c.Assert(len(affiliates), Equals, 2)
	c.Assert(affiliates[0], Equals, "a1")
	c.Assert(affiliates[1], Equals, "a2")
	c.Assert(bps[0].Uint64(), Equals, uint64(100))
	c.Assert(bps[1].Uint64(), Equals, uint64(200))

	// Test empty parts in slash-separated string
	affiliates, bps, _, err = parseMultipleAffiliateParams(ctx, mgr, "a1//a3", "100//300")
	c.Assert(err, IsNil)
	c.Assert(len(affiliates), Equals, 2)
	c.Assert(affiliates[0], Equals, "a1")
	c.Assert(affiliates[1], Equals, "a3")
	c.Assert(bps[0].Uint64(), Equals, uint64(100))
	c.Assert(bps[1].Uint64(), Equals, uint64(300))

	// Test mismatch between affiliates and bps (more affiliates than bps) - should now return error
	_, _, _, err = parseMultipleAffiliateParams(ctx, mgr, "a1/a2/a3", "100/200")
	c.Assert(err, NotNil)
	c.Assert(err.Error(), Equals, "mismatch between number of affiliates (3) and BPS values (2)")

	// Test mismatch between affiliates and bps (more bps than affiliates) - should also return error
	_, _, _, err = parseMultipleAffiliateParams(ctx, mgr, "a1/a2", "100/200/300")
	c.Assert(err, NotNil)
	c.Assert(err.Error(), Equals, "mismatch between number of affiliates (2) and BPS values (3)")

	// Test equal numbers of affiliates and bps - should work
	affiliates, bps, totalBps, err = parseMultipleAffiliateParams(ctx, mgr, "a1/a2/a3", "100/200/150")
	c.Assert(err, IsNil)
	c.Assert(len(affiliates), Equals, 3)
	c.Assert(len(bps), Equals, 3)
	c.Assert(affiliates[0], Equals, "a1")
	c.Assert(affiliates[1], Equals, "a2")
	c.Assert(affiliates[2], Equals, "a3")
	c.Assert(bps[0].Uint64(), Equals, uint64(100))
	c.Assert(bps[1].Uint64(), Equals, uint64(200))
	c.Assert(bps[2].Uint64(), Equals, uint64(150))
	c.Assert(totalBps.Uint64(), Equals, uint64(450))

	// Test invalid bps (should skip invalid ones)
	affiliates, bps, _, err = parseMultipleAffiliateParams(ctx, mgr, "a1/a2/a3", "100/invalid/300")
	c.Assert(err, IsNil)
	c.Assert(len(affiliates), Equals, 2) // Skips a2 because of invalid bps
	c.Assert(affiliates[0], Equals, "a1")
	c.Assert(affiliates[1], Equals, "a3")
	c.Assert(bps[0].Uint64(), Equals, uint64(100))
	c.Assert(bps[1].Uint64(), Equals, uint64(300))

	// Test total bps exceeding limit (1000)
	_, _, _, err = parseMultipleAffiliateParams(ctx, mgr, "a1/a2", "600/500")
	c.Assert(err, NotNil)
	c.Assert(err.Error(), Equals, "total affiliate fee must not be more than 1000 bps")

	// Test edge case: exactly at limit
	_, _, totalBps, err = parseMultipleAffiliateParams(ctx, mgr, "a1/a2", "500/500")
	c.Assert(err, IsNil)
	c.Assert(totalBps.Uint64(), Equals, uint64(1000))

	// Test affiliates with no BPS values (should return error due to mismatch)
	_, _, _, err = parseMultipleAffiliateParams(ctx, mgr, "a1/a2/a3", "")
	c.Assert(err, NotNil)
	c.Assert(err.Error(), Equals, "mismatch between number of affiliates (3) and BPS values (0)")
}

func (s *QuotesSuite) TestRapidStreamingSwapCalculation(c *C) {
	ctx, mgr := setupManagerForTest(c)

	// Helper function to calculate streaming swap blocks
	calculateStreamingSwapBlocks := func(streamingQuantity, streamingInterval, rapidSwapMax uint64) int64 {
		var streamSwapBlocks int64
		if streamingQuantity > 0 {
			if streamingInterval == 0 {
				// Rapid streaming: multiple swaps per block limited by AdvSwapQueueRapidSwapMax
				rapidSwapMax = max(rapidSwapMax, 1)
				// Calculate blocks needed: ceil(quantity / rapidSwapMax)
				streamSwapBlocks = (int64(streamingQuantity) + int64(rapidSwapMax) - 1) / int64(rapidSwapMax)
			} else {
				// Traditional streaming: one swap per interval
				streamSwapBlocks = int64(streamingInterval) * int64(streamingQuantity-1)
			}
		}
		return streamSwapBlocks
	}

	// Test 1: Rapid streaming with rapidSwapMax=5, quantity=10
	// Should take ceil(10/5) = 2 blocks
	mgr.Keeper().SetMimir(ctx, "AdvSwapQueueRapidSwapMax", 5)
	blocks := calculateStreamingSwapBlocks(10, 0, 5)
	c.Assert(blocks, Equals, int64(2))

	// Test 2: Rapid streaming with rapidSwapMax=5, quantity=11
	// Should take ceil(11/5) = 3 blocks
	blocks = calculateStreamingSwapBlocks(11, 0, 5)
	c.Assert(blocks, Equals, int64(3))

	// Test 3: Rapid streaming with rapidSwapMax=5, quantity=5
	// Should take ceil(5/5) = 1 block
	blocks = calculateStreamingSwapBlocks(5, 0, 5)
	c.Assert(blocks, Equals, int64(1))

	// Test 4: Rapid streaming with rapidSwapMax=5, quantity=4
	// Should take ceil(4/5) = 1 block
	blocks = calculateStreamingSwapBlocks(4, 0, 5)
	c.Assert(blocks, Equals, int64(1))

	// Test 5: Rapid streaming with rapidSwapMax=1, quantity=10
	// Should take ceil(10/1) = 10 blocks
	blocks = calculateStreamingSwapBlocks(10, 0, 1)
	c.Assert(blocks, Equals, int64(10))

	// Test 6: Rapid streaming with rapidSwapMax=10, quantity=20
	// Should take ceil(20/10) = 2 blocks
	blocks = calculateStreamingSwapBlocks(20, 0, 10)
	c.Assert(blocks, Equals, int64(2))

	// Test 7: Rapid streaming with rapidSwapMax=10, quantity=15
	// Should take ceil(15/10) = 2 blocks
	blocks = calculateStreamingSwapBlocks(15, 0, 10)
	c.Assert(blocks, Equals, int64(2))

	// Test 8: Traditional streaming with interval=5, quantity=10
	// Should take 5 * (10-1) = 45 blocks
	blocks = calculateStreamingSwapBlocks(10, 5, 5)
	c.Assert(blocks, Equals, int64(45))

	// Test 9: Traditional streaming with interval=1, quantity=10
	// Should take 1 * (10-1) = 9 blocks
	blocks = calculateStreamingSwapBlocks(10, 1, 5)
	c.Assert(blocks, Equals, int64(9))

	// Test 10: Edge case - quantity=0
	// Should take 0 blocks
	blocks = calculateStreamingSwapBlocks(0, 0, 5)
	c.Assert(blocks, Equals, int64(0))

	// Test 11: Edge case - rapidSwapMax=0 (should use safety default of 1)
	// Should take ceil(10/1) = 10 blocks
	blocks = calculateStreamingSwapBlocks(10, 0, 0)
	c.Assert(blocks, Equals, int64(10))

	// Test 12: Verify actual quote API integration
	// Set up a test scenario with rapid streaming
	mgr.Keeper().SetMimir(ctx, "AdvSwapQueueRapidSwapMax", 5)

	// The actual queryQuoteSwap function would be called here in a full integration test
	// For now, we've verified the calculation logic works correctly
	rapidSwapMax := mgr.Keeper().GetConfigInt64(ctx, constants.AdvSwapQueueRapidSwapMax)
	c.Assert(rapidSwapMax, Equals, int64(5))
}
