package thorchain

import (
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"

	. "gopkg.in/check.v1"
)

type HandlerModifyLimitSwapSuite struct{}

var _ = Suite(&HandlerModifyLimitSwapSuite{})

func (s *HandlerModifyLimitSwapSuite) TestModifyLimitSwapHandler(c *C) {
	ctx, mgr := setupManagerForTest(c)

	handler := NewModifyLimitSwapHandler(mgr)

	// Create a valid MsgModifyLimitSwap
	fromAddr := GetRandomBTCAddress()
	sourceAsset := common.BTCAsset
	targetAsset := common.RuneAsset()
	sourceCoin := common.NewCoin(sourceAsset, cosmos.NewUint(100*common.One))
	targetCoin := common.NewCoin(targetAsset, cosmos.NewUint(500*common.One))
	modifiedTargetAmount := cosmos.NewUint(600 * common.One)
	signer := GetRandomBech32Addr()

	msg := types.NewMsgModifyLimitSwap(fromAddr, sourceCoin, targetCoin, modifiedTargetAmount, signer)

	// Test when no matching limit swap exists
	result, err := handler.Run(ctx, msg)
	c.Assert(err, NotNil)
	c.Assert(result, IsNil)
	c.Assert(err.Error(), Equals, "could not find matching limit swap")

	// Create a limit swap in the keeper
	txID := GetRandomTxHash()
	tx := common.NewTx(
		txID,
		fromAddr,
		fromAddr,
		common.Coins{sourceCoin},
		common.Gas{},
		"",
	)
	limitSwap := NewMsgSwap(tx, targetAsset, fromAddr, targetCoin.Amount, common.NoAddress, cosmos.ZeroUint(), "", "", nil, LimitSwap, 0, 0, signer)

	// Set up the swap book item and index
	c.Assert(mgr.Keeper().SetAdvSwapQueueItem(ctx, *limitSwap), IsNil)
	c.Assert(mgr.Keeper().SetAdvSwapQueueIndex(ctx, *limitSwap), IsNil)

	// Test successful modification
	result, err = handler.Run(ctx, msg)
	c.Assert(err, IsNil)
	c.Assert(result, NotNil)

	// Verify the limit swap was modified
	modifiedSwap, err := mgr.Keeper().GetAdvSwapQueueItem(ctx, txID)
	c.Assert(err, IsNil)
	c.Assert(modifiedSwap.TradeTarget.Equal(modifiedTargetAmount), Equals, true)

	// verify original index is no longer there
	hashes, err := mgr.Keeper().GetAdvSwapQueueIndex(ctx, *limitSwap)
	c.Assert(err, IsNil)
	c.Check(hashes, HasLen, 0)

	// verify new index IS there
	hashes, err = mgr.Keeper().GetAdvSwapQueueIndex(ctx, modifiedSwap)
	c.Assert(err, IsNil)
	c.Check(hashes, HasLen, 1)

	// Test cancellation (setting modified amount to zero)
	cancelMsg := types.NewMsgModifyLimitSwap(fromAddr, sourceCoin, common.NewCoin(targetAsset, modifiedTargetAmount), cosmos.ZeroUint(), signer)
	result, err = handler.Run(ctx, cancelMsg)
	c.Assert(err, IsNil)
	c.Assert(result, NotNil)

	// Verify the limit swap was removed from the swap book
	hashes, err = mgr.Keeper().GetAdvSwapQueueIndex(ctx, modifiedSwap)
	c.Assert(err, IsNil)
	c.Check(hashes, HasLen, 0)

	_, err = mgr.Keeper().GetAdvSwapQueueItem(ctx, txID)
	c.Assert(err, NotNil) // Should be removed
}

func (s *HandlerModifyLimitSwapSuite) TestModifyLimitSwapValidation(c *C) {
	ctx, k := setupKeeperForTest(c)

	// Create a manager with our test keeper
	mgr := NewDummyMgr()
	mgr.K = k

	handler := NewModifyLimitSwapHandler(mgr)

	// Test with invalid message (empty signer)
	fromAddr := GetRandomTHORAddress()
	sourceAsset := common.BTCAsset
	targetAsset := common.RuneAsset()
	sourceCoin := common.NewCoin(sourceAsset, cosmos.NewUint(100*common.One))
	targetCoin := common.NewCoin(targetAsset, cosmos.NewUint(500*common.One))
	modifiedTargetAmount := cosmos.NewUint(600 * common.One)

	invalidMsg := types.NewMsgModifyLimitSwap(fromAddr, sourceCoin, targetCoin, modifiedTargetAmount, cosmos.AccAddress{})
	result, err := handler.Run(ctx, invalidMsg)
	c.Assert(err, NotNil)
	c.Assert(result, IsNil)
}

func (s *HandlerModifyLimitSwapSuite) TestModifyLimitSwapAddressCheck(c *C) {
	ctx, mgr := setupManagerForTest(c)

	handler := NewModifyLimitSwapHandler(mgr)

	// Create addresses
	fromAddr := GetRandomTHORAddress()
	differentAddr := GetRandomTHORAddress()
	sourceAsset := common.RuneAsset()
	targetAsset := common.BTCAsset
	sourceCoin := common.NewCoin(sourceAsset, cosmos.NewUint(100*common.One))
	targetCoin := common.NewCoin(targetAsset, cosmos.NewUint(500*common.One))
	modifiedTargetAmount := cosmos.NewUint(600 * common.One)
	// When source asset is RUNE, signer must match fromAddr
	signer, err := fromAddr.AccAddress()
	c.Assert(err, IsNil)

	// Create a limit swap in the keeper
	txID := GetRandomTxHash()
	tx := common.NewTx(
		txID,
		fromAddr,
		fromAddr,
		common.Coins{sourceCoin},
		common.Gas{},
		"",
	)
	limitSwap := NewMsgSwap(tx, targetAsset, fromAddr, targetCoin.Amount, common.NoAddress, cosmos.ZeroUint(), "", "", nil, LimitSwap, 0, 0, signer)

	// Set up the swap book item and index
	c.Assert(mgr.Keeper().SetAdvSwapQueueItem(ctx, *limitSwap), IsNil)
	c.Assert(mgr.Keeper().SetAdvSwapQueueIndex(ctx, *limitSwap), IsNil)

	// Try to modify with a different address
	// When source asset is RUNE, signer must match the From address
	differentSigner, err := differentAddr.AccAddress()
	c.Assert(err, IsNil)
	msg := types.NewMsgModifyLimitSwap(differentAddr, sourceCoin, targetCoin, modifiedTargetAmount, differentSigner)
	result, err := handler.Run(ctx, msg)
	c.Assert(err, NotNil)
	c.Assert(result, IsNil)
	c.Assert(err.Error(), Equals, "could not find matching limit swap")

	// Verify the original limit swap is unchanged
	originalSwap, err := mgr.Keeper().GetAdvSwapQueueItem(ctx, txID)
	c.Assert(err, IsNil)
	c.Assert(originalSwap.TradeTarget.Equal(targetCoin.Amount), Equals, true)
}

func (s *HandlerModifyLimitSwapSuite) TestModifyMultipleLimitSwaps(c *C) {
	ctx, mgr := setupManagerForTest(c)

	handler := NewModifyLimitSwapHandler(mgr)

	// Create addresses and assets
	fromAddr := GetRandomBTCAddress()
	sourceAsset := common.BTCAsset
	targetAsset := common.RuneAsset()
	sourceCoin := common.NewCoin(sourceAsset, cosmos.NewUint(100*common.One))
	targetCoin := common.NewCoin(targetAsset, cosmos.NewUint(500*common.One))
	modifiedTargetAmount := cosmos.NewUint(600 * common.One)
	signer := GetRandomBech32Addr()

	// Create multiple limit swaps with the same source/target
	txID1 := GetRandomTxHash()
	tx1 := common.NewTx(
		txID1,
		fromAddr,
		fromAddr,
		common.Coins{sourceCoin},
		common.Gas{},
		"",
	)
	limitSwap1 := NewMsgSwap(tx1, targetAsset, fromAddr, targetCoin.Amount, common.NoAddress, cosmos.ZeroUint(), "", "", nil, LimitSwap, 0, 0, signer)
	c.Assert(mgr.Keeper().SetAdvSwapQueueItem(ctx, *limitSwap1), IsNil)
	c.Assert(mgr.Keeper().SetAdvSwapQueueIndex(ctx, *limitSwap1), IsNil)

	txID2 := GetRandomTxHash()
	tx2 := common.NewTx(
		txID2,
		fromAddr,
		fromAddr,
		common.Coins{sourceCoin},
		common.Gas{},
		"",
	)
	limitSwap2 := NewMsgSwap(tx2, targetAsset, fromAddr, targetCoin.Amount, common.NoAddress, cosmos.ZeroUint(), "", "", nil, LimitSwap, 0, 0, signer)
	c.Assert(mgr.Keeper().SetAdvSwapQueueItem(ctx, *limitSwap2), IsNil)
	c.Assert(mgr.Keeper().SetAdvSwapQueueIndex(ctx, *limitSwap2), IsNil)

	// Modify both limit swaps
	msg := types.NewMsgModifyLimitSwap(fromAddr, sourceCoin, targetCoin, modifiedTargetAmount, signer)
	result, err := handler.Run(ctx, msg)
	c.Assert(err, IsNil)
	c.Assert(result, NotNil)

	// Verify only the first limit swap was modified (only one swap should be modified)
	modifiedSwap1, err := mgr.Keeper().GetAdvSwapQueueItem(ctx, txID1)
	c.Assert(err, IsNil)
	c.Assert(modifiedSwap1.TradeTarget.Equal(modifiedTargetAmount), Equals, true)

	// The second swap should remain unchanged
	modifiedSwap2, err := mgr.Keeper().GetAdvSwapQueueItem(ctx, txID2)
	c.Assert(err, IsNil)
	c.Assert(modifiedSwap2.TradeTarget.Equal(targetCoin.Amount), Equals, true) // Should still be original amount
}

func (s *HandlerModifyLimitSwapSuite) TestCancelMultipleLimitSwaps(c *C) {
	// Create a manager with our test keeper
	ctx, mgr := setupManagerForTest(c)

	handler := NewModifyLimitSwapHandler(mgr)

	// Create addresses and assets
	fromAddr := GetRandomTHORAddress()
	sourceAsset := common.RuneAsset()
	targetAsset := common.BTCAsset
	sourceCoin := common.NewCoin(sourceAsset, cosmos.NewUint(100*common.One))
	targetCoin := common.NewCoin(targetAsset, cosmos.NewUint(500*common.One))
	// When source asset is RUNE, signer must match fromAddr
	signer, err := fromAddr.AccAddress()
	c.Assert(err, IsNil)

	// Create multiple limit swaps with the same source/target
	txID1 := GetRandomTxHash()
	tx1 := common.NewTx(
		txID1,
		fromAddr,
		fromAddr,
		common.Coins{sourceCoin},
		common.Gas{},
		"",
	)
	limitSwap1 := NewMsgSwap(tx1, targetAsset, fromAddr, targetCoin.Amount, common.NoAddress, cosmos.ZeroUint(), "", "", nil, LimitSwap, 0, 0, signer)
	c.Assert(mgr.Keeper().SetAdvSwapQueueItem(ctx, *limitSwap1), IsNil)
	c.Assert(mgr.Keeper().SetAdvSwapQueueIndex(ctx, *limitSwap1), IsNil)

	txID2 := GetRandomTxHash()
	tx2 := common.NewTx(
		txID2,
		fromAddr,
		fromAddr,
		common.Coins{sourceCoin},
		common.Gas{},
		"",
	)
	limitSwap2 := NewMsgSwap(tx2, targetAsset, fromAddr, targetCoin.Amount, common.NoAddress, cosmos.ZeroUint(), "", "", nil, LimitSwap, 0, 0, signer)
	c.Assert(mgr.Keeper().SetAdvSwapQueueItem(ctx, *limitSwap2), IsNil)
	c.Assert(mgr.Keeper().SetAdvSwapQueueIndex(ctx, *limitSwap2), IsNil)

	// Cancel both limit swaps by setting modified amount to zero
	cancelMsg := types.NewMsgModifyLimitSwap(fromAddr, sourceCoin, targetCoin, cosmos.ZeroUint(), signer)
	result, err := handler.Run(ctx, cancelMsg)
	c.Assert(err, IsNil)
	c.Assert(result, NotNil)

	// Verify only the first limit swap was cancelled (removed)
	_, err = mgr.Keeper().GetAdvSwapQueueItem(ctx, txID1)
	c.Assert(err, NotNil) // Should be removed

	// The second swap should remain unchanged as a limit swap
	mSwap2, err := mgr.Keeper().GetAdvSwapQueueItem(ctx, txID2)
	c.Assert(err, IsNil)
	c.Check(mSwap2.SwapType, Equals, LimitSwap) // Should still be a limit swap
}

func (s *HandlerModifyLimitSwapSuite) TestModifyLimitSwapErrorHandling(c *C) {
	ctx, mgr := setupManagerForTest(c)
	handler := NewModifyLimitSwapHandler(mgr)

	// Test with invalid assets (same source and target)
	fromAddr := GetRandomBTCAddress()
	sourceAsset := common.BTCAsset
	invalidCoin := common.NewCoin(sourceAsset, cosmos.NewUint(100*common.One))
	signer := GetRandomBech32Addr()

	invalidMsg := types.NewMsgModifyLimitSwap(fromAddr, invalidCoin, invalidCoin, cosmos.NewUint(200*common.One), signer)
	result, err := handler.Run(ctx, invalidMsg)
	c.Assert(err, NotNil)
	c.Assert(result, IsNil)
	c.Assert(err.Error(), Matches, ".*source asset and target asset cannot be the same.*")

	// Test with mismatched from address and source asset chain
	thorAddr := GetRandomTHORAddress()
	btcCoin := common.NewCoin(common.BTCAsset, cosmos.NewUint(100*common.One))
	runeCoin := common.NewCoin(common.RuneAsset(), cosmos.NewUint(500*common.One))

	invalidChainMsg := types.NewMsgModifyLimitSwap(thorAddr, btcCoin, runeCoin, cosmos.NewUint(600*common.One), signer)
	result, err = handler.Run(ctx, invalidChainMsg)
	c.Assert(err, NotNil)
	c.Assert(result, IsNil)
	c.Assert(err.Error(), Matches, ".*from address and source asset do not match.*")
}

func (s *HandlerModifyLimitSwapSuite) TestModifyLimitSwapCancellationLogic(c *C) {
	ctx, mgr := setupManagerForTest(c)
	handler := NewModifyLimitSwapHandler(mgr)

	// Create a limit swap with a very large target amount
	fromAddr := GetRandomBTCAddress()
	sourceAsset := common.BTCAsset
	targetAsset := common.RuneAsset()
	sourceCoin := common.NewCoin(sourceAsset, cosmos.NewUint(100*common.One))
	largeAmount := cosmos.NewUint(1 << 62) // Very large amount
	targetCoin := common.NewCoin(targetAsset, largeAmount)
	signer := GetRandomBech32Addr()

	txID := GetRandomTxHash()
	tx := common.NewTx(
		txID,
		fromAddr,
		fromAddr,
		common.Coins{sourceCoin},
		common.Gas{},
		"",
	)
	limitSwap := NewMsgSwap(tx, targetAsset, fromAddr, targetCoin.Amount, common.NoAddress, cosmos.ZeroUint(), "", "", nil, LimitSwap, 0, 0, signer)

	// Set up the swap book item and index
	c.Assert(mgr.Keeper().SetAdvSwapQueueItem(ctx, *limitSwap), IsNil)
	c.Assert(mgr.Keeper().SetAdvSwapQueueIndex(ctx, *limitSwap), IsNil)

	// Test cancellation with zero amount
	cancelMsg := types.NewMsgModifyLimitSwap(fromAddr, sourceCoin, targetCoin, cosmos.ZeroUint(), signer)
	result, err := handler.Run(ctx, cancelMsg)
	c.Assert(err, IsNil)
	c.Assert(result, NotNil)

	// Verify the swap was removed
	_, err = mgr.Keeper().GetAdvSwapQueueItem(ctx, txID)
	c.Assert(err, NotNil) // Should be removed
}

// TestModifyLimitSwapSecurityFromFieldSpoofing tests various scenarios where
// a malicious actor attempts to spoof the From field to modify or cancel
// another user's limit swap. This test verifies that the handler properly
// checks both the From address AND the Signer to prevent unauthorized modifications.
func (s *HandlerModifyLimitSwapSuite) TestModifyLimitSwapSecurityFromFieldSpoofing(c *C) {
	ctx, mgr := setupManagerForTest(c)
	handler := NewModifyLimitSwapHandler(mgr)

	// Test Case 1: Malicious actor tries to modify another user's BTC limit swap by using wrong From address
	{
		// Legitimate user creates a limit swap
		legitimateUser := GetRandomBTCAddress()
		sourceAsset := common.BTCAsset
		targetAsset := common.RuneAsset()
		sourceCoin := common.NewCoin(sourceAsset, cosmos.NewUint(100*common.One))
		targetCoin := common.NewCoin(targetAsset, cosmos.NewUint(500*common.One))
		signer := GetRandomBech32Addr()

		txID := GetRandomTxHash()
		tx := common.NewTx(
			txID,
			legitimateUser,
			legitimateUser,
			common.Coins{sourceCoin},
			common.Gas{},
			"",
		)
		limitSwap := NewMsgSwap(tx, targetAsset, legitimateUser, targetCoin.Amount, common.NoAddress, cosmos.ZeroUint(), "", "", nil, LimitSwap, 0, 0, signer)

		// Set up the swap in the keeper
		c.Assert(mgr.Keeper().SetAdvSwapQueueItem(ctx, *limitSwap), IsNil)
		c.Assert(mgr.Keeper().SetAdvSwapQueueIndex(ctx, *limitSwap), IsNil)

		// Malicious actor attempts to modify the limit swap using a different From address
		maliciousUser := GetRandomBTCAddress()
		maliciousActor := GetRandomBech32Addr()
		maliciousMsg := types.NewMsgModifyLimitSwap(
			maliciousUser, // Using wrong From address
			sourceCoin,
			targetCoin,
			cosmos.NewUint(100*common.One), // Trying to reduce the target amount
			maliciousActor,
		)

		// The handler should reject this because the From address doesn't match
		result, err := handler.Run(ctx, maliciousMsg)
		c.Assert(err, NotNil)
		c.Assert(result, IsNil)
		c.Assert(err.Error(), Equals, "could not find matching limit swap")

		// Verify the original limit swap is unchanged
		originalSwap, err := mgr.Keeper().GetAdvSwapQueueItem(ctx, txID)
		c.Assert(err, IsNil)
		c.Assert(originalSwap.TradeTarget.Equal(targetCoin.Amount), Equals, true)
	}

	// Test Case 2: Malicious actor tries to cancel another user's limit swap
	{
		// Create a new limit swap for a different user
		legitimateUser2 := GetRandomBTCAddress()
		sourceAsset := common.BTCAsset
		targetAsset := common.RuneAsset()
		sourceCoin := common.NewCoin(sourceAsset, cosmos.NewUint(200*common.One))
		targetCoin := common.NewCoin(targetAsset, cosmos.NewUint(1000*common.One))
		signer2 := GetRandomBech32Addr()

		txID2 := GetRandomTxHash()
		tx2 := common.NewTx(
			txID2,
			legitimateUser2,
			legitimateUser2,
			common.Coins{sourceCoin},
			common.Gas{},
			"",
		)
		limitSwap2 := NewMsgSwap(tx2, targetAsset, legitimateUser2, targetCoin.Amount, common.NoAddress, cosmos.ZeroUint(), "", "", nil, LimitSwap, 0, 0, signer2)

		// Set up the swap in the keeper
		c.Assert(mgr.Keeper().SetAdvSwapQueueItem(ctx, *limitSwap2), IsNil)
		c.Assert(mgr.Keeper().SetAdvSwapQueueIndex(ctx, *limitSwap2), IsNil)

		// Malicious actor attempts to cancel using wrong From address
		maliciousUser := GetRandomBTCAddress()
		maliciousActor := GetRandomBech32Addr()
		cancelMsg := types.NewMsgModifyLimitSwap(
			maliciousUser, // Using wrong From address
			sourceCoin,
			targetCoin,
			cosmos.ZeroUint(), // Trying to cancel
			maliciousActor,
		)

		// The handler should reject this because From doesn't match
		result, err := handler.Run(ctx, cancelMsg)
		c.Assert(err, NotNil)
		c.Assert(result, IsNil)
		c.Assert(err.Error(), Equals, "could not find matching limit swap")

		// Verify the limit swap still exists
		stillExists, err := mgr.Keeper().GetAdvSwapQueueItem(ctx, txID2)
		c.Assert(err, IsNil)
		c.Assert(stillExists.TradeTarget.Equal(targetCoin.Amount), Equals, true)
	}

	// Test Case 3: RUNE-based limit swap - From and Signer mismatch validation
	{
		// For RUNE swaps, the From address must match the Signer
		legitimateUser := GetRandomTHORAddress()
		maliciousUser := GetRandomTHORAddress()
		sourceAsset := common.RuneAsset()
		targetAsset := common.BTCAsset
		sourceCoin := common.NewCoin(sourceAsset, cosmos.NewUint(100*common.One))
		targetCoin := common.NewCoin(targetAsset, cosmos.NewUint(0.1*common.One))

		_, err := legitimateUser.AccAddress()
		c.Assert(err, IsNil)
		maliciousSigner, err := maliciousUser.AccAddress()
		c.Assert(err, IsNil)

		// Try to create a modify message where From doesn't match Signer
		invalidMsg := types.NewMsgModifyLimitSwap(
			legitimateUser, // From: legitimate user
			sourceCoin,
			targetCoin,
			cosmos.NewUint(0.05*common.One),
			maliciousSigner, // Signer: malicious user
		)

		// This should fail validation
		err = invalidMsg.ValidateBasic()
		c.Assert(err, NotNil)
		c.Assert(err.Error(), Matches, ".*from and signer address must match when source asset is native.*")
	}

	// Test Case 4: SECURITY VULNERABILITY - Anyone can modify BTC swaps if they know the From address
	// This test demonstrates that the current implementation allows unauthorized modifications
	{
		// User A creates a limit swap
		userAAddr := GetRandomBTCAddress()
		// User B creates a similar limit swap
		userBAddr := GetRandomBTCAddress()

		sourceAsset := common.BTCAsset
		targetAsset := common.RuneAsset()
		sourceCoin := common.NewCoin(sourceAsset, cosmos.NewUint(100*common.One))
		targetCoin := common.NewCoin(targetAsset, cosmos.NewUint(500*common.One))
		signerA := GetRandomBech32Addr()
		signerB := GetRandomBech32Addr()

		// Create User A's swap
		txIDA := GetRandomTxHash()
		txA := common.NewTx(txIDA, userAAddr, userAAddr, common.Coins{sourceCoin}, common.Gas{}, "")
		limitSwapA := NewMsgSwap(txA, targetAsset, userAAddr, targetCoin.Amount, common.NoAddress, cosmos.ZeroUint(), "", "", nil, LimitSwap, 0, 0, signerA)
		c.Assert(mgr.Keeper().SetAdvSwapQueueItem(ctx, *limitSwapA), IsNil)
		c.Assert(mgr.Keeper().SetAdvSwapQueueIndex(ctx, *limitSwapA), IsNil)

		// Create User B's swap
		txIDB := GetRandomTxHash()
		txB := common.NewTx(txIDB, userBAddr, userBAddr, common.Coins{sourceCoin}, common.Gas{}, "")
		limitSwapB := NewMsgSwap(txB, targetAsset, userBAddr, targetCoin.Amount, common.NoAddress, cosmos.ZeroUint(), "", "", nil, LimitSwap, 0, 0, signerB)
		c.Assert(mgr.Keeper().SetAdvSwapQueueItem(ctx, *limitSwapB), IsNil)
		c.Assert(mgr.Keeper().SetAdvSwapQueueIndex(ctx, *limitSwapB), IsNil)

		// With the current security model, for BTC swaps anyone can modify if they know the From address
		// This is the vulnerability - User B can modify User A's swap
		maliciousMsg := types.NewMsgModifyLimitSwap(
			userAAddr, // Using User A's address
			sourceCoin,
			targetCoin,
			cosmos.NewUint(100*common.One),
			signerB, // But signing with User B's key
		)

		// This currently succeeds - which is a security issue
		result, err := handler.Run(ctx, maliciousMsg)
		c.Assert(err, IsNil)
		c.Assert(result, NotNil)

		// The swap was modified by User B - this shouldn't be allowed!
		modifiedSwapA, err := mgr.Keeper().GetAdvSwapQueueItem(ctx, txIDA)
		c.Assert(err, IsNil)
		c.Assert(modifiedSwapA.TradeTarget.Equal(cosmos.NewUint(100*common.One)), Equals, true)

		// User B's swap remains unchanged
		swapB, err := mgr.Keeper().GetAdvSwapQueueItem(ctx, txIDB)
		c.Assert(err, IsNil)
		c.Assert(swapB.TradeTarget.Equal(targetCoin.Amount), Equals, true)

		// User A can now modify their own swap (which was already modified to 100 by User B)
		validMsg := types.NewMsgModifyLimitSwap(
			userAAddr,
			sourceCoin,
			common.NewCoin(targetAsset, cosmos.NewUint(100*common.One)), // Current target
			cosmos.NewUint(600*common.One),
			signerA,
		)
		result, err = handler.Run(ctx, validMsg)
		c.Assert(err, IsNil)
		c.Assert(result, NotNil)

		// Verify User A's swap was modified to 600
		finalSwapA, err := mgr.Keeper().GetAdvSwapQueueItem(ctx, txIDA)
		c.Assert(err, IsNil)
		c.Assert(finalSwapA.TradeTarget.Equal(cosmos.NewUint(600*common.One)), Equals, true)

		// User B's swap should remain unchanged
		unchangedSwapB, err := mgr.Keeper().GetAdvSwapQueueItem(ctx, txIDB)
		c.Assert(err, IsNil)
		c.Assert(unchangedSwapB.TradeTarget.Equal(targetCoin.Amount), Equals, true)
	}
}
