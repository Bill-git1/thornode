package thorchain

import (
	"testing"

	. "gopkg.in/check.v1"
)

type QuotesSuite struct{}

var _ = Suite(&QuotesSuite{})

func TestQuotes(t *testing.T) { TestingT(t) }

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
