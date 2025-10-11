package thorchain

import (
	"testing"

	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
)

type HandlerObservedTxHelpersSuite struct{}

var _ = Suite(&HandlerObservedTxHelpersSuite{})

func TestHandlerObservedTxHelpersSuite(t *testing.T) {
	TestingT(t)
}

func (s *HandlerObservedTxHelpersSuite) TestGenerateReferenceMemoID(c *C) {
	ctx, mgr := setupManagerForTest(c)

	// Test with 8-decimal asset (BTC)
	btcPool := NewPool()
	btcPool.Asset = common.BTCAsset
	btcPool.Decimals = 8
	btcPool.BalanceAsset = cosmos.NewUint(100 * common.One)
	btcPool.BalanceRune = cosmos.NewUint(100 * common.One)
	c.Assert(mgr.Keeper().SetPool(ctx, btcPool), IsNil)

	// Create observed tx with specific amount
	tx := GetRandomObservedTx()
	tx.Tx.Coins = common.NewCoins(common.NewCoin(common.BTCAsset, cosmos.NewUint(123456789)))

	refID, err := generateReferenceMemoID(ctx, mgr, common.BTCAsset, tx)
	c.Assert(err, IsNil)
	c.Assert(refID, Equals, "56789") // last 5 digits of 123456789

	// Test with larger amount
	tx.Tx.Coins = common.NewCoins(common.NewCoin(common.BTCAsset, cosmos.NewUint(987654321)))
	refID, err = generateReferenceMemoID(ctx, mgr, common.BTCAsset, tx)
	c.Assert(err, IsNil)
	c.Assert(refID, Equals, "54321") // last 5 digits of 987654321

	// Test with amount less than 5 digits
	tx.Tx.Coins = common.NewCoins(common.NewCoin(common.BTCAsset, cosmos.NewUint(123)))
	refID, err = generateReferenceMemoID(ctx, mgr, common.BTCAsset, tx)
	c.Assert(err, IsNil)
	c.Assert(refID, Equals, "00123") // padded to 5 digits
}

func (s *HandlerObservedTxHelpersSuite) TestGenerateReferenceMemoIDWithDifferentDecimals(c *C) {
	ctx, mgr := setupManagerForTest(c)

	// Test with 6-decimal asset (simulate GAIA)
	gaiaAsset := common.Asset{Chain: common.GAIAChain, Symbol: "ATOM", Ticker: "ATOM", Synth: false}
	gaiaPool := NewPool()
	gaiaPool.Asset = gaiaAsset
	gaiaPool.Decimals = 6 // GAIA has 6 decimals
	gaiaPool.BalanceAsset = cosmos.NewUint(100 * common.One)
	gaiaPool.BalanceRune = cosmos.NewUint(100 * common.One)
	c.Assert(mgr.Keeper().SetPool(ctx, gaiaPool), IsNil)

	// For 6 decimals, amount should be divided by 100 (10^(8-6))
	tx := GetRandomObservedTx()
	tx.Tx.Coins = common.NewCoins(common.NewCoin(gaiaAsset, cosmos.NewUint(123456780000))) // 1234.56780000 in 6-decimal format

	refID, err := generateReferenceMemoID(ctx, mgr, gaiaAsset, tx)
	c.Assert(err, IsNil)
	c.Assert(refID, Equals, "67800") // 123456780000 / 100 = 1234567800, last 5 digits = 67800

	// Test with 7-decimal asset
	customAsset := common.Asset{Chain: common.ETHChain, Symbol: "CUSTOM", Ticker: "CUSTOM", Synth: false}
	customPool := NewPool()
	customPool.Asset = customAsset
	customPool.Decimals = 7
	customPool.BalanceAsset = cosmos.NewUint(100 * common.One)
	customPool.BalanceRune = cosmos.NewUint(100 * common.One)
	c.Assert(mgr.Keeper().SetPool(ctx, customPool), IsNil)

	// For 7 decimals, amount should be divided by 10 (10^(8-7))
	tx.Tx.Coins = common.NewCoins(common.NewCoin(customAsset, cosmos.NewUint(123456780000)))
	refID, err = generateReferenceMemoID(ctx, mgr, customAsset, tx)
	c.Assert(err, IsNil)
	c.Assert(refID, Equals, "78000") // 123456780000 / 10 = 12345678000, last 5 digits = 78000
}

func (s *HandlerObservedTxHelpersSuite) TestGenerateReferenceMemoIDErrorCases(c *C) {
	ctx, mgr := setupManagerForTest(c)

	// Test with empty coins
	emptyTx := GetRandomObservedTx()
	emptyTx.Tx.Coins = common.NewCoins()
	_, err := generateReferenceMemoID(ctx, mgr, common.BTCAsset, emptyTx)
	c.Assert(err, NotNil)
	c.Assert(err.Error(), Matches, ".*no coins.*")

	// Test with zero amount
	zeroTx := GetRandomObservedTx()
	zeroTx.Tx.Coins = common.NewCoins(common.NewCoin(common.BTCAsset, cosmos.ZeroUint()))
	_, err = generateReferenceMemoID(ctx, mgr, common.BTCAsset, zeroTx)
	c.Assert(err, NotNil)
	c.Assert(err.Error(), Matches, ".*zero amount.*")

	// Test with empty asset
	normalTx := GetRandomObservedTx()
	normalTx.Tx.Coins = common.NewCoins(common.NewCoin(common.BTCAsset, cosmos.NewUint(123456789)))
	_, err = generateReferenceMemoID(ctx, mgr, common.EmptyAsset, normalTx)
	c.Assert(err, NotNil)
	c.Assert(err.Error(), Matches, ".*asset is empty.*")
}

func (s *HandlerObservedTxHelpersSuite) TestGenerateReferenceMemoIDDecimalPrecision(c *C) {
	ctx, mgr := setupManagerForTest(c)

	// Test various decimal combinations
	testCases := []struct {
		decimals int64
		amount   uint64
		expected string
		desc     string
	}{
		{8, 123456789, "56789", "8 decimals, no adjustment"},
		{6, 123456780000, "67800", "6 decimals, divide by 100"},
		{7, 123456780000, "78000", "7 decimals, divide by 10"},
		{5, 123456780000, "56780", "5 decimals, divide by 1000"},
		{4, 123456780000, "45678", "4 decimals, divide by 10000"},
	}

	for i, tc := range testCases {
		// Create custom asset for each test case
		asset := common.Asset{
			Chain:  common.ETHChain,
			Symbol: common.Symbol("TEST" + string(rune('A'+i))),
			Ticker: common.Ticker("TEST" + string(rune('A'+i))),
			Synth:  false,
		}

		pool := NewPool()
		pool.Asset = asset
		pool.Decimals = tc.decimals
		pool.BalanceAsset = cosmos.NewUint(100 * common.One)
		pool.BalanceRune = cosmos.NewUint(100 * common.One)
		c.Assert(mgr.Keeper().SetPool(ctx, pool), IsNil)

		tx := GetRandomObservedTx()
		tx.Tx.Coins = common.NewCoins(common.NewCoin(asset, cosmos.NewUint(tc.amount)))

		refID, err := generateReferenceMemoID(ctx, mgr, asset, tx)
		c.Assert(err, IsNil, Commentf("Test case: %s", tc.desc))
		c.Assert(refID, Equals, tc.expected, Commentf("Test case: %s", tc.desc))
	}
}

func (s *HandlerObservedTxHelpersSuite) TestGenerateReferenceMemoIDModulus(c *C) {
	ctx, mgr := setupManagerForTest(c)

	// Setup BTC pool
	btcPool := NewPool()
	btcPool.Asset = common.BTCAsset
	btcPool.Decimals = 8
	btcPool.BalanceAsset = cosmos.NewUint(100 * common.One)
	btcPool.BalanceRune = cosmos.NewUint(100 * common.One)
	c.Assert(mgr.Keeper().SetPool(ctx, btcPool), IsNil)

	// Test that modulus operation works correctly for large numbers
	tx := GetRandomObservedTx()

	// Reference of zero is invalid.
	tx.Tx.Coins = common.NewCoins(common.NewCoin(common.BTCAsset, cosmos.NewUint(9999999900000)))
	refID, err := generateReferenceMemoID(ctx, mgr, common.BTCAsset, tx)
	c.Assert(err, NotNil)
	c.Assert(refID, Equals, "")

	// Test with amount that results in exactly 99999
	tx.Tx.Coins = common.NewCoins(common.NewCoin(common.BTCAsset, cosmos.NewUint(9999999999999)))
	refID, err = generateReferenceMemoID(ctx, mgr, common.BTCAsset, tx)
	c.Assert(err, IsNil)
	c.Assert(refID, Equals, "99999") // 9999999999999 % 100000 = 99999
}

func (s *HandlerObservedTxHelpersSuite) TestReferenceMemoIntegration(c *C) {
	ctx, mgr := setupManagerForTest(c)

	// Setup BTC pool
	btcPool := NewPool()
	btcPool.Asset = common.BTCAsset
	btcPool.Decimals = 8
	btcPool.BalanceAsset = cosmos.NewUint(100 * common.One)
	btcPool.BalanceRune = cosmos.NewUint(100 * common.One)
	c.Assert(mgr.Keeper().SetPool(ctx, btcPool), IsNil)

	// Set TTL for memoless transactions
	mgr.Keeper().SetMimir(ctx, constants.MemolessTxnTTL.String(), 100)

	// Create observed tx with empty memo
	tx := GetRandomObservedTx()
	tx.Tx.Coins = common.NewCoins(common.NewCoin(common.BTCAsset, cosmos.NewUint(123456789)))

	// Generate reference memo ID
	refID, err := generateReferenceMemoID(ctx, mgr, common.BTCAsset, tx)
	c.Assert(err, IsNil)
	c.Assert(refID, Equals, "56789")

	// Create ReferenceReadMemo and generate memo string
	refMemo := NewReferenceReadMemo(refID)
	memoStr := refMemo.CreateMemo()
	c.Assert(memoStr, Equals, "r:56789")

	// Create a reference memo in storage that matches our generated reference
	storedRefMemo := NewReferenceMemo(common.BTCAsset, "SWAP:ETH.ETH:0x1234567890123456789012345678901234567890", refID, 0)
	mgr.Keeper().SetReferenceMemo(ctx, storedRefMemo)

	// Test that fetchMemoFromReference can resolve our generated reference
	tx.Tx.Memo = memoStr
	resolvedMemo := fetchMemoFromReference(ctx, mgr, common.BTCAsset, tx.Tx, 1) // tx observed at height 1, memo created at height 0
	c.Assert(resolvedMemo, Equals, "SWAP:ETH.ETH:0x1234567890123456789012345678901234567890")

	// Verify usage was tracked
	updatedRefMemo, err := mgr.Keeper().GetReferenceMemo(ctx, common.BTCAsset, refID)
	c.Assert(err, IsNil)
	c.Assert(updatedRefMemo.GetUsageCount(), Equals, int64(1))
	c.Assert(updatedRefMemo.HasBeenUsedBy(tx.Tx.ID), Equals, true)
}

func (s *HandlerObservedTxHelpersSuite) TestReferenceMemoIntegrationWithExpiredMemo(c *C) {
	ctx, mgr := setupManagerForTest(c)
	ctx = ctx.WithBlockHeight(100) // Current block height

	// Setup BTC pool
	btcPool := NewPool()
	btcPool.Asset = common.BTCAsset
	btcPool.Decimals = 8
	btcPool.BalanceAsset = cosmos.NewUint(100 * common.One)
	btcPool.BalanceRune = cosmos.NewUint(100 * common.One)
	c.Assert(mgr.Keeper().SetPool(ctx, btcPool), IsNil)

	// Set TTL for reference memos
	mgr.Keeper().SetMimir(ctx, constants.MemolessTxnTTL.String(), 50) // TTL of 50 blocks

	// Create observed tx
	tx := GetRandomObservedTx()
	tx.Tx.Coins = common.NewCoins(common.NewCoin(common.BTCAsset, cosmos.NewUint(123456789)))

	// Generate reference memo ID
	refID, err := generateReferenceMemoID(ctx, mgr, common.BTCAsset, tx)
	c.Assert(err, IsNil)

	// Create an expired reference memo (created 60 blocks ago)
	expiredRefMemo := NewReferenceMemo(common.BTCAsset, "SWAP:ETH.ETH:0x1234567890123456789012345678901234567890", refID, 40) // 40 + 50 < 100, so expired
	mgr.Keeper().SetReferenceMemo(ctx, expiredRefMemo)

	// Create memo string
	refMemo := NewReferenceReadMemo(refID)
	memoStr := refMemo.CreateMemo()

	// Test that fetchMemoFromReference returns empty string for expired memo
	testTx := common.NewTx(common.TxID(""), common.NoAddress, common.NoAddress, common.Coins{}, common.Gas{}, memoStr)
	resolvedMemo := fetchMemoFromReference(ctx, mgr, common.BTCAsset, testTx, 50) // tx observed at height 50, memo created at height 40
	c.Assert(resolvedMemo, Equals, "")
}

func (s *HandlerObservedTxHelpersSuite) TestReferenceMemoIntegrationWithUsageLimit(c *C) {
	ctx, mgr := setupManagerForTest(c)

	// Setup BTC pool
	btcPool := NewPool()
	btcPool.Asset = common.BTCAsset
	btcPool.Decimals = 8
	btcPool.BalanceAsset = cosmos.NewUint(100 * common.One)
	btcPool.BalanceRune = cosmos.NewUint(100 * common.One)
	c.Assert(mgr.Keeper().SetPool(ctx, btcPool), IsNil)

	// Set usage limit
	mgr.Keeper().SetMimir(ctx, constants.MemolessTxnMaxUse.String(), 2) // Max 2 uses

	// Set TTL for memoless transactions
	mgr.Keeper().SetMimir(ctx, constants.MemolessTxnTTL.String(), 100)

	// Create observed tx
	tx := GetRandomObservedTx()
	tx.Tx.Coins = common.NewCoins(common.NewCoin(common.BTCAsset, cosmos.NewUint(123456789)))

	// Generate reference memo ID
	refID, err := generateReferenceMemoID(ctx, mgr, common.BTCAsset, tx)
	c.Assert(err, IsNil)

	// Create reference memo
	storedRefMemo := NewReferenceMemo(common.BTCAsset, "SWAP:ETH.ETH:0x1234567890123456789012345678901234567890", refID, 0)
	mgr.Keeper().SetReferenceMemo(ctx, storedRefMemo)

	// Create memo string
	refMemo := NewReferenceReadMemo(refID)
	memoStr := refMemo.CreateMemo()

	// First usage should succeed
	txID1, _ := common.NewTxID("1111111111111111111111111111111111111111111111111111111111111111")
	testTx := common.NewTx(txID1, common.NoAddress, common.NoAddress, common.Coins{}, common.Gas{}, memoStr)
	resolvedMemo := fetchMemoFromReference(ctx, mgr, common.BTCAsset, testTx, 1) // tx observed at height 1, memo created at height 0
	c.Assert(resolvedMemo, Equals, "SWAP:ETH.ETH:0x1234567890123456789012345678901234567890")

	// Second usage should succeed
	txID2, _ := common.NewTxID("2222222222222222222222222222222222222222222222222222222222222222")
	testTx2 := common.NewTx(txID2, common.NoAddress, common.NoAddress, common.Coins{}, common.Gas{}, memoStr)
	resolvedMemo = fetchMemoFromReference(ctx, mgr, common.BTCAsset, testTx2, 2) // tx observed at height 2, memo created at height 0
	c.Assert(resolvedMemo, Equals, "SWAP:ETH.ETH:0x1234567890123456789012345678901234567890")

	// Third usage should fail (exceed limit)
	txID3, _ := common.NewTxID("3333333333333333333333333333333333333333333333333333333333333333")
	testTx3 := common.NewTx(txID3, common.NoAddress, common.NoAddress, common.Coins{}, common.Gas{}, memoStr)
	resolvedMemo = fetchMemoFromReference(ctx, mgr, common.BTCAsset, testTx3, 3) // tx observed at height 3, memo created at height 0
	c.Assert(resolvedMemo, Equals, "")

	// Verify usage count is 2
	updatedRefMemo, err := mgr.Keeper().GetReferenceMemo(ctx, common.BTCAsset, refID)
	c.Assert(err, IsNil)
	c.Assert(updatedRefMemo.GetUsageCount(), Equals, int64(3))
}

func (s *HandlerObservedTxHelpersSuite) TestReferenceMemoIntegrationNonExistentReference(c *C) {
	ctx, mgr := setupManagerForTest(c)

	// Setup BTC pool
	btcPool := NewPool()
	btcPool.Asset = common.BTCAsset
	btcPool.Decimals = 8
	btcPool.BalanceAsset = cosmos.NewUint(100 * common.One)
	btcPool.BalanceRune = cosmos.NewUint(100 * common.One)
	c.Assert(mgr.Keeper().SetPool(ctx, btcPool), IsNil)

	// Generate reference memo for non-existent reference
	refMemo := NewReferenceReadMemo("99999") // This reference doesn't exist in storage
	memoStr := refMemo.CreateMemo()

	// Test that fetchMemoFromReference returns empty string for non-existent reference
	testTx := common.NewTx(common.TxID(""), common.NoAddress, common.NoAddress, common.Coins{}, common.Gas{}, memoStr)
	resolvedMemo := fetchMemoFromReference(ctx, mgr, common.BTCAsset, testTx, 1) // tx observed at height 1, reference doesn't exist
	c.Assert(resolvedMemo, Equals, "")
}

func (s *HandlerObservedTxHelpersSuite) TestReferenceMemoIntegrationFullWorkflow(c *C) {
	ctx, mgr := setupManagerForTest(c)

	// Setup BTC pool
	btcPool := NewPool()
	btcPool.Asset = common.BTCAsset
	btcPool.Decimals = 8
	btcPool.BalanceAsset = cosmos.NewUint(100 * common.One)
	btcPool.BalanceRune = cosmos.NewUint(100 * common.One)
	c.Assert(mgr.Keeper().SetPool(ctx, btcPool), IsNil)

	// Set TTL for memoless transactions
	mgr.Keeper().SetMimir(ctx, constants.MemolessTxnTTL.String(), 100)

	// Simulate the full workflow:
	// 1. Memoless transaction comes in
	// 2. generateReferenceMemoID creates an ID
	// 3. CreateMemo creates the memo string
	// 4. fetchMemoFromReference resolves it
	// 5. trackReferenceMemoUsage tracks usage

	// Step 1: Create memoless transaction
	tx := GetRandomObservedTx()
	tx.Tx.Memo = "" // Empty memo
	tx.Tx.Coins = common.NewCoins(common.NewCoin(common.BTCAsset, cosmos.NewUint(987654321)))

	// Step 2: Generate reference ID (simulating our new functionality)
	refID, err := generateReferenceMemoID(ctx, mgr, common.BTCAsset, tx)
	c.Assert(err, IsNil)
	c.Assert(refID, Equals, "54321") // last 5 digits of 987654321

	// Step 3: Create memo string (simulating CreateMemo)
	refMemo := NewReferenceReadMemo(refID)
	generatedMemo := refMemo.CreateMemo()
	c.Assert(generatedMemo, Equals, "r:54321")

	// Simulate that a reference memo exists in storage
	actualMemo := "SWAP:BTC.BTC:bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4"
	storedRefMemo := NewReferenceMemo(common.BTCAsset, actualMemo, refID, 0)
	mgr.Keeper().SetReferenceMemo(ctx, storedRefMemo)

	// Step 4: fetchMemoFromReference resolves the generated memo
	testTx := common.NewTx(tx.Tx.ID, common.NoAddress, common.NoAddress, common.Coins{}, common.Gas{}, generatedMemo)
	resolvedMemo := fetchMemoFromReference(ctx, mgr, common.BTCAsset, testTx, 1) // tx observed at height 1, memo created at height 0
	c.Assert(resolvedMemo, Equals, actualMemo)

	// Step 5: Usage is automatically tracked by fetchMemoFromReference

	// Verify the full workflow worked
	updatedRefMemo, err := mgr.Keeper().GetReferenceMemo(ctx, common.BTCAsset, refID)
	c.Assert(err, IsNil)
	c.Assert(updatedRefMemo.GetUsageCount(), Equals, int64(1))
	c.Assert(updatedRefMemo.HasBeenUsedBy(tx.Tx.ID), Equals, true)
}

func (s *HandlerObservedTxHelpersSuite) TestUnfinalizedHeightPreservation(c *C) {
	ctx, mgr := setupManagerForTest(c)

	// Create a test transaction
	tx := GetRandomObservedTx()
	tx.Tx.Chain = common.BTCChain
	tx.Tx.Coins = common.NewCoins(common.NewCoin(common.BTCAsset, cosmos.NewUint(1000000)))

	// Create an empty voter
	voter := NewObservedTxVoter(tx.Tx.ID, []common.ObservedTx{})
	c.Assert(voter.Height, Equals, int64(0))
	c.Assert(voter.UnfinalizedHeight, Equals, int64(0))

	// Mock node accounts for consensus
	nas := NodeAccounts{
		GetRandomValidatorNode(NodeActive),
		GetRandomValidatorNode(NodeActive),
		GetRandomValidatorNode(NodeActive),
	}

	signer := nas[0].NodeAddress

	// Test processTxInAttestation - first consensus (non-finalized)
	ctx = ctx.WithBlockHeight(100)
	voter, ok := processTxInAttestation(ctx, mgr, voter, nas, tx, signer, false)

	// After first consensus, both Height and UnfinalizedHeight should be set to the same value
	if voter.HasConsensus(nas) && !tx.IsFinal() {
		c.Assert(voter.Height, Equals, int64(100))
		c.Assert(voter.UnfinalizedHeight, Equals, int64(100))
		c.Assert(ok, Equals, true)
	}

	// Test that UnfinalizedHeight is preserved when Height changes
	ctx = ctx.WithBlockHeight(200)

	// Add another observation to trigger finalization
	tx.FinaliseHeight = 150
	voter, ok = processTxInAttestation(ctx, mgr, voter, nas, tx, nas[1].NodeAddress, false)

	// After finalization, Height might change but UnfinalizedHeight should remain
	if voter.HasFinalised(nas) {
		c.Assert(voter.FinalisedHeight, Equals, int64(200))
		c.Assert(voter.UnfinalizedHeight, Equals, int64(100)) // Should preserve original consensus height
		c.Assert(ok, Equals, true)
	}
}

func (s *HandlerObservedTxHelpersSuite) TestUnfinalizedHeightUsedInFetchMemoFromReference(c *C) {
	ctx, mgr := setupManagerForTest(c)

	// Set up BTC pool
	btcPool := NewPool()
	btcPool.Asset = common.BTCAsset
	btcPool.Decimals = 8
	btcPool.BalanceAsset = cosmos.NewUint(100 * common.One)
	btcPool.BalanceRune = cosmos.NewUint(100 * common.One)
	c.Assert(mgr.Keeper().SetPool(ctx, btcPool), IsNil)

	// Set TTL for memoless transactions
	mgr.Keeper().SetMimir(ctx, constants.MemolessTxnTTL.String(), 100)

	// Create a reference memo at height 50
	refMemo := NewReferenceMemo(common.BTCAsset, "SWAP:ETH.ETH:0x1234567890123456789012345678901234567890", "12345", 50)
	mgr.Keeper().SetReferenceMemo(ctx, refMemo)

	// Create voter with UnfinalizedHeight set to 60 but Height set to 120
	voter := NewObservedTxVoter(common.TxID("testid"), []common.ObservedTx{})
	voter.Height = 120           // Later consensus height
	voter.UnfinalizedHeight = 60 // Original consensus height when first observed
	voter.Tx.Tx.Memo = "r:12345" // Reference memo

	// Test that fetchMemoFromReference uses UnfinalizedHeight (60), not Height (120)
	// Since reference was created at height 50 and UnfinalizedHeight is 60, this should succeed
	resolvedMemo := fetchMemoFromReference(ctx, mgr, common.BTCAsset, voter.Tx.Tx, voter.UnfinalizedHeight)
	c.Assert(resolvedMemo, Equals, "SWAP:ETH.ETH:0x1234567890123456789012345678901234567890")

	// If we had used Height (120) instead, let's verify it would fail the height check
	// (transaction observed after memo creation: 120 > 50, but this should be caught by height validation)
	resolvedMemoWithHeight := fetchMemoFromReference(ctx, mgr, common.BTCAsset, voter.Tx.Tx, voter.Height)
	c.Assert(resolvedMemoWithHeight, Equals, "SWAP:ETH.ETH:0x1234567890123456789012345678901234567890") // This will still work since 120 > 50

	// Test edge case where using Height would fail but UnfinalizedHeight succeeds
	// Create reference memo at height 70
	refMemo2 := NewReferenceMemo(common.BTCAsset, "SWAP:BTC.BTC:bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4", "54321", 70)
	mgr.Keeper().SetReferenceMemo(ctx, refMemo2)

	// Set voter with UnfinalizedHeight = 75 (valid) but Height = 65 (invalid - before memo creation)
	voter.Height = 65            // This would fail height validation (65 <= 70)
	voter.UnfinalizedHeight = 75 // This should pass height validation (75 > 70)
	voter.Tx.Tx.Memo = "r:54321"

	// Using UnfinalizedHeight should succeed
	resolvedMemo = fetchMemoFromReference(ctx, mgr, common.BTCAsset, voter.Tx.Tx, voter.UnfinalizedHeight)
	c.Assert(resolvedMemo, Equals, "SWAP:BTC.BTC:bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4")

	// Using Height should fail
	resolvedMemoWithHeight = fetchMemoFromReference(ctx, mgr, common.BTCAsset, voter.Tx.Tx, voter.Height)
	c.Assert(resolvedMemoWithHeight, Equals, "") // Should fail due to height validation
}
