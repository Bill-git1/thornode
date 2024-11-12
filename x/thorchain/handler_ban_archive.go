package thorchain

import (
	"fmt"

	errorsmod "cosmossdk.io/errors"
	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/common/cosmos"
	"gitlab.com/thorchain/thornode/constants"
)

func (h BanHandler) handleV1(ctx cosmos.Context, msg MsgBan) (*cosmos.Result, error) {
	toBan, err := h.mgr.Keeper().GetNodeAccount(ctx, msg.NodeAddress)
	if err != nil {
		err = wrapError(ctx, err, "fail to get to ban node account")
		return nil, err
	}
	// trunk-ignore(golangci-lint/govet): shadow
	if err := toBan.Valid(); err != nil {
		return nil, err
	}
	if toBan.ForcedToLeave {
		// already ban, no need to ban again
		return &cosmos.Result{}, nil
	}

	switch toBan.Status {
	case NodeActive, NodeStandby:
		// we can ban an active or standby node
	default:
		return nil, errorsmod.Wrap(errInternal, "cannot ban a node account that is not currently active or standby")
	}

	banner, err := h.mgr.Keeper().GetNodeAccount(ctx, msg.Signer)
	if err != nil {
		err = wrapError(ctx, err, "fail to get banner node account")
		return nil, err
	}
	// trunk-ignore(golangci-lint/govet): shadow
	if err := banner.Valid(); err != nil {
		return nil, err
	}

	active, err := h.mgr.Keeper().ListActiveValidators(ctx)
	if err != nil {
		err = wrapError(ctx, err, "fail to get list of active node accounts")
		return nil, err
	}

	voter, err := h.mgr.Keeper().GetBanVoter(ctx, msg.NodeAddress)
	if err != nil {
		return nil, err
	}

	if !voter.HasSigned(msg.Signer) && voter.BlockHeight == 0 {
		// take 0.1% of the minimum bond, and put it into the reserve
		// trunk-ignore(golangci-lint/govet): shadow
		minBond, err := h.mgr.Keeper().GetMimir(ctx, constants.MinimumBondInRune.String())
		if minBond < 0 || err != nil {
			minBond = h.mgr.GetConstants().GetInt64Value(constants.MinimumBondInRune)
		}
		slashAmount := cosmos.NewUint(uint64(minBond)).QuoUint64(1000)
		if slashAmount.GT(banner.Bond) {
			slashAmount = banner.Bond
		}
		banner.Bond = common.SafeSub(banner.Bond, slashAmount)

		coin := common.NewCoin(common.RuneNative, slashAmount)
		// trunk-ignore(golangci-lint/govet): shadow
		if err := h.mgr.Keeper().SendFromModuleToModule(ctx, BondName, ReserveName, common.NewCoins(coin)); err != nil {
			ctx.Logger().Error("fail to transfer funds from bond to reserve", "error", err)
			return nil, err
		}

		// trunk-ignore(golangci-lint/govet): shadow
		if err := h.mgr.Keeper().SetNodeAccount(ctx, banner); err != nil {
			return nil, fmt.Errorf("fail to save node account: %w", err)
		}

		tx := common.Tx{}
		tx.ID = common.BlankTxID
		tx.FromAddress = banner.BondAddress
		bondEvent := NewEventBond(slashAmount, BondCost, tx, &banner, nil)
		// trunk-ignore(golangci-lint/govet): shadow
		if err := h.mgr.EventMgr().EmitEvent(ctx, bondEvent); err != nil {
			return nil, fmt.Errorf("fail to emit bond event: %w", err)
		}
	}

	voter.Sign(msg.Signer)
	h.mgr.Keeper().SetBanVoter(ctx, voter)
	// doesn't have consensus yet
	if !voter.HasConsensus(active) {
		ctx.Logger().Info("not having consensus yet, return")
		return &cosmos.Result{}, nil
	}

	if voter.BlockHeight > 0 {
		// ban already processed
		return &cosmos.Result{}, nil
	}

	voter.BlockHeight = ctx.BlockHeight()
	h.mgr.Keeper().SetBanVoter(ctx, voter)

	toBan.ForcedToLeave = true
	toBan.LeaveScore = 1 // Set Leave Score to 1, which means the nodes is bad
	if err := h.mgr.Keeper().SetNodeAccount(ctx, toBan); err != nil {
		err = fmt.Errorf("fail to save node account: %w", err)
		return nil, err
	}

	return &cosmos.Result{}, nil
}
