package thorchain

import (
	"errors"
	"fmt"

	"github.com/cosmos/cosmos-sdk/codec"
	se "github.com/cosmos/cosmos-sdk/types/errors"
	upgradetypes "github.com/cosmos/cosmos-sdk/x/upgrade/types"
	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/common/cosmos"
	"gitlab.com/thorchain/thornode/x/thorchain/keeper"
	"gitlab.com/thorchain/thornode/x/thorchain/types"
)

type HandlerUpgradeSuite struct{}

type TestUpgradeKeeper struct {
	keeper.KVStoreDummy
	activeAccounts   []NodeAccount
	failNodeAccount  NodeAccount
	emptyNodeAccount NodeAccount
	vaultNodeAccount NodeAccount
	proposedUpgrade  *types.Upgrade
	votes            map[string]bool

	scheduled bool
}

func (k *TestUpgradeKeeper) GetNodeAccount(_ cosmos.Context, addr cosmos.AccAddress) (NodeAccount, error) {
	if k.failNodeAccount.NodeAddress.Equals(addr) {
		return NodeAccount{}, errKaboom
	}
	if k.emptyNodeAccount.NodeAddress.Equals(addr) {
		return NodeAccount{}, nil
	}
	if k.vaultNodeAccount.NodeAddress.Equals(addr) {
		return NodeAccount{Type: NodeTypeVault}, nil
	}

	for _, na := range k.activeAccounts {
		if na.NodeAddress.Equals(addr) {
			return na, nil
		}
	}

	return NodeAccount{}, errKaboom
}

func (k *TestUpgradeKeeper) ProposeUpgrade(_ cosmos.Context, name string, upgrade types.Upgrade) error {
	k.proposedUpgrade = &upgrade
	return nil
}

func (k *TestUpgradeKeeper) GetProposedUpgrade(_ cosmos.Context, name string) (*types.Upgrade, error) {
	return k.proposedUpgrade, nil
}

func (k *TestUpgradeKeeper) ApproveUpgrade(_ cosmos.Context, addr cosmos.AccAddress, name string) {
	k.votes[addr.String()] = true
}

func (k *TestUpgradeKeeper) RejectUpgrade(_ cosmos.Context, addr cosmos.AccAddress, name string) {
	k.votes[addr.String()] = false
}

func (k *TestUpgradeKeeper) GetNodeAccountIterator(_ cosmos.Context) cosmos.Iterator {
	nas := make([]NodeAccount, 0, len(k.activeAccounts)+2)
	nas = append(nas, k.activeAccounts...)
	nas = append(nas, k.vaultNodeAccount, k.emptyNodeAccount)
	return newMockNodeAccountIterator(k.Cdc(), nas)
}

func (k *TestUpgradeKeeper) ListActiveValidators(_ cosmos.Context) (NodeAccounts, error) {
	return k.activeAccounts, nil
}

func (k *TestUpgradeKeeper) GetUpgradeVoteIterator(_ cosmos.Context, name string) cosmos.Iterator {
	votes := make([]mockVote, 0, len(k.votes))
	for addr, approve := range k.votes {
		acc, err := cosmos.AccAddressFromBech32(addr)
		if err != nil {
			panic(err)
		}
		votes = append(votes, mockVote{acc: acc, approve: approve})
	}
	return newMockUpgradeVoteIterator(votes)
}

func (k *TestUpgradeKeeper) ScheduleUpgrade(_ cosmos.Context, plan upgradetypes.Plan) error {
	k.scheduled = true
	return nil
}

func (k *TestUpgradeKeeper) ClearUpgradePlan(_ cosmos.Context) {
	k.scheduled = false
}

func (k *TestUpgradeKeeper) GetUpgradePlan(_ cosmos.Context) (upgradetypes.Plan, bool) {
	if k.scheduled {
		return upgradetypes.Plan{Name: "1.2.3"}, true
	}
	return upgradetypes.Plan{}, false
}

var _ = Suite(&HandlerUpgradeSuite{})

func (s *HandlerUpgradeSuite) TestUpgrade(c *C) {
	ctx, _ := setupKeeperForTest(c)

	const (
		upgradeName = "1.2.3"
		upgradeInfo = "scheduled upgrade"
	)

	upgradeHeight := ctx.BlockHeight() + 100

	keeper := &TestUpgradeKeeper{
		failNodeAccount:  GetRandomValidatorNode(NodeActive),
		emptyNodeAccount: GetRandomValidatorNode(NodeStandby),
		vaultNodeAccount: GetRandomVaultNode(NodeActive),
		votes:            make(map[string]bool),
	}

	// add some active accounts
	for i := 0; i < 10; i++ {
		keeper.activeAccounts = append(keeper.activeAccounts, GetRandomValidatorNode(NodeActive))
	}

	mgr := NewDummyMgrWithKeeper(keeper)

	handler := NewProposeUpgradeHandler(mgr)

	// invalid height
	msg := NewMsgProposeUpgrade(upgradeName, ctx.BlockHeight(), upgradeInfo, keeper.activeAccounts[0].NodeAddress)
	result, err := handler.Run(ctx, msg)
	c.Assert(result, IsNil)
	c.Assert(err, NotNil)

	// invalid msg
	msg = &MsgProposeUpgrade{}
	result, err = handler.Run(ctx, msg)
	c.Assert(result, IsNil)
	c.Assert(err, NotNil)

	// fail to get node account should fail
	msg1 := NewMsgProposeUpgrade(upgradeName, upgradeHeight, upgradeInfo, keeper.failNodeAccount.NodeAddress)
	result, err = handler.Run(ctx, msg1)
	c.Assert(err, NotNil)
	c.Assert(result, IsNil)

	// node account empty should fail
	msg2 := NewMsgProposeUpgrade(upgradeName, upgradeHeight, upgradeInfo, keeper.emptyNodeAccount.NodeAddress)
	result, err = handler.Run(ctx, msg2)
	c.Assert(err, NotNil)
	c.Assert(result, IsNil)
	c.Assert(errors.Is(err, se.ErrUnauthorized), Equals, true)

	// vault node should fail
	msg3 := NewMsgProposeUpgrade(upgradeName, upgradeHeight, upgradeInfo, keeper.vaultNodeAccount.NodeAddress)
	result, err = handler.Run(ctx, msg3)
	c.Assert(err, NotNil)
	c.Assert(result, IsNil)
	c.Assert(errors.Is(err, se.ErrUnauthorized), Equals, true)

	// happy path to get the upgrade proposed
	msg4 := NewMsgProposeUpgrade(upgradeName, upgradeHeight, upgradeInfo, keeper.activeAccounts[0].NodeAddress)
	result, err = handler.Run(ctx, msg4)
	c.Assert(err, IsNil)
	c.Assert(result, NotNil)
	c.Assert(keeper.scheduled, Equals, false)

	// proposed upgrade with same name should fail
	msg5 := NewMsgProposeUpgrade(upgradeName, upgradeHeight, upgradeInfo, keeper.activeAccounts[1].NodeAddress)
	result, err = handler.Run(ctx, msg5)
	c.Assert(err, NotNil)
	c.Assert(result, IsNil)
	c.Assert(errors.Is(err, se.ErrUnknownRequest), Equals, true)

	approveHandler := NewApproveUpgradeHandler(mgr)

	// vote for upgrade by 1 less than 2/3 of active accounts
	for i, na := range keeper.activeAccounts {
		if i == 0 {
			// skip the proposer because they already approved by proposing
			continue
		}
		if i == 6 {
			// break after 6 approve votes so that we are 1 less than 2/3
			break
		}
		msg := NewMsgApproveUpgrade(upgradeName, na.NodeAddress)
		result, err = approveHandler.Run(ctx, msg)
		c.Assert(err, IsNil)
		c.Assert(result, NotNil)
	}

	// upgrade should still not be scheduled
	c.Assert(keeper.scheduled, Equals, false)

	// vote for upgrade by one more active account
	msg6 := NewMsgApproveUpgrade(upgradeName, keeper.activeAccounts[8].NodeAddress)
	result, err = approveHandler.Run(ctx, msg6)
	c.Assert(err, IsNil)
	c.Assert(result, NotNil)

	// upgrade should now be scheduled
	c.Assert(keeper.scheduled, Equals, true)

	rejectHandler := NewRejectUpgradeHandler(mgr)

	// reject upgrade by one of the active accounts to drop below 2/3
	msg7 := NewMsgRejectUpgrade(upgradeName, keeper.activeAccounts[4].NodeAddress)
	result, err = rejectHandler.Run(ctx, msg7)
	c.Assert(err, IsNil)
	c.Assert(result, NotNil)

	// upgrade should now be cleared
	c.Assert(keeper.scheduled, Equals, false)
}

type mockNodeAccountIterator struct {
	cdc          codec.BinaryCodec
	nodeAccounts []NodeAccount
	i            int
}

func newMockNodeAccountIterator(cdc codec.BinaryCodec, nodeAccounts []NodeAccount) *mockNodeAccountIterator {
	return &mockNodeAccountIterator{cdc: cdc, nodeAccounts: nodeAccounts}
}

func (it *mockNodeAccountIterator) Domain() (start []byte, end []byte) { return nil, nil }
func (it *mockNodeAccountIterator) Valid() bool                        { return it.i < len(it.nodeAccounts) }
func (it *mockNodeAccountIterator) Next()                              { it.i++ }
func (it *mockNodeAccountIterator) Key() (key []byte)                  { return nil }
func (it *mockNodeAccountIterator) Value() (value []byte) {
	bz, err := it.cdc.Marshal(&it.nodeAccounts[it.i])
	if err != nil {
		panic(fmt.Errorf("failed to marshal: %w", err))
	}
	return bz
}
func (it *mockNodeAccountIterator) Error() error { return nil }
func (it *mockNodeAccountIterator) Close() error { return nil }

type mockUpgradeVoteIterator struct {
	upgradeVotes []mockVote
	i            int
}

type mockVote struct {
	acc     cosmos.AccAddress
	approve bool
}

func newMockUpgradeVoteIterator(upgradeVotes []mockVote) *mockUpgradeVoteIterator {
	return &mockUpgradeVoteIterator{upgradeVotes: upgradeVotes}
}

func (it *mockUpgradeVoteIterator) Domain() (start []byte, end []byte) { return nil, nil }
func (it *mockUpgradeVoteIterator) Valid() bool                        { return it.i < len(it.upgradeVotes) }
func (it *mockUpgradeVoteIterator) Next()                              { it.i++ }
func (it *mockUpgradeVoteIterator) Key() (key []byte) {
	return it.upgradeVotes[it.i].acc.Bytes()
}

func (it *mockUpgradeVoteIterator) Value() (value []byte) {
	if it.upgradeVotes[it.i].approve {
		return []byte{0x01}
	}
	return []byte{0x00}
}

func (it *mockUpgradeVoteIterator) Error() error { return nil }
func (it *mockUpgradeVoteIterator) Close() error { return nil }
