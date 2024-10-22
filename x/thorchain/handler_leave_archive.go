package thorchain

import (
	"fmt"

	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/common/cosmos"
	"gitlab.com/thorchain/thornode/constants"
)

func (h LeaveHandler) handleV134(ctx cosmos.Context, msg MsgLeave) error {
	nodeAcc, err := h.mgr.Keeper().GetNodeAccount(ctx, msg.NodeAddress)
	if err != nil {
		return ErrInternal(err, "fail to get node account by bond address")
	}
	if nodeAcc.IsEmpty() {
		return cosmos.ErrUnknownRequest("node account doesn't exist")
	}
	if !nodeAcc.BondAddress.Equals(msg.Tx.FromAddress) {
		return cosmos.ErrUnauthorized(fmt.Sprintf("%s are not authorized to manage %s", msg.Tx.FromAddress, msg.NodeAddress))
	}
	// THORNode add the node to leave queue

	coin := msg.Tx.Coins.GetCoin(common.RuneAsset())
	if !coin.IsEmpty() {
		nodeAcc.Bond = nodeAcc.Bond.Add(coin.Amount)
	}
	bondAddr, err := nodeAcc.BondAddress.AccAddress()
	if err != nil {
		return ErrInternal(err, "fail to refund bond")
	}

	if nodeAcc.Status == NodeActive {
		if nodeAcc.LeaveScore == 0 {
			// get to the 8th decimal point, but keep numbers integers for safer math
			age := cosmos.NewUint(uint64((ctx.BlockHeight() - nodeAcc.StatusSince) * common.One))
			// trunk-ignore(golangci-lint/govet): shadow
			slashPts, err := h.mgr.Keeper().GetNodeAccountSlashPoints(ctx, nodeAcc.NodeAddress)
			if err != nil || slashPts == 0 {
				ctx.Logger().Error("fail to get node account slash points", "error", err)
				nodeAcc.LeaveScore = age.Uint64()
			} else {
				nodeAcc.LeaveScore = age.QuoUint64(uint64(slashPts)).Uint64()
			}
		}
	} else {
		// trunk-ignore(golangci-lint/govet): shadow
		bondLockPeriod, err := h.mgr.Keeper().GetMimir(ctx, constants.BondLockupPeriod.String())
		if err != nil || bondLockPeriod < 0 {
			bondLockPeriod = h.mgr.GetConstants().GetInt64Value(constants.BondLockupPeriod)
		}
		if ctx.BlockHeight()-nodeAcc.StatusSince < bondLockPeriod {
			return fmt.Errorf("node can not unbond before %d", nodeAcc.StatusSince+bondLockPeriod)
		}
		vaults, err := h.mgr.Keeper().GetAsgardVaultsByStatus(ctx, RetiringVault)
		if err != nil {
			return ErrInternal(err, "fail to get retiring vault")
		}
		isMemberOfRetiringVault := false
		for _, v := range vaults {
			if v.GetMembership().Contains(nodeAcc.PubKeySet.Secp256k1) {
				isMemberOfRetiringVault = true
				ctx.Logger().Info("node account is still part of the retiring vault,can't return bond yet")
				break
			}
		}
		if !isMemberOfRetiringVault {
			// NOTE: there is an edge case, where the first node doesn't have a
			// vault (it was destroyed when we successfully migrated funds from
			// their address to a new TSS vault
			if !h.mgr.Keeper().VaultExists(ctx, nodeAcc.PubKeySet.Secp256k1) {
				// trunk-ignore(golangci-lint/govet): shadow
				if err := refundBond(ctx, msg.Tx, bondAddr, cosmos.ZeroUint(), &nodeAcc, h.mgr); err != nil {
					return ErrInternal(err, "fail to refund bond")
				}
				nodeAcc.UpdateStatus(NodeDisabled, ctx.BlockHeight())
			}
		}
	}
	nodeAcc.RequestedToLeave = true
	// trunk-ignore(golangci-lint/govet): shadow
	if err := h.mgr.Keeper().SetNodeAccount(ctx, nodeAcc); err != nil {
		return ErrInternal(err, "fail to save node account to key value store")
	}
	ctx.EventManager().EmitEvent(
		cosmos.NewEvent("validator_request_leave",
			cosmos.NewAttribute("signer", msg.Tx.FromAddress.String()),
			cosmos.NewAttribute("node", msg.NodeAddress.String()),
			cosmos.NewAttribute("txid", msg.Tx.ID.String())))

	return nil
}
