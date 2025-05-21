package thorchain

import (
	"errors"

	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/x/thorchain/keeper/types"
	types2 "gitlab.com/thorchain/thornode/v3/x/thorchain/types"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
)

type SlashingVCURSuite struct{}

var _ = Suite(&SlashingVCURSuite{})

func (s *SlashingVCURSuite) SetUpSuite(_ *C) {
	SetupConfigForTest()
}

func (s *SlashingVCURSuite) TestNodeSignSlashErrors(c *C) {
	testCases := []struct {
		name        string
		condition   func(keeper *TestSlashingLackKeeper)
		shouldError bool
	}{
		{
			name: "fail to get tx out should return an error",
			condition: func(keeper *TestSlashingLackKeeper) {
				keeper.failGetTxOut = true
			},
			shouldError: true,
		},
		{
			name: "fail to get vault should return an error",
			condition: func(keeper *TestSlashingLackKeeper) {
				keeper.failGetVault = true
			},
			shouldError: false,
		},
		{
			name: "fail to get node account by pub key should return an error",
			condition: func(keeper *TestSlashingLackKeeper) {
				keeper.failGetNodeAccountByPubKey = true
			},
			shouldError: false,
		},
		{
			name: "fail to get asgard vault by status should return an error",
			condition: func(keeper *TestSlashingLackKeeper) {
				keeper.failGetAsgardByStatus = true
			},
			shouldError: true,
		},
		{
			name: "fail to get observed tx voter should return an error",
			condition: func(keeper *TestSlashingLackKeeper) {
				keeper.failGetObservedTxVoter = true
			},
			shouldError: true,
		},
		{
			name: "fail to set tx out should return an error",
			condition: func(keeper *TestSlashingLackKeeper) {
				keeper.failSetTxOut = true
			},
			shouldError: true,
		},
	}
	for _, item := range testCases {
		c.Logf("name:%s", item.name)
		ctx, _ := setupKeeperForTest(c)
		ctx = ctx.WithBlockHeight(201) // set blockheight
		ver := GetCurrentVersion()
		constAccessor := constants.GetConstantValues(ver)
		na := GetRandomValidatorNode(NodeActive)
		inTx := common.NewTx(
			GetRandomTxHash(),
			GetRandomETHAddress(),
			GetRandomETHAddress(),
			common.Coins{
				common.NewCoin(common.ETHAsset, cosmos.NewUint(320000000)),
				common.NewCoin(common.RuneAsset(), cosmos.NewUint(420000000)),
			},
			nil,
			"SWAP:ETH.ETH",
		)

		txOutItem := TxOutItem{
			Chain:       common.ETHChain,
			InHash:      inTx.ID,
			VaultPubKey: na.PubKeySet.Secp256k1,
			ToAddress:   GetRandomETHAddress(),
			Coin: common.NewCoin(
				common.ETHAsset, cosmos.NewUint(3980500*common.One),
			),
		}
		txOut := NewTxOut(3)
		txOut.TxArray = append(txOut.TxArray, txOutItem)

		vault := GetRandomVault()
		vault.Type = AsgardVault
		keeper := &TestSlashingLackKeeper{
			txOut:  txOut,
			na:     na,
			vaults: Vaults{vault},
			voter: ObservedTxVoter{
				Actions: []TxOutItem{txOutItem},
			},
			slashPts: make(map[string]int64),
		}
		signingTransactionPeriod := constAccessor.GetInt64Value(constants.SigningTransactionPeriod)
		ctx = ctx.WithBlockHeight(3 + signingTransactionPeriod)
		slasher := newSlasherVCUR(keeper, NewDummyEventMgr())
		item.condition(keeper)
		if item.shouldError {
			c.Assert(slasher.LackSigning(ctx, NewDummyMgr()), NotNil)
		} else {
			c.Assert(slasher.LackSigning(ctx, NewDummyMgr()), IsNil)
		}
	}
}

func (s *SlashingVCURSuite) TestNotSigningSlash(c *C) {
	ctx, _ := setupKeeperForTest(c)
	ctx = ctx.WithBlockHeight(201) // set blockheight
	txOutStore := NewTxStoreDummy()
	ver := GetCurrentVersion()
	constAccessor := constants.GetConstantValues(ver)
	na := GetRandomValidatorNode(NodeActive)
	inTx := common.NewTx(
		GetRandomTxHash(),
		GetRandomETHAddress(),
		GetRandomETHAddress(),
		common.Coins{
			common.NewCoin(common.ETHAsset, cosmos.NewUint(320000000)),
			common.NewCoin(common.RuneAsset(), cosmos.NewUint(420000000)),
		},
		nil,
		"SWAP:ETH.ETH",
	)

	txOutItem := TxOutItem{
		Chain:       common.ETHChain,
		InHash:      inTx.ID,
		VaultPubKey: na.PubKeySet.Secp256k1,
		ToAddress:   GetRandomETHAddress(),
		Coin: common.NewCoin(
			common.ETHAsset, cosmos.NewUint(3980500*common.One),
		),
	}
	txOut := NewTxOut(3)
	txOut.TxArray = append(txOut.TxArray, txOutItem)

	vault := GetRandomVault()
	vault.Type = AsgardVault
	vault.Coins = common.Coins{
		common.NewCoin(common.ETHAsset, cosmos.NewUint(5000000*common.One)),
	}
	keeper := &TestSlashingLackKeeper{
		txOut:  txOut,
		na:     na,
		vaults: Vaults{vault},
		voter: ObservedTxVoter{
			Actions: []TxOutItem{txOutItem},
		},
		slashPts: make(map[string]int64),
	}
	signingTransactionPeriod := constAccessor.GetInt64Value(constants.SigningTransactionPeriod)
	ctx = ctx.WithBlockHeight(3 + signingTransactionPeriod)
	mgr := NewDummyMgr()
	mgr.txOutStore = txOutStore
	slasher := newSlasherVCUR(keeper, NewDummyEventMgr())
	c.Assert(slasher.LackSigning(ctx, mgr), IsNil)

	outItems, err := txOutStore.GetOutboundItems(ctx)
	c.Assert(err, IsNil)
	c.Assert(outItems, HasLen, 1)
	c.Assert(outItems[0].VaultPubKey.Equals(keeper.vaults[0].PubKey), Equals, true)
	c.Assert(outItems[0].Memo, Equals, "")
	c.Assert(keeper.voter.Actions, HasLen, 1)
	// ensure we've updated our action item
	c.Assert(keeper.voter.Actions[0].VaultPubKey.Equals(outItems[0].VaultPubKey), Equals, true)
	c.Assert(keeper.txOut.TxArray[0].OutHash.IsEmpty(), Equals, false)
}

func (s *SlashingVCURSuite) TestNewSlasher(c *C) {
	nas := NodeAccounts{
		GetRandomValidatorNode(NodeActive),
		GetRandomValidatorNode(NodeActive),
	}
	keeper := &TestSlashObservingKeeper{
		nas:      nas,
		addrs:    []cosmos.AccAddress{nas[0].NodeAddress},
		slashPts: make(map[string]int64),
	}
	slasher := newSlasherVCUR(keeper, NewDummyEventMgr())
	c.Assert(slasher, NotNil)
}

func (s *SlashingVCURSuite) TestHandleSuccessfulSign(c *C) {
	ctx, _ := setupKeeperForTest(c)
	constAccessor := constants.GetConstantValues(GetCurrentVersion())

	testCases := []struct {
		name                  string
		setupNodeAccount      func() (NodeAccount, error)
		validatorAddr         string
		expectedMissingBefore uint64
		expectedMissingAfter  uint64
		expectedErr           error
	}{
		{
			name: "normal node account with missing blocks",
			setupNodeAccount: func() (NodeAccount, error) {
				na := GetRandomValidatorNode(NodeActive)
				na.MissingBlocks = 10
				return na, nil
			},
			expectedMissingBefore: 10,
			expectedMissingAfter:  9,
			expectedErr:           nil,
		},
		{
			name: "node account with zero missing blocks",
			setupNodeAccount: func() (NodeAccount, error) {
				na := GetRandomValidatorNode(NodeActive)
				na.MissingBlocks = 0
				return na, nil
			},
			expectedMissingBefore: 0,
			expectedMissingAfter:  0,
			expectedErr:           nil,
		},
		{
			name: "node account with max missing blocks",
			setupNodeAccount: func() (NodeAccount, error) {
				na := GetRandomValidatorNode(NodeActive)
				na.MissingBlocks = 100
				return na, nil
			},
			expectedMissingBefore: 100,
			expectedMissingAfter:  99,
			expectedErr:           nil,
		},
	}

	for _, tc := range testCases {
		c.Logf("Test case: %s", tc.name)
		na, err := tc.setupNodeAccount()
		c.Assert(err, IsNil)

		keeper := &TestDoubleSlashKeeper{
			na:          na,
			network:     NewNetwork(),
			slashPoints: make(map[string]int64),
			constants:   make(map[string]int64),
		}
		slasher := newSlasherVCUR(keeper, NewDummyEventMgr())

		pk, err := cosmos.GetPubKeyFromBech32(cosmos.Bech32PubKeyTypeConsPub, na.ValidatorConsPubKey)
		c.Assert(err, IsNil)

		var pair nodeAddressValidatorAddressPair
		pair.nodeAddress = na.NodeAddress
		pair.validatorAddress = pk.Address()

		c.Assert(keeper.na.MissingBlocks, Equals, tc.expectedMissingBefore)
		err = slasher.HandleSuccessfulSign(ctx, pk.Address(), constAccessor, []nodeAddressValidatorAddressPair{pair})
		c.Assert(err, IsNil)
		c.Assert(keeper.na.MissingBlocks, Equals, tc.expectedMissingAfter)
	}
}

func (s *SlashingVCURSuite) TestHandleSuccessfulSignErrors(c *C) {
	ctx, _ := setupKeeperForTest(c)
	constAccessor := constants.GetConstantValues(GetCurrentVersion())

	// Test case: validator address not found
	na := GetRandomValidatorNode(NodeActive)
	keeper := &TestDoubleSlashKeeper{
		na:          na,
		network:     NewNetwork(),
		slashPoints: make(map[string]int64),
		constants:   make(map[string]int64),
	}
	slasher := newSlasherVCUR(keeper, NewDummyEventMgr())

	randomAddr := GetRandomBech32Addr().String()
	err := slasher.HandleSuccessfulSign(ctx, []byte(randomAddr), constAccessor, []nodeAddressValidatorAddressPair{})
	c.Assert(err, NotNil)
	c.Assert(err.Error(), Matches, "could not find active node account with validator address: .*")
}

func (s *SlashingVCURSuite) TestHandleMissingSign(c *C) {
	ctx, _ := setupKeeperForTest(c)
	constAccessor := constants.GetConstantValues(GetCurrentVersion())

	testCases := []struct {
		name                  string
		setupNodeAccount      func() (NodeAccount, error)
		maxTrack              int64
		missBlockSignSlashPts int64
		expectedMissingBefore uint64
		expectedMissingAfter  uint64
		expectedSlashPoints   int64
		expectedErr           error
	}{
		{
			name: "normal node account with no missing blocks",
			setupNodeAccount: func() (NodeAccount, error) {
				na := GetRandomValidatorNode(NodeActive)
				na.MissingBlocks = 0
				return na, nil
			},
			maxTrack:              10,
			missBlockSignSlashPts: 5,
			expectedMissingBefore: 0,
			expectedMissingAfter:  1,
			expectedSlashPoints:   5,
			expectedErr:           nil,
		},
		{
			name: "node account with some missing blocks",
			setupNodeAccount: func() (NodeAccount, error) {
				na := GetRandomValidatorNode(NodeActive)
				na.MissingBlocks = 5
				return na, nil
			},
			maxTrack:              10,
			missBlockSignSlashPts: 5,
			expectedMissingBefore: 5,
			expectedMissingAfter:  6,
			expectedSlashPoints:   5,
			expectedErr:           nil,
		},
		{
			name: "node account at max missing blocks",
			setupNodeAccount: func() (NodeAccount, error) {
				na := GetRandomValidatorNode(NodeActive)
				na.MissingBlocks = 10
				return na, nil
			},
			maxTrack:              10,
			missBlockSignSlashPts: 5,
			expectedMissingBefore: 10,
			expectedMissingAfter:  10, // Should not increase beyond maxTrack
			expectedSlashPoints:   5,
			expectedErr:           nil,
		},
		{
			name: "node account exceeding max missing blocks",
			setupNodeAccount: func() (NodeAccount, error) {
				na := GetRandomValidatorNode(NodeActive)
				na.MissingBlocks = 11 // This is already above maxTrack, but should be capped
				return na, nil
			},
			maxTrack:              10,
			missBlockSignSlashPts: 5,
			expectedMissingBefore: 11,
			expectedMissingAfter:  10, // Should be capped at maxTrack
			expectedSlashPoints:   5,
			expectedErr:           nil,
		},
		{
			name: "node account with low maxTrack value",
			setupNodeAccount: func() (NodeAccount, error) {
				na := GetRandomValidatorNode(NodeActive)
				na.MissingBlocks = 1
				return na, nil
			},
			maxTrack:              3,
			missBlockSignSlashPts: 5,
			expectedMissingBefore: 1,
			expectedMissingAfter:  2,
			expectedSlashPoints:   5,
			expectedErr:           nil,
		},
		{
			name: "node account with high slash points",
			setupNodeAccount: func() (NodeAccount, error) {
				na := GetRandomValidatorNode(NodeActive)
				na.MissingBlocks = 5
				return na, nil
			},
			maxTrack:              10,
			missBlockSignSlashPts: 20,
			expectedMissingBefore: 5,
			expectedMissingAfter:  6,
			expectedSlashPoints:   20,
			expectedErr:           nil,
		},
	}

	for _, tc := range testCases {
		c.Logf("Test case: %s", tc.name)
		na, err := tc.setupNodeAccount()
		c.Assert(err, IsNil)

		keeper := &TestDoubleSlashKeeper{
			na:          na,
			network:     NewNetwork(),
			slashPoints: make(map[string]int64),
			constants:   make(map[string]int64),
		}
		keeper.constants["MissBlockSignSlashPoints"] = tc.missBlockSignSlashPts
		keeper.constants["MaxTrackMissingBlock"] = tc.maxTrack
		slasher := newSlasherVCUR(keeper, NewDummyEventMgr())

		pk, err := cosmos.GetPubKeyFromBech32(cosmos.Bech32PubKeyTypeConsPub, na.ValidatorConsPubKey)
		c.Assert(err, IsNil)

		var pair nodeAddressValidatorAddressPair
		pair.nodeAddress = na.NodeAddress
		pair.validatorAddress = pk.Address()

		c.Assert(keeper.na.MissingBlocks, Equals, tc.expectedMissingBefore)
		err = slasher.HandleMissingSign(ctx, pk.Address(), constAccessor, []nodeAddressValidatorAddressPair{pair})
		c.Assert(err, IsNil)
		c.Assert(keeper.na.MissingBlocks, Equals, tc.expectedMissingAfter)
		c.Assert(keeper.slashPoints[na.NodeAddress.String()], Equals, tc.expectedSlashPoints)
	}
}

func (s *SlashingVCURSuite) TestHandleMissingSignErrors(c *C) {
	ctx, _ := setupKeeperForTest(c)
	constAccessor := constants.GetConstantValues(GetCurrentVersion())

	// Test case: validator address not found
	na := GetRandomValidatorNode(NodeActive)
	keeper := &TestDoubleSlashKeeper{
		na:          na,
		network:     NewNetwork(),
		slashPoints: make(map[string]int64),
		constants:   make(map[string]int64),
	}
	slasher := newSlasherVCUR(keeper, NewDummyEventMgr())

	randomAddr := GetRandomBech32Addr().String()
	err := slasher.HandleMissingSign(ctx, []byte(randomAddr), constAccessor, []nodeAddressValidatorAddressPair{})
	c.Assert(err, NotNil)
	c.Assert(err.Error(), Matches, "could not find active node account with validator address: .*")
}

func (s *SlashingVCURSuite) TestDoubleSign(c *C) {
	ctx, _ := setupKeeperForTest(c)
	constAccessor := constants.GetConstantValues(GetCurrentVersion())

	na := GetRandomValidatorNode(NodeActive)

	keeper := &TestDoubleSlashKeeper{
		na:          na,
		network:     NewNetwork(),
		slashPoints: make(map[string]int64),
		constants:   make(map[string]int64),
	}
	slasher := newSlasherVCUR(keeper, NewDummyEventMgr())

	pk, err := cosmos.GetPubKeyFromBech32(cosmos.Bech32PubKeyTypeConsPub, na.ValidatorConsPubKey)
	c.Assert(err, IsNil)

	var pair nodeAddressValidatorAddressPair
	pair.nodeAddress = na.NodeAddress
	pair.validatorAddress = pk.Address()

	c.Assert(keeper.slashPoints[na.NodeAddress.String()], Equals, int64(0))
	keeper.constants["DoubleBlockSignSlashPoints"] = 1
	err = slasher.HandleDoubleSign(ctx, pk.Address(), 0, constAccessor, []nodeAddressValidatorAddressPair{pair})
	c.Assert(err, IsNil)
	c.Assert(keeper.slashPoints[na.NodeAddress.String()], Equals, int64(1))
}

func (s *SlashingVCURSuite) TestIncreaseDecreaseSlashPoints(c *C) {
	ctx, _ := setupKeeperForTest(c)

	na := GetRandomValidatorNode(NodeActive)
	na.Bond = cosmos.NewUint(100 * common.One)

	keeper := &TestDoubleSlashKeeper{
		na:          na,
		network:     NewNetwork(),
		slashPoints: make(map[string]int64),
	}
	slasher := newSlasherVCUR(keeper, NewDummyEventMgr())
	addr := GetRandomBech32Addr()
	slasher.IncSlashPoints(ctx, 1, addr)
	slasher.DecSlashPoints(ctx, 1, addr)
	c.Assert(keeper.slashPoints[addr.String()], Equals, int64(0))
}

func (s *SlashingVCURSuite) TestSlashVault(c *C) {
	ctx, mgr := setupManagerForTest(c)
	slasher := newSlasherVCUR(mgr.Keeper(), mgr.EventMgr())
	// when coins are empty , it should return nil
	c.Assert(slasher.SlashVault(ctx, GetRandomPubKey(), common.NewCoins(), mgr), IsNil)

	// when vault is not available , it should return an error
	err := slasher.SlashVault(ctx, GetRandomPubKey(), common.NewCoins(common.NewCoin(common.BTCAsset, cosmos.NewUint(common.One))), mgr)
	c.Assert(err, NotNil)
	c.Assert(errors.Is(err, types.ErrVaultNotFound), Equals, true)

	// create a node
	node := GetRandomValidatorNode(NodeActive)
	c.Assert(mgr.Keeper().SetNodeAccount(ctx, node), IsNil)
	FundModule(c, ctx, mgr.Keeper(), BondName, node.Bond.Uint64())
	vault := GetRandomVault()
	vault.Type = AsgardVault
	vault.Status = types2.VaultStatus_ActiveVault
	vault.PubKey = node.PubKeySet.Secp256k1
	vault.Membership = []string{
		node.PubKeySet.Secp256k1.String(),
	}
	vault.Coins = common.NewCoins(
		common.NewCoin(common.BTCAsset, cosmos.NewUint(2*common.One)),
	)
	c.Assert(mgr.Keeper().SetVault(ctx, vault), IsNil)

	pool := NewPool()
	pool.Asset = common.BTCAsset
	pool.BalanceRune = cosmos.NewUint(100 * common.One)
	pool.BalanceAsset = cosmos.NewUint(100 * common.One)
	pool.Status = PoolAvailable

	c.Assert(mgr.Keeper().SetPool(ctx, pool), IsNil)

	asgardBeforeSlash := mgr.Keeper().GetRuneBalanceOfModule(ctx, AsgardName)
	bondBeforeSlash := mgr.Keeper().GetRuneBalanceOfModule(ctx, BondName)
	reserveBeforeSlash := mgr.Keeper().GetRuneBalanceOfModule(ctx, ReserveName)
	poolBeforeSlash := pool.BalanceRune

	err = slasher.SlashVault(ctx, vault.PubKey, common.NewCoins(common.NewCoin(common.BTCAsset, cosmos.NewUint(common.One))), mgr)
	c.Assert(err, IsNil)
	nodeTemp, err := mgr.Keeper().GetNodeAccountByPubKey(ctx, vault.PubKey)
	c.Assert(err, IsNil)
	expectedBond := cosmos.NewUint(99850000000)
	c.Assert(nodeTemp.Bond.Equal(expectedBond), Equals, true, Commentf("%d", nodeTemp.Bond.Uint64()))

	asgardAfterSlash := mgr.Keeper().GetRuneBalanceOfModule(ctx, AsgardName)
	bondAfterSlash := mgr.Keeper().GetRuneBalanceOfModule(ctx, BondName)
	reserveAfterSlash := mgr.Keeper().GetRuneBalanceOfModule(ctx, ReserveName)

	pool, err = mgr.Keeper().GetPool(ctx, pool.Asset)
	c.Assert(err, IsNil)
	poolAfterSlash := pool.BalanceRune

	// ensure pool's change is in sync with asgard's change
	c.Assert(asgardAfterSlash.Sub(asgardBeforeSlash).Uint64(), Equals, poolAfterSlash.Sub(poolBeforeSlash).Uint64(), Commentf("%d", "pool/asgard rune mismatch"))

	c.Check(asgardAfterSlash.Sub(asgardBeforeSlash).Uint64(), Equals, uint64(100000000), Commentf("%d", asgardAfterSlash.Sub(asgardBeforeSlash).Uint64()))
	c.Check(asgardAfterSlash.Sub(asgardBeforeSlash).Uint64(), Equals, uint64(100000000), Commentf("%d", asgardAfterSlash.Sub(asgardBeforeSlash).Uint64()))
	c.Check(bondBeforeSlash.Sub(bondAfterSlash).Uint64(), Equals, uint64(150000000), Commentf("%d", bondBeforeSlash.Sub(bondAfterSlash).Uint64()))
	c.Check(reserveAfterSlash.Sub(reserveBeforeSlash).Uint64(), Equals, uint64(50000000), Commentf("%d", reserveAfterSlash.Sub(reserveBeforeSlash).Uint64()))

	// add one more node , slash asgard
	node1 := GetRandomValidatorNode(NodeActive)
	c.Assert(mgr.Keeper().SetNodeAccount(ctx, node1), IsNil)
	FundModule(c, ctx, mgr.Keeper(), BondName, node1.Bond.Uint64())

	vault1 := GetRandomVault()
	vault1.Type = AsgardVault
	vault1.Status = types2.VaultStatus_ActiveVault
	vault1.PubKey = GetRandomPubKey()
	vault1.Membership = []string{
		node.PubKeySet.Secp256k1.String(),
		node1.PubKeySet.Secp256k1.String(),
	}
	vault1.Coins = common.NewCoins(
		common.NewCoin(common.BTCAsset, cosmos.NewUint(2*common.One)),
	)
	c.Assert(mgr.Keeper().SetVault(ctx, vault1), IsNil)
	nodeBeforeSlash, err := mgr.Keeper().GetNodeAccount(ctx, node.NodeAddress)
	c.Assert(err, IsNil)
	nodeBondBeforeSlash := nodeBeforeSlash.Bond
	node1BondBeforeSlash := node1.Bond
	mgr.Keeper().SetMimir(ctx, "PauseOnSlashThreshold", 1)
	err = slasher.SlashVault(ctx, vault1.PubKey, common.NewCoins(common.NewCoin(common.BTCAsset, cosmos.NewUint(common.One))), mgr)
	c.Assert(err, IsNil)

	nodeAfterSlash, err := mgr.Keeper().GetNodeAccount(ctx, node.NodeAddress)
	c.Assert(err, IsNil)
	node1AfterSlash, err := mgr.Keeper().GetNodeAccount(ctx, node1.NodeAddress)
	c.Assert(err, IsNil)
	nodeBondAfterSlash := nodeAfterSlash.Bond
	node1BondAfterSlash := node1AfterSlash.Bond

	c.Check(nodeBondBeforeSlash.Sub(nodeBondAfterSlash).Uint64(), Equals, uint64(76457722), Commentf("%d", nodeBondBeforeSlash.Sub(nodeBondAfterSlash).Uint64()))
	c.Check(node1BondBeforeSlash.Sub(node1BondAfterSlash).Uint64(), Equals, uint64(76572581), Commentf("%d", node1BondBeforeSlash.Sub(node1BondAfterSlash).Uint64()))

	val, err := mgr.Keeper().GetMimir(ctx, "HaltBTCChain")
	c.Assert(err, IsNil)
	c.Assert(val, Equals, int64(18), Commentf("%d", val))
}

func (s *SlashingVCURSuite) TestUpdatePoolFromSlash(c *C) {
	ctx, mgr := setupManagerForTest(c)
	slasher := newSlasherVCUR(mgr.Keeper(), mgr.EventMgr())

	pool := NewPool()
	pool.Asset = common.BTCAsset
	pool.BalanceRune = cosmos.NewUint(1000 * common.One)
	pool.BalanceAsset = cosmos.NewUint(1000 * common.One)
	pool.Status = PoolAvailable
	c.Assert(mgr.Keeper().SetPool(ctx, pool), IsNil)

	deductAsset := cosmos.NewUint(250 * common.One)
	creditRune := cosmos.NewUint(500 * common.One)
	stolenAsset := common.NewCoin(common.BTCAsset, deductAsset)
	slasher.updatePoolFromSlash(ctx, pool, stolenAsset, creditRune, mgr)

	pool, err := mgr.Keeper().GetPool(ctx, common.BTCAsset)
	c.Assert(err, IsNil)
	c.Assert(pool.BalanceAsset.Uint64(), Equals, uint64(750*common.One))
	c.Assert(pool.BalanceRune.Uint64(), Equals, uint64(1500*common.One))
}

func (s *SlashingVCURSuite) TestNetworkShouldNotSlashMorethanVaultAmount(c *C) {
	ctx, mgr := setupManagerForTest(c)
	slasher := newSlasherVCUR(mgr.Keeper(), mgr.EventMgr())

	// create a node
	node := GetRandomValidatorNode(NodeActive)
	c.Assert(mgr.Keeper().SetNodeAccount(ctx, node), IsNil)
	FundModule(c, ctx, mgr.Keeper(), BondName, node.Bond.Uint64())
	vault := GetRandomVault()
	vault.Type = AsgardVault
	vault.Status = types2.VaultStatus_ActiveVault
	vault.PubKey = node.PubKeySet.Secp256k1
	vault.Membership = []string{
		node.PubKeySet.Secp256k1.String(),
	}
	vault.Coins = common.NewCoins(
		common.NewCoin(common.BTCAsset, cosmos.NewUint(common.One/2)),
	)
	c.Assert(mgr.Keeper().SetVault(ctx, vault), IsNil)

	pool := NewPool()
	pool.Asset = common.BTCAsset
	pool.BalanceRune = cosmos.NewUint(100 * common.One)
	pool.BalanceAsset = cosmos.NewUint(100 * common.One)
	pool.Status = PoolAvailable

	c.Assert(mgr.Keeper().SetPool(ctx, pool), IsNil)

	asgardBeforeSlash := mgr.Keeper().GetRuneBalanceOfModule(ctx, AsgardName)
	bondBeforeSlash := mgr.Keeper().GetRuneBalanceOfModule(ctx, BondName)
	reserveBeforeSlash := mgr.Keeper().GetRuneBalanceOfModule(ctx, ReserveName)
	poolBeforeSlash := pool.BalanceRune

	// vault only has 0.5 BTC , however the outbound is 1 BTC , make sure we don't over slash the vault
	err := slasher.SlashVault(ctx, vault.PubKey, common.NewCoins(common.NewCoin(common.BTCAsset, cosmos.NewUint(common.One))), mgr)
	c.Assert(err, IsNil)
	nodeTemp, err := mgr.Keeper().GetNodeAccountByPubKey(ctx, vault.PubKey)
	c.Assert(err, IsNil)
	expectedBond := cosmos.NewUint(99925000000)
	c.Assert(nodeTemp.Bond.Equal(expectedBond), Equals, true, Commentf("%d", nodeTemp.Bond.Uint64()))

	asgardAfterSlash := mgr.Keeper().GetRuneBalanceOfModule(ctx, AsgardName)
	bondAfterSlash := mgr.Keeper().GetRuneBalanceOfModule(ctx, BondName)
	reserveAfterSlash := mgr.Keeper().GetRuneBalanceOfModule(ctx, ReserveName)

	pool, err = mgr.Keeper().GetPool(ctx, pool.Asset)
	c.Assert(err, IsNil)
	poolAfterSlash := pool.BalanceRune

	// ensure pool's change is in sync with asgard's change
	c.Assert(asgardAfterSlash.Sub(asgardBeforeSlash).Uint64(), Equals, poolAfterSlash.Sub(poolBeforeSlash).Uint64(), Commentf("%d", "pool/asgard rune mismatch"))

	c.Check(asgardAfterSlash.Sub(asgardBeforeSlash).Uint64(), Equals, uint64(50000000), Commentf("%d", asgardAfterSlash.Sub(asgardBeforeSlash).Uint64()))
	c.Check(bondBeforeSlash.Sub(bondAfterSlash).Uint64(), Equals, uint64(75000000), Commentf("%d", bondBeforeSlash.Sub(bondAfterSlash).Uint64()))
	c.Check(reserveAfterSlash.Sub(reserveBeforeSlash).Uint64(), Equals, uint64(25000000), Commentf("%d", reserveAfterSlash.Sub(reserveBeforeSlash).Uint64()))

	// add one more node , slash asgard
	node1 := GetRandomValidatorNode(NodeActive)
	c.Assert(mgr.Keeper().SetNodeAccount(ctx, node1), IsNil)
	FundModule(c, ctx, mgr.Keeper(), BondName, node1.Bond.Uint64())

	vault1 := GetRandomVault()
	vault1.Type = AsgardVault
	vault1.Status = types2.VaultStatus_ActiveVault
	vault1.PubKey = GetRandomPubKey()
	vault1.Membership = []string{
		node.PubKeySet.Secp256k1.String(),
		node1.PubKeySet.Secp256k1.String(),
	}
	vault1.Coins = common.NewCoins(
		common.NewCoin(common.BTCAsset, cosmos.NewUint(common.One/2)),
	)
	c.Assert(mgr.Keeper().SetVault(ctx, vault1), IsNil)
	nodeBeforeSlash, err := mgr.Keeper().GetNodeAccount(ctx, node.NodeAddress)
	c.Assert(err, IsNil)
	nodeBondBeforeSlash := nodeBeforeSlash.Bond
	node1BondBeforeSlash := node1.Bond
	mgr.Keeper().SetMimir(ctx, "PauseOnSlashThreshold", 1)
	err = slasher.SlashVault(ctx, vault1.PubKey, common.NewCoins(common.NewCoin(common.BTCAsset, cosmos.NewUint(common.One))), mgr)
	c.Assert(err, IsNil)

	nodeAfterSlash, err := mgr.Keeper().GetNodeAccount(ctx, node.NodeAddress)
	c.Assert(err, IsNil)
	node1AfterSlash, err := mgr.Keeper().GetNodeAccount(ctx, node1.NodeAddress)
	c.Assert(err, IsNil)
	nodeBondAfterSlash := nodeAfterSlash.Bond
	node1BondAfterSlash := node1AfterSlash.Bond

	c.Check(nodeBondBeforeSlash.Sub(nodeBondAfterSlash).Uint64(), Equals, uint64(37862675), Commentf("%d", nodeBondBeforeSlash.Sub(nodeBondAfterSlash).Uint64()))
	c.Check(node1BondBeforeSlash.Sub(node1BondAfterSlash).Uint64(), Equals, uint64(37891094), Commentf("%d", node1BondBeforeSlash.Sub(node1BondAfterSlash).Uint64()))

	val, err := mgr.Keeper().GetMimir(ctx, "HaltBTCChain")
	c.Assert(err, IsNil)
	c.Assert(val, Equals, int64(18), Commentf("%d", val))

	// Attempt to slash more than node has, pool should only be deducted what was successfully slashed
	pool.BalanceRune = cosmos.NewUint(4000 * common.One)
	pool.BalanceAsset = cosmos.NewUint(4000 * common.One)
	c.Assert(mgr.Keeper().SetPool(ctx, pool), IsNil)

	node2 := GetRandomValidatorNode(NodeActive)
	c.Assert(mgr.Keeper().SetNodeAccount(ctx, node2), IsNil)
	FundModule(c, ctx, mgr.Keeper(), BondName, node2.Bond.Uint64())

	vault = GetRandomVault()
	vault.Status = types2.VaultStatus_ActiveVault
	vault.PubKey = node.PubKeySet.Secp256k1
	vault.Membership = []string{
		node2.PubKeySet.Secp256k1.String(),
	}
	vault.Coins = common.NewCoins(
		common.NewCoin(common.BTCAsset, cosmos.NewUint(4000*common.One)),
	)
	c.Assert(mgr.Keeper().SetVault(ctx, vault), IsNil)

	err = slasher.SlashVault(ctx, vault.PubKey, common.NewCoins(common.NewCoin(common.BTCAsset, cosmos.NewUint(2000*common.One))), mgr)
	c.Assert(err, IsNil)
	updatedPool, err := mgr.Keeper().GetPool(ctx, common.BTCAsset)
	c.Assert(err, IsNil)

	// Even though the total rune value to slash is 3000, the node only has 1000 RUNE bonded, so only slash and credit that much to the pool's rune side
	c.Assert(updatedPool.BalanceRune.Uint64(), Equals, cosmos.NewUint(5000*common.One).Uint64())
	// But deduct full stolen amount from asset side
	c.Assert(updatedPool.BalanceAsset.Uint64(), Equals, cosmos.NewUint(2000*common.One).Uint64())
}

func (s *SlashingVCURSuite) TestNeedsNewVault(c *C) {
	ctx, mgr := setupManagerForTest(c)

	inhash := GetRandomTxHash()
	outhash := GetRandomTxHash()
	pk1 := GetRandomPubKey()
	pk2 := GetRandomPubKey()
	pk3 := GetRandomPubKey()
	sig1, _ := pk1.GetThorAddress()
	sig2, _ := pk2.GetThorAddress()
	sig3, _ := pk3.GetThorAddress()
	pk := GetRandomPubKey()
	tx := GetRandomTx()
	tx.ID = outhash
	obs := NewObservedTx(tx, 0, pk, 0)
	obs.ObservedPubKey = pk
	obs.Signers = []string{sig1.String(), sig2.String(), sig3.String()}

	vault := GetRandomVault()
	vault.Membership = []string{pk1.String(), pk2.String(), pk3.String()}

	slasher := newSlasherVCUR(mgr.Keeper(), mgr.EventMgr())

	c.Assert(len(tx.Coins), Equals, 1)
	toi := TxOutItem{
		InHash:      inhash,
		Coin:        tx.Coins[0],
		VaultPubKey: pk,
	}

	c.Check(slasher.needsNewVault(ctx, mgr, vault, 300, 1, toi), Equals, true)

	voter := NewObservedTxVoter(outhash, []common.ObservedTx{obs})
	mgr.Keeper().SetObservedTxOutVoter(ctx, voter)

	mgr.Keeper().SetObservedLink(ctx, inhash, outhash)

	c.Check(slasher.needsNewVault(ctx, mgr, vault, 300, 1, toi), Equals, false)
	ctx = ctx.WithBlockHeight(600)
	c.Check(slasher.needsNewVault(ctx, mgr, vault, 300, 1, toi), Equals, false)
	ctx = ctx.WithBlockHeight(900)
	c.Check(slasher.needsNewVault(ctx, mgr, vault, 300, 1, toi), Equals, false)

	// test that more than 2/3rd will always return false
	ctx = ctx.WithBlockHeight(999999999)
	c.Check(slasher.needsNewVault(ctx, mgr, vault, 300, 1, toi), Equals, false)
}
