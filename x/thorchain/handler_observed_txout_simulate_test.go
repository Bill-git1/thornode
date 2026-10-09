package thorchain

import (
	"fmt"

	"github.com/blang/semver"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
)

type SimulateIsolationSuite struct{}

var _ = Suite(&SimulateIsolationSuite{})

// observedOutboundFixture stores an active validator, its vault and a scheduled
// outbound, and returns a message by which that validator alone reaches
// consensus on the outbound (one active node).
func observedOutboundFixture(c *C, ctx cosmos.Context, mgr *Mgrs) (*MsgObservedTxOut, NodeAccount) {
	k := mgr.Keeper()
	na := GetRandomValidatorNode(NodeActive)
	c.Assert(k.SetNodeAccount(ctx, na), IsNil)

	pk := GetRandomPubKey()
	vault := NewVault(ctx.BlockHeight(), ActiveVault, AsgardVault, pk, common.Chains{common.ETHChain}.Strings(), []ChainContract{})
	vault.Membership = []string{na.PubKeySet.Secp256k1.String()}
	vault.Coins = common.Coins{common.NewCoin(common.ETHAsset, cosmos.NewUint(200*common.One))}
	c.Assert(k.SetVault(ctx, vault), IsNil)

	tx := GetRandomTx()
	var err error
	tx.FromAddress, err = pk.GetAddress(common.ETHChain)
	c.Assert(err, IsNil)
	tx.Coins = common.Coins{common.NewCoin(common.ETHAsset, cosmos.NewUint(common.One))}
	tx.Gas = common.Gas{common.NewCoin(common.ETHAsset, cosmos.NewUint(37500))}
	inHash := GetRandomTxHash()
	tx.Memo = fmt.Sprintf("OUT:%s", inHash)
	c.Assert(mgr.TxOutStore().UnSafeAddTxOutItem(ctx, mgr, TxOutItem{
		Chain: tx.Chain, InHash: inHash, ToAddress: tx.ToAddress, VaultPubKey: pk, Coin: tx.Coins[0], Memo: tx.Memo,
	}, ctx.BlockHeight()), IsNil)

	obs := NewObservedTx(tx, 12, pk, 12)
	return NewMsgObservedTxOut(ObservedTxs{obs}, na.NodeAddress), na
}

// A simulation runs the full handler on a discarded state branch. Nothing it
// does may reach the next block's state on this node: neither the observers
// nor the gas that the shared managers buffer in memory.
func (s *SimulateIsolationSuite) TestSimulatedObservationLeavesNoBlockState(c *C) {
	ctx, mgr := setupManagerForTest(c)
	msg, _ := observedOutboundFixture(c, ctx, mgr)
	server := NewMsgServerImpl(mgr)

	simCtx, _ := ctx.WithExecMode(sdk.ExecModeSimulate).CacheContext() // never written
	_, err := server.ObservedTxOut(simCtx, msg)
	c.Assert(err, IsNil)

	c.Check(mgr.GasMgr().GetGas(), HasLen, 0)
	mgr.ObMgr().EndBlock(ctx, mgr.Keeper())
	observing, err := mgr.Keeper().GetObservingAddresses(ctx)
	c.Assert(err, IsNil)
	c.Check(observing, HasLen, 0)
}

// Block execution keeps using the shared managers.
func (s *SimulateIsolationSuite) TestFinalizedObservationUsesSharedManagers(c *C) {
	ctx, mgr := setupManagerForTest(c)
	msg, na := observedOutboundFixture(c, ctx, mgr)
	server := NewMsgServerImpl(mgr)

	_, err := server.ObservedTxOut(ctx.WithExecMode(sdk.ExecModeFinalize), msg)
	c.Assert(err, IsNil)

	c.Check(mgr.GasMgr().GetGas(), HasLen, 1)
	mgr.ObMgr().EndBlock(ctx, mgr.Keeper())
	observing, err := mgr.Keeper().GetObservingAddresses(ctx)
	c.Assert(err, IsNil)
	c.Assert(observing, HasLen, 1)
	c.Check(observing[0].Equals(na.NodeAddress), Equals, true)
}

// Observers buffered outside block execution are dropped when the next block
// begins.
func (s *SimulateIsolationSuite) TestObserverManagerBeginBlockResets(c *C) {
	om := newObserverMgr()
	om.AppendObserver(common.ETHChain, []cosmos.AccAddress{GetRandomBech32Addr()})
	om.BeginBlock()
	c.Check(om.List(), HasLen, 0)
}

func (s *SimulateIsolationSuite) TestManagersForExecMode(c *C) {
	ctx, mgr := setupManagerForTest(c)

	got, err := managersForExecMode(ctx.WithExecMode(sdk.ExecModeFinalize), mgr)
	c.Assert(err, IsNil)
	c.Check(got == Manager(mgr), Equals, true)

	got, err = managersForExecMode(ctx.WithExecMode(sdk.ExecModeSimulate), mgr)
	c.Assert(err, IsNil)
	c.Check(got == Manager(mgr), Equals, false)
	c.Check(got.GetConstants(), NotNil)

	dummy := NewDummyMgrWithKeeper(mgr.Keeper())
	got, err = managersForExecMode(ctx.WithExecMode(sdk.ExecModeSimulate), dummy)
	c.Assert(err, IsNil)
	c.Check(got == Manager(dummy), Equals, true)
}

// A poisoned shared version makes the normal send handler reject the message.
// Simulation succeeds only if BankSendHandler routes through a newly loaded
// throwaway manager.
func (s *SimulateIsolationSuite) TestBankSendSimulationUsesTemporaryManagers(c *C) {
	ctx, mgr := setupManagerForTest(c)
	from := GetRandomBech32Addr()
	to := GetRandomBech32Addr()
	FundAccount(c, ctx, mgr.Keeper(), from, 200*common.One)

	coin, err := common.NewCoin(common.RuneNative, cosmos.NewUint(12*common.One)).Native()
	c.Assert(err, IsNil)
	msg := &banktypes.MsgSend{
		FromAddress: from.String(),
		ToAddress:   to.String(),
		Amount:      cosmos.NewCoins(coin),
	}

	mgr.mu.Lock()
	mgr.currentVersion = semver.Version{}
	mgr.mu.Unlock()

	handler := NewBankSendHandler(mgr)
	simCtx, _ := ctx.WithExecMode(sdk.ExecModeSimulate).CacheContext() // never written
	_, err = handler.Send(simCtx, msg)
	c.Assert(err, IsNil)

	_, err = handler.Send(ctx.WithExecMode(sdk.ExecModeFinalize), msg)
	c.Check(err, Equals, errBadVersion)
}
