package thorchain

import (
	"fmt"

	"github.com/blang/semver"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
)

// LeaveHandler a handler to process leave request
// if an operator of THORChain node would like to leave and get their bond back , they have to
// send a Leave request through THORChain
type LeaveHandler struct {
	mgr Manager
}

// NewLeaveHandler create a new LeaveHandler
func NewLeaveHandler(mgr Manager) LeaveHandler {
	return LeaveHandler{
		mgr: mgr,
	}
}

// Run execute the handler
func (h LeaveHandler) Run(ctx cosmos.Context, m cosmos.Msg) (*cosmos.Result, error) {
	msg, ok := m.(*MsgLeave)
	if !ok {
		return nil, errInvalidMessage
	}
	ctx.Logger().Info("receive MsgLeave",
		"sender", msg.Tx.FromAddress.String(),
		"request tx hash", msg.Tx.ID)
	if err := h.validate(ctx, *msg); err != nil {
		ctx.Logger().Error("msg leave fail validation", "error", err)
		return nil, err
	}

	if err := h.handle(ctx, *msg); err != nil {
		ctx.Logger().Error("fail to process msg leave", "error", err)
		return nil, err
	}
	return &cosmos.Result{}, nil
}

func (h LeaveHandler) validate(ctx cosmos.Context, msg MsgLeave) error {
	version := h.mgr.GetVersion()
	switch {
	case version.GTE(semver.MustParse("0.1.0")):
		return h.validateV1(ctx, msg)
	default:
		return errBadVersion
	}
}

func (h LeaveHandler) validateV1(ctx cosmos.Context, msg MsgLeave) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}

	jail, err := h.mgr.Keeper().GetNodeAccountJail(ctx, msg.NodeAddress)
	if err != nil {
		// ignore this error and carry on. Don't want a jail bug causing node
		// accounts to not be able to get their funds out
		ctx.Logger().Error("fail to get node account jail", "error", err)
	}
	if jail.IsJailed(ctx) {
		return fmt.Errorf("failed to leave due to jail status: (release height %d) %s", jail.ReleaseHeight, jail.Reason)
	}

	return nil
}

func (h LeaveHandler) handle(ctx cosmos.Context, msg MsgLeave) error {
	version := h.mgr.GetVersion()
	switch {
	case version.GTE(semver.MustParse("2.137.0")):
		return h.handleV137(ctx, msg)
	case version.GTE(semver.MustParse("1.134.0")):
		return h.handleV134(ctx, msg)
	default:
		return errBadVersion
	}
}

func (h LeaveHandler) handleV137(ctx cosmos.Context, msg MsgLeave) error {
	nodeAcc, err := h.mgr.Keeper().GetNodeAccount(ctx, msg.NodeAddress)
	if err != nil {
		return ErrInternal(err, "fail to get node account by bond address")
	}
	if nodeAcc.IsEmpty() {
		return cosmos.ErrUnknownRequest("node account doesn't exist")
	}
	if !nodeAcc.BondAddress.Equals(msg.Tx.FromAddress) {
		// the msg was not sent from the node operator address
		// allow bond providers > minBond to issue leave
		// but first, make sure the node is active
		if nodeAcc.Status != NodeActive {
			return cosmos.ErrUnauthorized(fmt.Sprintf("%s is not authorized to manage %s when it is not Active", msg.Tx.FromAddress, msg.NodeAddress))
		}
		// check if message was sent by a bond provider with bond > minBond
		minBond := h.mgr.Keeper().GetConfigInt64(ctx, constants.MinimumBondInRune)
		minBondUint := cosmos.NewUint(uint64(minBond))
		var bondProviders BondProviders
		bondProviders, err = h.mgr.Keeper().GetBondProviders(ctx, msg.NodeAddress)
		if err != nil {
			return ErrInternal(err, "fail to get bond providers")
		}
		allowed := false
		for _, bondProvider := range bondProviders.Providers {
			var bpAccAddress cosmos.AccAddress
			bpAccAddress, err = msg.Tx.FromAddress.AccAddress()
			if err != nil {
				return ErrInternal(err, "fail to resolve bond provider AccAddress")
			}
			if bondProvider.BondAddress.Equals(bpAccAddress) && bondProvider.Bond.GTE(minBondUint) {
				allowed = true
				break
			}
		}
		if !allowed {
			return cosmos.ErrUnauthorized(fmt.Sprintf("%s are not authorized to manage %s", msg.Tx.FromAddress, msg.NodeAddress))
		}
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
