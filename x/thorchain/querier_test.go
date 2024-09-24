package thorchain

import (
	"encoding/json"
	"strconv"

	"github.com/blang/semver"

	abci "github.com/tendermint/tendermint/abci/types"
	. "gopkg.in/check.v1"

	"github.com/cosmos/cosmos-sdk/crypto/hd"
	ckeys "github.com/cosmos/cosmos-sdk/crypto/keyring"
	types2 "github.com/cosmos/cosmos-sdk/types"

	"gitlab.com/thorchain/thornode/cmd"
	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/common/cosmos"
	openapi "gitlab.com/thorchain/thornode/openapi/gen"
	"gitlab.com/thorchain/thornode/x/thorchain/keeper"
	"gitlab.com/thorchain/thornode/x/thorchain/query"
	"gitlab.com/thorchain/thornode/x/thorchain/types"
)

type QuerierSuite struct {
	kb      cosmos.KeybaseStore
	mgr     *Mgrs
	k       keeper.Keeper
	querier cosmos.Querier
	ctx     cosmos.Context
}

var _ = Suite(&QuerierSuite{})

type TestQuerierKeeper struct {
	keeper.KVStoreDummy
	txOut *TxOut
}

func (k *TestQuerierKeeper) GetTxOut(_ cosmos.Context, _ int64) (*TxOut, error) {
	return k.txOut, nil
}

func (s *QuerierSuite) SetUpTest(c *C) {
	kb := ckeys.NewInMemory()
	username := "thorchain"
	password := "password"

	_, _, err := kb.NewMnemonic(username, ckeys.English, cmd.THORChainHDPath, password, hd.Secp256k1)
	c.Assert(err, IsNil)
	s.kb = cosmos.KeybaseStore{
		SignerName:   username,
		SignerPasswd: password,
		Keybase:      kb,
	}
	s.ctx, s.mgr = setupManagerForTest(c)
	s.k = s.mgr.Keeper()
	s.querier = NewQuerier(s.mgr, s.kb)
}

func (s *QuerierSuite) TestQueryKeysign(c *C) {
	ctx, _ := setupKeeperForTest(c)
	ctx = ctx.WithBlockHeight(12)

	pk := GetRandomPubKey()
	toAddr := GetRandomETHAddress()
	txOut := NewTxOut(1)
	txOutItem := TxOutItem{
		Chain:       common.ETHChain,
		VaultPubKey: pk,
		ToAddress:   toAddr,
		InHash:      GetRandomTxHash(),
		Coin:        common.NewCoin(common.ETHAsset, cosmos.NewUint(100*common.One)),
	}
	txOut.TxArray = append(txOut.TxArray, txOutItem)
	keeper := &TestQuerierKeeper{
		txOut: txOut,
	}

	_, mgr := setupManagerForTest(c)
	mgr.K = keeper
	querier := NewQuerier(mgr, s.kb)

	path := []string{
		"keysign",
		"5",
		pk.String(),
	}
	res, err := querier(ctx, path, abci.RequestQuery{})
	c.Assert(err, IsNil)
	c.Assert(res, NotNil)
}

func (s *QuerierSuite) TestQueryPool(c *C) {
	ctx, mgr := setupManagerForTest(c)
	querier := NewQuerier(mgr, s.kb)
	path := []string{"pools"}

	pubKey := GetRandomPubKey()
	asgard := NewVault(ctx.BlockHeight(), ActiveVault, AsgardVault, pubKey, common.Chains{common.ETHChain}.Strings(), []ChainContract{})
	c.Assert(mgr.Keeper().SetVault(ctx, asgard), IsNil)

	poolETH := NewPool()
	poolETH.Asset = common.ETHAsset
	poolETH.LPUnits = cosmos.NewUint(100)

	poolBTC := NewPool()
	poolBTC.Asset = common.BTCAsset
	poolBTC.LPUnits = cosmos.NewUint(0)

	err := mgr.Keeper().SetPool(ctx, poolETH)
	c.Assert(err, IsNil)

	err = mgr.Keeper().SetPool(ctx, poolBTC)
	c.Assert(err, IsNil)

	res, err := querier(ctx, path, abci.RequestQuery{})
	c.Assert(err, IsNil)

	var out Pools

	err = json.Unmarshal(res, &out)
	c.Assert(err, IsNil)
	c.Assert(len(out), Equals, 1)

	poolBTC.LPUnits = cosmos.NewUint(100)
	err = mgr.Keeper().SetPool(ctx, poolBTC)
	c.Assert(err, IsNil)

	res, err = querier(ctx, path, abci.RequestQuery{})
	c.Assert(err, IsNil)

	err = json.Unmarshal(res, &out)
	c.Assert(err, IsNil)
	c.Assert(len(out), Equals, 2)

	result, err := s.querier(s.ctx, []string{query.QueryPool.Key, "ETH.ETH"}, abci.RequestQuery{})
	c.Assert(result, HasLen, 0)
	c.Assert(err, NotNil)
}

func (s *QuerierSuite) TestVaultss(c *C) {
	ctx, mgr := setupManagerForTest(c)
	querier := NewQuerier(mgr, s.kb)
	path := []string{"pools"}

	pubKey := GetRandomPubKey()
	asgard := NewVault(ctx.BlockHeight(), ActiveVault, AsgardVault, pubKey, common.Chains{common.ETHChain}.Strings(), nil)
	c.Assert(mgr.Keeper().SetVault(ctx, asgard), IsNil)

	poolETH := NewPool()
	poolETH.Asset = common.ETHAsset
	poolETH.LPUnits = cosmos.NewUint(100)

	poolBTC := NewPool()
	poolBTC.Asset = common.BTCAsset
	poolBTC.LPUnits = cosmos.NewUint(0)

	err := mgr.Keeper().SetPool(ctx, poolETH)
	c.Assert(err, IsNil)

	err = mgr.Keeper().SetPool(ctx, poolBTC)
	c.Assert(err, IsNil)

	res, err := querier(ctx, path, abci.RequestQuery{})
	c.Assert(err, IsNil)

	var out Pools
	err = json.Unmarshal(res, &out)
	c.Assert(err, IsNil)
	c.Assert(len(out), Equals, 1)

	poolBTC.LPUnits = cosmos.NewUint(100)
	err = mgr.Keeper().SetPool(ctx, poolBTC)
	c.Assert(err, IsNil)

	res, err = querier(ctx, path, abci.RequestQuery{})
	c.Assert(err, IsNil)

	err = json.Unmarshal(res, &out)
	c.Assert(err, IsNil)
	c.Assert(len(out), Equals, 2)

	result, err := s.querier(s.ctx, []string{query.QueryPool.Key, "ETH.ETH"}, abci.RequestQuery{})
	c.Assert(result, HasLen, 0)
	c.Assert(err, NotNil)
}

func (s *QuerierSuite) TestSaverPools(c *C) {
	ctx, mgr := setupManagerForTest(c)
	querier := NewQuerier(mgr, s.kb)
	path := []string{"pools"}

	poolDOGE := NewPool()
	poolDOGE.Asset = common.DOGEAsset.GetSyntheticAsset()
	poolDOGE.LPUnits = cosmos.NewUint(100)

	poolBTC := NewPool()
	poolBTC.Asset = common.BTCAsset
	poolBTC.LPUnits = cosmos.NewUint(1000)

	poolETH := NewPool()
	poolETH.Asset = common.ETHAsset.GetSyntheticAsset()
	poolETH.LPUnits = cosmos.NewUint(100)

	err := mgr.Keeper().SetPool(ctx, poolDOGE)
	c.Assert(err, IsNil)

	err = mgr.Keeper().SetPool(ctx, poolBTC)
	c.Assert(err, IsNil)

	err = mgr.Keeper().SetPool(ctx, poolETH)
	c.Assert(err, IsNil)

	res, err := querier(ctx, path, abci.RequestQuery{})
	c.Assert(err, IsNil)

	var out []openapi.Pool
	err = json.Unmarshal(res, &out)
	c.Assert(err, IsNil)
	c.Assert(len(out), Equals, 1)
}

func (s *QuerierSuite) TestQueryNodeAccounts(c *C) {
	ctx, keeper := setupKeeperForTest(c)

	_, mgr := setupManagerForTest(c)
	querier := NewQuerier(mgr, s.kb)
	path := []string{"nodes"}

	nodeAccount := GetRandomValidatorNode(NodeActive)
	c.Assert(keeper.SetNodeAccount(ctx, nodeAccount), IsNil)
	vault := GetRandomVault()
	vault.Status = ActiveVault
	vault.BlockHeight = 1
	c.Assert(keeper.SetVault(ctx, vault), IsNil)
	res, err := querier(ctx, path, abci.RequestQuery{})
	c.Assert(err, IsNil)

	var out types.NodeAccounts
	err1 := json.Unmarshal(res, &out)
	c.Assert(err1, IsNil)
	c.Assert(len(out), Equals, 1)

	nodeAccount2 := GetRandomValidatorNode(NodeActive)
	nodeAccount2.Bond = cosmos.NewUint(common.One * 3000)
	c.Assert(keeper.SetNodeAccount(ctx, nodeAccount2), IsNil)

	/* Check Bond-weighted rewards estimation works*/
	var nodeAccountResp []openapi.Node

	// Add bond rewards + set min bond for bond-weighted system
	network, _ := keeper.GetNetwork(ctx)
	network.BondRewardRune = cosmos.NewUint(common.One * 1000)
	c.Assert(keeper.SetNetwork(ctx, network), IsNil)
	keeper.SetMimir(ctx, "MinimumBondInRune", common.One*1000)

	res, err = querier(ctx, path, abci.RequestQuery{})
	c.Assert(err, IsNil)

	err1 = json.Unmarshal(res, &nodeAccountResp)
	c.Assert(err1, IsNil)
	c.Assert(len(nodeAccountResp), Equals, 2)

	for _, node := range nodeAccountResp {
		if node.NodeAddress == nodeAccount.NodeAddress.String() {
			// First node has 25% of total bond, gets 25% of rewards
			c.Assert(node.CurrentAward, Equals, cosmos.NewUint(common.One*250).String())
			continue
		} else if node.NodeAddress == nodeAccount2.NodeAddress.String() {
			// Second node has 75% of total bond, gets 75% of rewards
			c.Assert(node.CurrentAward, Equals, cosmos.NewUint(common.One*750).String())
			continue
		}

		c.Fail()
	}

	/* Check querier only returns nodes with bond */
	nodeAccount2.Bond = cosmos.NewUint(0)
	c.Assert(keeper.SetNodeAccount(ctx, nodeAccount2), IsNil)

	res, err = querier(ctx, path, abci.RequestQuery{})
	c.Assert(err, IsNil)

	err1 = json.Unmarshal(res, &out)
	c.Assert(err1, IsNil)
	c.Assert(len(out), Equals, 1)
}

func (s *QuerierSuite) TestQueryUpgradeProposals(c *C) {
	ctx, mgr := setupManagerForTest(c)
	querier := NewQuerier(mgr, s.kb)

	k := mgr.Keeper()

	// Add node accounts
	na1 := GetRandomValidatorNode(NodeActive)
	na1.Bond = cosmos.NewUint(100 * common.One)
	c.Assert(k.SetNodeAccount(ctx, na1), IsNil)
	na2 := GetRandomValidatorNode(NodeActive)
	na2.Bond = cosmos.NewUint(200 * common.One)
	c.Assert(k.SetNodeAccount(ctx, na2), IsNil)
	na3 := GetRandomValidatorNode(NodeActive)
	na3.Bond = cosmos.NewUint(300 * common.One)
	c.Assert(k.SetNodeAccount(ctx, na3), IsNil)
	na4 := GetRandomValidatorNode(NodeActive)
	na4.Bond = cosmos.NewUint(400 * common.One)
	c.Assert(k.SetNodeAccount(ctx, na4), IsNil)
	na5 := GetRandomValidatorNode(NodeActive)
	na5.Bond = cosmos.NewUint(500 * common.One)
	c.Assert(k.SetNodeAccount(ctx, na5), IsNil)
	na6 := GetRandomValidatorNode(NodeActive)
	na6.Bond = cosmos.NewUint(600 * common.One)
	c.Assert(k.SetNodeAccount(ctx, na6), IsNil)

	const (
		upgradeName = "1.2.3"
		upgradeInfo = "scheduled upgrade"
	)

	upgradeHeight := ctx.BlockHeight() + 100

	// propose upgrade
	c.Assert(k.ProposeUpgrade(ctx, upgradeName, types.Upgrade{
		Height: upgradeHeight,
		Info:   upgradeInfo,
	}), IsNil)

	k.ApproveUpgrade(ctx, na1.NodeAddress, upgradeName)
	k.ApproveUpgrade(ctx, na2.NodeAddress, upgradeName)
	k.ApproveUpgrade(ctx, na3.NodeAddress, upgradeName)

	res, err := querier(ctx, []string{query.QueryUpgradeProposals.Key}, abci.RequestQuery{})
	c.Assert(err, IsNil)
	var proposals []openapi.UpgradeProposal

	err = json.Unmarshal(res, &proposals)
	c.Assert(err, IsNil)

	c.Assert(len(proposals), Equals, 1)
	p := proposals[0]
	c.Assert(p.Name, Equals, upgradeName)
	c.Assert(p.Info, Equals, upgradeInfo)
	c.Assert(p.Height, Equals, upgradeHeight)

	res, err = querier(ctx, []string{query.QueryUpgradeProposal.Key, upgradeName}, abci.RequestQuery{})
	c.Assert(err, IsNil)
	err = json.Unmarshal(res, &p)
	c.Assert(err, IsNil)

	c.Assert(p.Name, Equals, upgradeName)
	c.Assert(p.Info, Equals, upgradeInfo)
	c.Assert(p.Height, Equals, upgradeHeight)
	c.Assert(*p.Approved, Equals, false)
	c.Assert(*p.ValidatorsToQuorum, Equals, int64(1))
	c.Assert(*p.ApprovedPercent, Equals, "0.5")

	k.ApproveUpgrade(ctx, na4.NodeAddress, upgradeName)

	res, err = querier(ctx, []string{query.QueryUpgradeProposal.Key, upgradeName}, abci.RequestQuery{})
	c.Assert(err, IsNil)
	err = json.Unmarshal(res, &p)
	c.Assert(err, IsNil)

	c.Assert(*p.Approved, Equals, true)
	c.Assert(*p.ValidatorsToQuorum, Equals, int64(0))
	c.Assert(*p.ApprovedPercent, Equals, "0.6666666666666666")

	k.RejectUpgrade(ctx, na2.NodeAddress, upgradeName)

	res, err = querier(ctx, []string{query.QueryUpgradeProposal.Key, upgradeName}, abci.RequestQuery{})
	c.Assert(err, IsNil)
	err = json.Unmarshal(res, &p)
	c.Assert(err, IsNil)

	c.Assert(*p.Approved, Equals, false)
	c.Assert(*p.ValidatorsToQuorum, Equals, int64(1))
	c.Assert(*p.ApprovedPercent, Equals, "0.5")

	var votes []openapi.UpgradeVote
	res, err = querier(ctx, []string{query.QueryUpgradeVotes.Key, upgradeName}, abci.RequestQuery{})
	c.Assert(err, IsNil)
	err = json.Unmarshal(res, &votes)
	c.Assert(err, IsNil)
	c.Assert(len(votes), Equals, 4)

	foundVote := make(map[string]bool)
	for _, v := range votes {
		if _, ok := foundVote[v.NodeAddress]; ok {
			c.Log("duplicate vote", v.NodeAddress)
			c.Fail()
		}
		foundVote[v.NodeAddress] = true
		switch v.NodeAddress {
		case na1.NodeAddress.String():
			c.Assert(v.Vote, Equals, "approve")
		case na2.NodeAddress.String():
			c.Assert(v.Vote, Equals, "reject")
		case na3.NodeAddress.String():
			c.Assert(v.Vote, Equals, "approve")
		case na4.NodeAddress.String():
			c.Assert(v.Vote, Equals, "approve")
		case na5.NodeAddress.String():
			c.Assert(v.Vote, Equals, "approve")
		default:
			c.Log("unexpected voter address", v.NodeAddress)
			c.Fail()
		}
	}
}

func (s *QuerierSuite) TestQuerierRagnarokInProgress(c *C) {
	req := abci.RequestQuery{
		Data:   nil,
		Path:   query.QueryRagnarok.Key,
		Height: s.ctx.BlockHeight(),
		Prove:  false,
	}
	// test ragnarok
	result, err := s.querier(s.ctx, []string{query.QueryRagnarok.Key}, req)
	c.Assert(result, NotNil)
	c.Assert(err, IsNil)
	var ragnarok bool
	c.Assert(json.Unmarshal(result, &ragnarok), IsNil)
	c.Assert(ragnarok, Equals, false)
}

func (s *QuerierSuite) TestQueryLiquidityProviders(c *C) {
	req := abci.RequestQuery{
		Data:   nil,
		Path:   query.QueryLiquidityProviders.Key,
		Height: s.ctx.BlockHeight(),
		Prove:  false,
	}
	// test liquidity providers
	result, err := s.querier(s.ctx, []string{query.QueryLiquidityProviders.Key, "ETH.ETH"}, req)
	c.Assert(result, NotNil)
	c.Assert(err, IsNil)
	s.k.SetLiquidityProvider(s.ctx, LiquidityProvider{
		Asset:              common.ETHAsset,
		RuneAddress:        GetRandomETHAddress(),
		AssetAddress:       GetRandomETHAddress(),
		LastAddHeight:      1024,
		LastWithdrawHeight: 0,
		Units:              cosmos.NewUint(10),
	})
	result, err = s.querier(s.ctx, []string{query.QueryLiquidityProviders.Key, "ETH.ETH"}, req)
	c.Assert(err, IsNil)
	var lps LiquidityProviders
	c.Assert(json.Unmarshal(result, &lps), IsNil)
	c.Assert(lps, HasLen, 1)

	req = abci.RequestQuery{
		Data:   nil,
		Path:   query.QuerySavers.Key,
		Height: s.ctx.BlockHeight(),
		Prove:  false,
	}

	s.k.SetLiquidityProvider(s.ctx, LiquidityProvider{
		Asset:              common.ETHAsset.GetSyntheticAsset(),
		RuneAddress:        GetRandomETHAddress(),
		AssetAddress:       GetRandomRUNEAddress(),
		LastAddHeight:      1024,
		LastWithdrawHeight: 0,
		Units:              cosmos.NewUint(10),
	})

	// Query Savers from SaversPool
	result, err = s.querier(s.ctx, []string{query.QuerySavers.Key, "ETH.ETH"}, req)
	c.Assert(err, IsNil)
	var savers LiquidityProviders
	c.Assert(json.Unmarshal(result, &savers), IsNil)
	c.Assert(lps, HasLen, 1)
}

func (s *QuerierSuite) TestQueryTxInVoter(c *C) {
	req := abci.RequestQuery{
		Data:   nil,
		Path:   query.QueryTxVoter.Key,
		Height: s.ctx.BlockHeight(),
		Prove:  false,
	}
	tx := GetRandomTx()
	// test getTxInVoter
	result, err := s.querier(s.ctx, []string{query.QueryTxVoter.Key, tx.ID.String()}, req)
	c.Assert(result, IsNil)
	c.Assert(err, NotNil)
	observedTxInVote := NewObservedTxVoter(tx.ID, []ObservedTx{NewObservedTx(tx, s.ctx.BlockHeight(), GetRandomPubKey(), s.ctx.BlockHeight())})
	s.k.SetObservedTxInVoter(s.ctx, observedTxInVote)
	result, err = s.querier(s.ctx, []string{query.QueryTxVoter.Key, tx.ID.String()}, req)
	c.Assert(err, IsNil)
	c.Assert(result, NotNil)
	var voter openapi.TxDetailsResponse
	c.Assert(json.Unmarshal(result, &voter), IsNil)

	// common.Tx Valid cannot be used for openapi.Tx, so checking some criteria individually.
	c.Assert(voter.TxId == nil, Equals, false)
	c.Assert(len(voter.Txs) == 1, Equals, true)
	c.Assert(voter.Txs[0].ExternalObservedHeight == nil, Equals, false)
	c.Assert(*voter.Txs[0].ExternalObservedHeight <= 0, Equals, false)
	c.Assert(voter.Txs[0].ObservedPubKey == nil, Equals, false)
	c.Assert(voter.Txs[0].ExternalConfirmationDelayHeight == nil, Equals, false)
	c.Assert(*voter.Txs[0].ExternalConfirmationDelayHeight <= 0, Equals, false)
	c.Assert(voter.Txs[0].Tx.Id == nil, Equals, false)
	c.Assert(voter.Txs[0].Tx.FromAddress == nil, Equals, false)
	c.Assert(voter.Txs[0].Tx.ToAddress == nil, Equals, false)
	c.Assert(voter.Txs[0].Tx.Chain == nil, Equals, false)
	c.Assert(len(voter.Txs[0].Tx.Coins) == 0, Equals, false)
}

func (s *QuerierSuite) TestQueryTxStages(c *C) {
	req := abci.RequestQuery{
		Data:   nil,
		Path:   query.QueryTxStages.Key,
		Height: s.ctx.BlockHeight(),
		Prove:  false,
	}
	tx := GetRandomTx()
	// test getTxInVoter
	result, err := s.querier(s.ctx, []string{query.QueryTxStages.Key, tx.ID.String()}, req)
	c.Assert(result, NotNil) // Expecting a not-started Observation stage.
	c.Assert(err, IsNil)     // Expecting no error for an unobserved hash.
	observedTxInVote := NewObservedTxVoter(tx.ID, []ObservedTx{NewObservedTx(tx, s.ctx.BlockHeight(), GetRandomPubKey(), s.ctx.BlockHeight())})
	s.k.SetObservedTxInVoter(s.ctx, observedTxInVote)
	result, err = s.querier(s.ctx, []string{query.QueryTxStages.Key, tx.ID.String()}, req)
	c.Assert(err, IsNil)
	c.Assert(result, NotNil)
}

func (s *QuerierSuite) TestQueryTxStatus(c *C) {
	req := abci.RequestQuery{
		Data:   nil,
		Path:   query.QueryTxStatus.Key,
		Height: s.ctx.BlockHeight(),
		Prove:  false,
	}
	tx := GetRandomTx()
	// test getTxInVoter
	result, err := s.querier(s.ctx, []string{query.QueryTxStatus.Key, tx.ID.String()}, req)
	c.Assert(result, NotNil) // Expecting a not-started Observation stage.
	c.Assert(err, IsNil)     // Expecting no error for an unobserved hash.
	observedTxInVote := NewObservedTxVoter(tx.ID, []ObservedTx{NewObservedTx(tx, s.ctx.BlockHeight(), GetRandomPubKey(), s.ctx.BlockHeight())})
	s.k.SetObservedTxInVoter(s.ctx, observedTxInVote)
	result, err = s.querier(s.ctx, []string{query.QueryTxStatus.Key, tx.ID.String()}, req)
	c.Assert(err, IsNil)
	c.Assert(result, NotNil)
}

func (s *QuerierSuite) TestQueryTx(c *C) {
	req := abci.RequestQuery{
		Data:   nil,
		Path:   query.QueryTx.Key,
		Height: s.ctx.BlockHeight(),
		Prove:  false,
	}
	tx := GetRandomTx()
	// test get tx in
	result, err := s.querier(s.ctx, []string{query.QueryTx.Key, tx.ID.String()}, req)
	c.Assert(result, IsNil)
	c.Assert(err, NotNil)
	nodeAccount := GetRandomValidatorNode(NodeActive)
	c.Assert(s.k.SetNodeAccount(s.ctx, nodeAccount), IsNil)
	voter, err := s.k.GetObservedTxInVoter(s.ctx, tx.ID)
	c.Assert(err, IsNil)
	voter.Add(NewObservedTx(tx, s.ctx.BlockHeight(), nodeAccount.PubKeySet.Secp256k1, s.ctx.BlockHeight()), nodeAccount.NodeAddress)
	s.k.SetObservedTxInVoter(s.ctx, voter)
	result, err = s.querier(s.ctx, []string{query.QueryTx.Key, tx.ID.String()}, req)
	c.Assert(err, IsNil)
	var newTx struct {
		openapi.ObservedTx `json:"observed_tx"`
		KeysignMetrics     types.TssKeysignMetric `json:"keysign_metric,omitempty"`
	}
	c.Assert(json.Unmarshal(result, &newTx), IsNil)

	// common.Tx Valid cannot be used for openapi.Tx, so checking some criteria individually.
	c.Assert(newTx.ExternalObservedHeight == nil, Equals, false)
	c.Assert(*newTx.ExternalObservedHeight <= 0, Equals, false)
	c.Assert(newTx.ObservedPubKey == nil, Equals, false)
	c.Assert(newTx.ExternalConfirmationDelayHeight == nil, Equals, false)
	c.Assert(*newTx.ExternalConfirmationDelayHeight <= 0, Equals, false)
	c.Assert(newTx.Tx.Id == nil, Equals, false)
	c.Assert(newTx.Tx.FromAddress == nil, Equals, false)
	c.Assert(newTx.Tx.ToAddress == nil, Equals, false)
	c.Assert(newTx.Tx.Chain == nil, Equals, false)
	c.Assert(len(newTx.Tx.Coins) == 0, Equals, false)
}

func (s *QuerierSuite) TestQueryKeyGen(c *C) {
	req := abci.RequestQuery{
		Data:   nil,
		Path:   query.QueryKeygensPubkey.Key,
		Height: s.ctx.BlockHeight(),
		Prove:  false,
	}

	result, err := s.querier(s.ctx, []string{
		query.QueryKeygensPubkey.Key,
		"whatever",
	}, req)

	c.Assert(result, IsNil)
	c.Assert(err, NotNil)

	result, err = s.querier(s.ctx, []string{
		query.QueryKeygensPubkey.Key,
		"10000",
	}, req)

	c.Assert(result, IsNil)
	c.Assert(err, NotNil)

	result, err = s.querier(s.ctx, []string{
		query.QueryKeygensPubkey.Key,
		strconv.FormatInt(s.ctx.BlockHeight(), 10),
	}, req)
	c.Assert(err, IsNil)
	c.Assert(result, NotNil)

	result, err = s.querier(s.ctx, []string{
		query.QueryKeygensPubkey.Key,
		strconv.FormatInt(s.ctx.BlockHeight(), 10),
		GetRandomPubKey().String(),
	}, req)
	c.Assert(result, NotNil)
	c.Assert(err, IsNil)
}

func (s *QuerierSuite) TestQueryQueue(c *C) {
	result, err := s.querier(s.ctx, []string{
		query.QueryQueue.Key,
		strconv.FormatInt(s.ctx.BlockHeight(), 10),
	}, abci.RequestQuery{})
	c.Assert(result, NotNil)
	c.Assert(err, IsNil)
	var q openapi.QueueResponse
	c.Assert(json.Unmarshal(result, &q), IsNil)
}

func (s *QuerierSuite) TestQueryHeights(c *C) {
	result, err := s.querier(s.ctx, []string{
		query.QueryHeights.Key,
		strconv.FormatInt(s.ctx.BlockHeight(), 10),
	}, abci.RequestQuery{})
	c.Assert(result, IsNil)
	c.Assert(err, NotNil)

	result, err = s.querier(s.ctx, []string{
		query.QueryHeights.Key,
	}, abci.RequestQuery{})
	c.Assert(result, NotNil)
	c.Assert(err, IsNil)
	var q []openapi.LastBlock
	c.Assert(json.Unmarshal(result, &q), IsNil)

	result, err = s.querier(s.ctx, []string{
		query.QueryHeights.Key,
		"BTC",
	}, abci.RequestQuery{})
	c.Assert(result, NotNil)
	c.Assert(err, IsNil)
	c.Assert(json.Unmarshal(result, &q), IsNil)

	result, err = s.querier(s.ctx, []string{
		query.QueryChainHeights.Key,
		"BTC",
	}, abci.RequestQuery{})
	c.Assert(result, NotNil)
	c.Assert(err, IsNil)
	c.Assert(json.Unmarshal(result, &q), IsNil)
}

func (s *QuerierSuite) TestQueryConstantValues(c *C) {
	result, err := s.querier(s.ctx, []string{
		query.QueryConstantValues.Key,
	}, abci.RequestQuery{})
	c.Assert(result, NotNil)
	c.Assert(err, IsNil)
}

func (s *QuerierSuite) TestQueryMimir(c *C) {
	s.k.SetMimir(s.ctx, "hello", 111)
	result, err := s.querier(s.ctx, []string{
		query.QueryMimirValues.Key,
	}, abci.RequestQuery{})
	c.Assert(err, IsNil)
	c.Assert(result, NotNil)
	var m map[string]int64
	c.Assert(json.Unmarshal(result, &m), IsNil)
	c.Assert(m, HasLen, 1)
	c.Assert(m["HELLO"], Equals, int64(111))
}

func (s *QuerierSuite) TestQueryBan(c *C) {
	result, err := s.querier(s.ctx, []string{
		query.QueryBan.Key,
	}, abci.RequestQuery{})
	c.Assert(result, IsNil)
	c.Assert(err, NotNil)

	result, err = s.querier(s.ctx, []string{
		query.QueryBan.Key,
		"Whatever",
	}, abci.RequestQuery{})
	c.Assert(result, IsNil)
	c.Assert(err, NotNil)

	result, err = s.querier(s.ctx, []string{
		query.QueryBan.Key,
		GetRandomBech32Addr().String(),
	}, abci.RequestQuery{})
	c.Assert(result, NotNil)
	c.Assert(err, IsNil)
}

func (s *QuerierSuite) TestQueryNodeAccount(c *C) {
	result, err := s.querier(s.ctx, []string{
		query.QueryNode.Key,
	}, abci.RequestQuery{})
	c.Assert(result, IsNil)
	c.Assert(err, NotNil)

	result, err = s.querier(s.ctx, []string{
		query.QueryNode.Key,
		"Whatever",
	}, abci.RequestQuery{})
	c.Assert(result, IsNil)
	c.Assert(err, NotNil)

	na := GetRandomValidatorNode(NodeActive)
	c.Assert(s.k.SetNodeAccount(s.ctx, na), IsNil)
	vault := GetRandomVault()
	vault.Status = ActiveVault
	vault.BlockHeight = 1
	c.Assert(s.k.SetVault(s.ctx, vault), IsNil)
	result, err = s.querier(s.ctx, []string{
		query.QueryNode.Key,
		na.NodeAddress.String(),
	}, abci.RequestQuery{})
	c.Assert(result, NotNil)
	c.Assert(err, IsNil)
	var r openapi.Node
	c.Assert(json.Unmarshal(result, &r), IsNil)

	/* Check bond-weighted rewards estimation works */

	// Add another node with 75% of the bond
	nodeAccount2 := GetRandomValidatorNode(NodeActive)
	nodeAccount2.Bond = cosmos.NewUint(common.One * 3000)
	c.Assert(s.k.SetNodeAccount(s.ctx, nodeAccount2), IsNil)

	// Add bond rewards + set min bond for bond-weighted system
	network, _ := s.k.GetNetwork(s.ctx)
	network.BondRewardRune = cosmos.NewUint(common.One * 1000)
	c.Assert(s.k.SetNetwork(s.ctx, network), IsNil)
	s.k.SetMimir(s.ctx, "MinimumBondInRune", common.One*1000)

	// Get first node
	result, err = s.querier(s.ctx, []string{
		query.QueryNode.Key,
		na.NodeAddress.String(),
	}, abci.RequestQuery{})
	c.Assert(result, NotNil)
	c.Assert(err, IsNil)
	var r2 openapi.Node
	c.Assert(json.Unmarshal(result, &r2), IsNil)

	// First node has 25% of bond, should have 25% of the rewards
	c.Assert(r2.TotalBond, Equals, cosmos.NewUint(common.One*1000).String())
	c.Assert(r2.CurrentAward, Equals, cosmos.NewUint(common.One*250).String())

	// Get second node
	result, err = s.querier(s.ctx, []string{
		query.QueryNode.Key,
		nodeAccount2.NodeAddress.String(),
	}, abci.RequestQuery{})
	c.Assert(result, NotNil)
	c.Assert(err, IsNil)
	var r3 openapi.Node
	c.Assert(json.Unmarshal(result, &r3), IsNil)

	// Second node has 75% of bond, should have 75% of the rewards
	c.Assert(r3.TotalBond, Equals, cosmos.NewUint(common.One*3000).String())
	c.Assert(r3.CurrentAward, Equals, cosmos.NewUint(common.One*750).String())
}

func (s *QuerierSuite) TestQueryPoolAddresses(c *C) {
	na := GetRandomValidatorNode(NodeActive)
	c.Assert(s.k.SetNodeAccount(s.ctx, na), IsNil)
	result, err := s.querier(s.ctx, []string{
		query.QueryInboundAddresses.Key,
		na.NodeAddress.String(),
	}, abci.RequestQuery{})
	c.Assert(result, NotNil)
	c.Assert(err, IsNil)

	var resp struct {
		Current []struct {
			Chain   common.Chain   `json:"chain"`
			PubKey  common.PubKey  `json:"pub_key"`
			Address common.Address `json:"address"`
			Halted  bool           `json:"halted"`
		} `json:"current"`
	}
	c.Assert(json.Unmarshal(result, &resp), IsNil)
}

func (s *QuerierSuite) TestQueryKeysignArrayPubKey(c *C) {
	na := GetRandomValidatorNode(NodeActive)
	c.Assert(s.k.SetNodeAccount(s.ctx, na), IsNil)
	result, err := s.querier(s.ctx, []string{
		query.QueryKeysignArrayPubkey.Key,
	}, abci.RequestQuery{})
	c.Assert(result, IsNil)
	c.Assert(err, NotNil)

	result, err = s.querier(s.ctx, []string{
		query.QueryKeysignArrayPubkey.Key,
		"asdf",
	}, abci.RequestQuery{})
	c.Assert(result, IsNil)
	c.Assert(err, NotNil)

	result, err = s.querier(s.ctx, []string{
		query.QueryKeysignArrayPubkey.Key,
		strconv.FormatInt(s.ctx.BlockHeight(), 10),
	}, abci.RequestQuery{})
	c.Assert(err, IsNil)
	c.Assert(result, NotNil)
	var r openapi.KeysignResponse
	c.Assert(json.Unmarshal(result, &r), IsNil)
}

func (s *QuerierSuite) TestQueryNetwork(c *C) {
	result, err := s.querier(s.ctx, []string{
		query.QueryNetwork.Key,
	}, abci.RequestQuery{})
	c.Assert(result, NotNil)
	c.Assert(err, IsNil)
	var r Network
	c.Assert(json.Unmarshal(result, &r), IsNil)
}

func (s *QuerierSuite) TestQueryAsgardVault(c *C) {
	c.Assert(s.k.SetVault(s.ctx, GetRandomVault()), IsNil)
	result, err := s.querier(s.ctx, []string{
		query.QueryVaultsAsgard.Key,
	}, abci.RequestQuery{})
	c.Assert(result, NotNil)
	c.Assert(err, IsNil)
	var r Vaults
	c.Assert(json.Unmarshal(result, &r), IsNil)
}

func (s *QuerierSuite) TestQueryVaultPubKeys(c *C) {
	node := GetRandomValidatorNode(NodeActive)
	c.Assert(s.k.SetNodeAccount(s.ctx, node), IsNil)
	vault := GetRandomVault()
	vault.PubKey = node.PubKeySet.Secp256k1
	vault.Type = AsgardVault
	vault.AddFunds(common.Coins{
		common.NewCoin(common.ETHAsset, cosmos.NewUint(common.One*100)),
	})
	vault.Routers = []types.ChainContract{
		{
			Chain:  "ETH",
			Router: "0xE65e9d372F8cAcc7b6dfcd4af6507851Ed31bb44",
		},
	}
	c.Assert(s.k.SetVault(s.ctx, vault), IsNil)
	vault1 := GetRandomVault()
	vault1.Routers = vault.Routers
	c.Assert(s.k.SetVault(s.ctx, vault1), IsNil)
	result, err := s.querier(s.ctx, []string{
		query.QueryVaultPubkeys.Key,
	}, abci.RequestQuery{})
	c.Assert(result, NotNil)
	c.Assert(err, IsNil)
	var r openapi.VaultPubkeysResponse
	c.Assert(json.Unmarshal(result, &r), IsNil)
}

func (s *QuerierSuite) TestQueryBalanceModule(c *C) {
	c.Assert(s.k.SetVault(s.ctx, GetRandomVault()), IsNil)
	result, err := s.querier(s.ctx, []string{
		query.QueryBalanceModule.Key,
		"asgard",
	}, abci.RequestQuery{})
	c.Assert(result, NotNil)
	c.Assert(err, IsNil)
	var r struct {
		Name    string            `json:"name"`
		Address cosmos.AccAddress `json:"address"`
		Coins   types2.Coins      `json:"coins"`
	}
	c.Assert(json.Unmarshal(result, &r), IsNil)
}

func (s *QuerierSuite) TestQueryVault(c *C) {
	vault := GetRandomVault()

	// Not enough argument
	result, err := s.querier(s.ctx, []string{
		query.QueryVault.Key,
		"ETH",
	}, abci.RequestQuery{})

	c.Assert(result, IsNil)
	c.Assert(err, NotNil)

	c.Assert(s.k.SetVault(s.ctx, vault), IsNil)
	result, err = s.querier(s.ctx, []string{
		query.QueryVault.Key,
		vault.PubKey.String(),
	}, abci.RequestQuery{})
	c.Assert(err, IsNil)
	c.Assert(result, NotNil)
	var returnVault Vault
	c.Assert(json.Unmarshal(result, &returnVault), IsNil)
	c.Assert(vault.PubKey.Equals(returnVault.PubKey), Equals, true)
	c.Assert(vault.Type, Equals, returnVault.Type)
	c.Assert(vault.Status, Equals, returnVault.Status)
	c.Assert(vault.BlockHeight, Equals, returnVault.BlockHeight)
}

func (s *QuerierSuite) TestQueryVersion(c *C) {
	result, err := s.querier(s.ctx, []string{
		query.QueryVersion.Key,
	}, abci.RequestQuery{})
	c.Assert(result, NotNil)
	c.Assert(err, IsNil)
	var r openapi.VersionResponse
	c.Assert(json.Unmarshal(result, &r), IsNil)

	verComputed := s.k.GetLowestActiveVersion(s.ctx)
	c.Assert(r.Current, Equals, verComputed.String(),
		Commentf("query should return same version as computed"))

	// override the version computed in BeginBlock
	s.k.SetVersionWithCtx(s.ctx, semver.MustParse("4.5.6"))

	result, err = s.querier(s.ctx, []string{
		query.QueryVersion.Key,
	}, abci.RequestQuery{})
	c.Assert(result, NotNil)
	c.Assert(err, IsNil)
	c.Assert(json.Unmarshal(result, &r), IsNil)
	c.Assert(r.Current, Equals, "4.5.6",
		Commentf("query should use stored version"))
}

func (s *QuerierSuite) TestPeerIDFromPubKey(c *C) {
	// Success example, secp256k1 pubkey from Mocknet node tthor1jgnk2mg88m57csrmrlrd6c3qe4lag3e33y2f3k
	var mocknetPubKey common.PubKey = "tthorpub1addwnpepqt8tnluxnk3y5quyq952klgqnlmz2vmaynm40fp592s0um7ucvjh5lc2l2z"
	c.Assert(getPeerIDFromPubKey(mocknetPubKey), Equals, "16Uiu2HAm9LeTqHJWSa67eHNZzSz3yKb64dbj7A4V1Ckv9hXyDkQR")

	// Failure example.
	expectedErrorString := "fail to parse account pub key(nonsense): decoding bech32 failed: invalid separator index -1"
	c.Assert(getPeerIDFromPubKey("nonsense"), Equals, expectedErrorString)
}
