package constants

import (
	"regexp"
	"time"

	"github.com/blang/semver"
	. "gopkg.in/check.v1"
)

type ConstantsTestSuite struct{}

var _ = Suite(&ConstantsTestSuite{})

func (ConstantsTestSuite) TestConstantName_String(c *C) {
	constantNames := []ConstantName{
		EmissionCurve,
		BlocksPerYear,
		OutboundTransactionFee,
		PoolCycle,
		MinimumNodesForBFT,
		DesiredValidatorSet,
		ChurnInterval,
		LackOfObservationPenalty,
		SigningTransactionPeriod,
		DoubleSignMaxAge,
		MinimumBondInRune,
		ValidatorMaxRewardRatio,
	}
	for _, item := range constantNames {
		c.Assert(item.String(), Not(Equals), "NA")
	}
}

func (ConstantsTestSuite) TestGetConstantValues(c *C) {
	ver := semver.MustParse("0.0.9")
	c.Assert(GetConstantValues(ver), NotNil)
	c.Assert(GetConstantValues(SWVersion), NotNil)
}

func (ConstantsTestSuite) TestAllConstantName(c *C) {
	keyRegex := regexp.MustCompile(MimirKeyRegex).MatchString
	for i := 0; i < len(_ConstantName_index)-1; i++ {
		key := ConstantName(i)
		if !keyRegex(key.String()) {
			c.Errorf("key:%s can't be used to set mimir", key)
		}
	}
}

func (ConstantsTestSuite) TestBlockTimeConstants(c *C) {
	// ThorchainBlockTime
	c.Assert(ThorchainBlockTime, Equals, 2*time.Second)
	consts := BlockTimeConstants()
	c.Assert(consts[BlocksPerYear], Equals, int64(15768000))
	c.Assert(consts[PoolCycle], Equals, int64(129600))                     // Make a pool available every 3 days
	c.Assert(consts[PendingLiquidityAgeLimit], Equals, int64(302400))      // age pending liquidity can be pending before its auto committed to the pool
	c.Assert(consts[MaxAnchorBlocks], Equals, int64(900))                  // max blocks to accumulate swap slips in anchor pools
	c.Assert(consts[DynamicMaxAnchorSlipBlocks], Equals, int64(604800))    // number of blocks to sample in calculating the dynamic max anchor slip
	c.Assert(consts[DynamicMaxAnchorCalcInterval], Equals, int64(43200))   // number of blocks to recalculate the dynamic max anchor
	c.Assert(consts[FundMigrationInterval], Equals, int64(2160))           // number of blocks THORNode will attempt to move funds from a retiring vault to an active one
	c.Assert(consts[ChurnInterval], Equals, int64(129600))                 // How many blocks THORNode try to rotate validators
	c.Assert(consts[ChurnRetryInterval], Equals, int64(2160))              // How many blocks until we retry a churn (only if we haven't had a successful churn in ChurnInterval blocks
	c.Assert(consts[SigningTransactionPeriod], Equals, int64(900))         // how many blocks before a request to sign a tx by yggdrasil pool, is counted as delinquent.
	c.Assert(consts[DoubleSignMaxAge], Equals, int64(72))                  // number of blocks to limit double signing a block
	c.Assert(consts[FailKeygenSlashPoints], Equals, int64(2160))           // slash for 2160 blocks, which equals 1 hour
	c.Assert(consts[ObservationDelayFlexibility], Equals, int64(60))       // number of blocks of flexibility for a validator to get their slash points taken off for making an observation
	c.Assert(consts[JailTimeKeygen], Equals, int64(12960))                 // blocks a node account is jailed for failing to keygen. DO NOT drop below tss timeout
	c.Assert(consts[JailTimeKeysign], Equals, int64(180))                  // blocks a node account is jailed for failing to keysign. DO NOT drop below tss timeout
	c.Assert(consts[NodePauseChainBlocks], Equals, int64(2160))            // number of blocks that a node can pause/resume a global chain halt
	c.Assert(consts[StreamingSwapMaxLength], Equals, int64(43200))         // max number of blocks a streaming swap can trade for
	c.Assert(consts[StreamingSwapMaxLengthNative], Equals, int64(43200))   // max number of blocks native streaming swaps can trade over
	c.Assert(consts[MinTxOutVolumeThreshold], Equals, int64(333333333334)) // // total txout volume (in rune) a block needs to have to slow outbound transactions
	c.Assert(consts[TxOutDelayMax], Equals, int64(51840))                  // max number of blocks a transaction can be delayed
	c.Assert(consts[TxOutDelayRate], Equals, int64(16666666667))           // outbound rune per block rate for scheduled transactions (excluding native assets)
	c.Assert(consts[MaxTxOutOffset], Equals, int64(1350))                  // max blocks to offset a txout into a future block
	c.Assert(consts[TNSFeePerBlock], Equals, int64(7))
	c.Assert(consts[TNSFeePerBlockUSD], Equals, int64(7))
	c.Assert(consts[ChurnOutForLowVersionBlocks], Equals, int64(64800))     // the blocks after the MinJoinVersion changes before nodes can be churned out for low version
	c.Assert(consts[CloutReset], Equals, int64(2160))                       // number of blocks before clout spent gets reset
	c.Assert(consts[RUNEPoolDepositMaturityBlocks], Equals, int64(1296000)) // blocks from last deposit to allow withdraw
}
