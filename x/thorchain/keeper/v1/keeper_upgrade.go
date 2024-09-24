package keeperv1

import (
	"bytes"
	"fmt"
	"math"
	"math/big"
	"strings"

	upgradetypes "github.com/cosmos/cosmos-sdk/x/upgrade/types"

	"gitlab.com/thorchain/thornode/common/cosmos"
	"gitlab.com/thorchain/thornode/x/thorchain/keeper"
	"gitlab.com/thorchain/thornode/x/thorchain/types"
)

// GetUpgradePlan proxies through to the upgrade keeper
func (k KVStore) GetUpgradePlan(ctx cosmos.Context) (upgradetypes.Plan, bool) {
	return k.upgradeKeeper.GetUpgradePlan(ctx)
}

// ScheduleUpgrade proxies through to the upgrade keeper
func (k KVStore) ScheduleUpgrade(ctx cosmos.Context, plan upgradetypes.Plan) error {
	return k.upgradeKeeper.ScheduleUpgrade(ctx, plan)
}

// ClearUpgradePlan proxies through to the upgrade keeper
func (k KVStore) ClearUpgradePlan(ctx cosmos.Context) {
	k.upgradeKeeper.ClearUpgradePlan(ctx)
}

// ProposeUpgrade proposes an upgrade by name
func (k KVStore) ProposeUpgrade(ctx cosmos.Context, name string, upgrade types.Upgrade) error {
	key := fmt.Sprintf("%s%s", prefixUpgradeProposals, name)
	store := ctx.KVStore(k.storeKey)

	v, err := k.cdc.Marshal(&upgrade)
	if err != nil {
		return fmt.Errorf("failed to marshal proposed upgrade: %w", err)
	}

	store.Set([]byte(key), v)

	return nil
}

// GetProposedUpgrade retrieves a proposed upgrade
func (k KVStore) GetProposedUpgrade(ctx cosmos.Context, name string) (*types.Upgrade, error) {
	key := fmt.Sprintf("%s%s", prefixUpgradeProposals, name)
	store := ctx.KVStore(k.storeKey)

	v := store.Get([]byte(key))
	if v == nil {
		return nil, nil
	}

	var upgrade types.Upgrade
	if err := k.cdc.Unmarshal(v, &upgrade); err != nil {
		return nil, fmt.Errorf("failed to unmarshal proposed upgrade: %w", err)
	}

	return &upgrade, nil
}

// ApproveUpgrade approves an upgrade as a validator
func (k KVStore) ApproveUpgrade(ctx cosmos.Context, addr cosmos.AccAddress, name string) {
	store := ctx.KVStore(k.storeKey)

	store.Set(append([]byte(VotePrefix(name)), addr...), []byte{0x1})
}

// RejectUpgrade rejects an upgrade as a validator
func (k KVStore) RejectUpgrade(ctx cosmos.Context, addr cosmos.AccAddress, name string) {
	store := ctx.KVStore(k.storeKey)

	store.Set(append([]byte(VotePrefix(name)), addr...), []byte{0xFF})
}

// RemoveExpiredUpgradeProposals removes an upgrade proposal and all votes
// after the proposal height has passed.
func (k KVStore) RemoveExpiredUpgradeProposals(ctx cosmos.Context) error {
	iter := k.GetUpgradeProposalIterator(ctx)
	defer iter.Close()

	store := ctx.KVStore(k.storeKey)

	for ; iter.Valid(); iter.Next() {
		key, value := iter.Key(), iter.Value()

		nameSplit := strings.Split(string(key), "/")
		name := nameSplit[len(nameSplit)-1]

		var upgrade types.Upgrade
		if err := k.cdc.Unmarshal(value, &upgrade); err != nil {
			return fmt.Errorf("failed to unmarshal proposed upgrade: %w", err)
		}

		if ctx.BlockHeight() <= upgrade.Height {
			continue
		}

		ctx.Logger().Info(
			"Deleting expired upgrade proposal",
			"name", name,
		)

		k.removeExpiredUpgradeProposalVotes(ctx, name)
		store.Delete(key)
	}

	return nil
}

func (k KVStore) removeExpiredUpgradeProposalVotes(ctx cosmos.Context, name string) {
	store := ctx.KVStore(k.storeKey)

	iter := k.GetUpgradeVoteIterator(ctx, name)
	defer iter.Close()

	for ; iter.Valid(); iter.Next() {
		store.Delete(iter.Key())
	}
}

// UpgradeQuorum represents the quorum status of an upgrade proposal.
type UpgradeQuorum struct {
	Approved        bool
	ApprovingVals   int
	TotalActive     int
	NeededForQuorum int
}

// UpgradeApprovedByMajority returns true and no error if the upgrade is approved by 2/3 of Validators.
// it additionally returns the current approving val count, the total active val count, and the
// additional active validators needed to reach quorum, if not already approved.
func UpgradeApprovedByMajority(ctx cosmos.Context, k keeper.Keeper, name string) (*UpgradeQuorum, error) {
	iterA := k.GetNodeAccountIterator(ctx)
	defer iterA.Close()

	activeVals, err := k.ListActiveValidators(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list active validators: %w", err)
	}

	active := make(map[string]bool)

	for _, na := range activeVals {
		active[na.NodeAddress.String()] = true
	}

	totalActive := len(active)

	iterV := k.GetUpgradeVoteIterator(ctx, name)
	defer iterV.Close()

	var approvingVals int

	for ; iterV.Valid(); iterV.Next() {
		key, vote := iterV.Key(), iterV.Value()
		if !bytes.Equal(vote, []byte{0x1}) {
			continue
		}

		prefix := []byte(VotePrefix(name))
		addr := cosmos.AccAddress(bytes.TrimPrefix(key, prefix))

		_, ok := active[addr.String()]
		if !ok {
			// this could happen if a validator votes and then becomes inactive
			continue
		}

		approvingVals++
	}

	valThreshold := big.NewRat(int64(totalActive)*2, 3)

	t, _ := valThreshold.Float64()

	if float64(approvingVals) >= t {
		return &UpgradeQuorum{
			Approved:        true,
			ApprovingVals:   approvingVals,
			TotalActive:     totalActive,
			NeededForQuorum: 0,
		}, nil
	}

	neededForQuorum := new(big.Rat).Sub(valThreshold, big.NewRat(int64(approvingVals), 1))
	nfq, _ := neededForQuorum.Float64()
	valsToQuorum := math.Ceil(nfq)

	return &UpgradeQuorum{
		Approved:        false,
		ApprovingVals:   approvingVals,
		TotalActive:     totalActive,
		NeededForQuorum: int(valsToQuorum),
	}, nil
}
