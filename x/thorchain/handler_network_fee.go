package thorchain

import (
	"context"

	"github.com/blang/semver"
	"github.com/cosmos/cosmos-sdk/telemetry"
	"github.com/hashicorp/go-metrics"

	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/keeper"
)

// NetworkFeeHandler a handler to process MsgNetworkFee messages
type NetworkFeeHandler struct {
	mgr Manager
}

// NewNetworkFeeHandler create a new instance of network fee handler
func NewNetworkFeeHandler(mgr Manager) NetworkFeeHandler {
	return NetworkFeeHandler{
		mgr: mgr,
	}
}

// Run is the main entry point for network fee logic
func (h NetworkFeeHandler) Run(ctx cosmos.Context, m cosmos.Msg) (*cosmos.Result, error) {
	msg, ok := m.(*MsgNetworkFee)
	if !ok {
		return nil, errInvalidMessage
	}
	if err := h.validate(ctx, *msg); err != nil {
		ctx.Logger().Error("MsgNetworkFee failed validation", "error", err)
		return nil, err
	}
	result, err := h.handle(ctx, *msg)
	if err != nil {
		ctx.Logger().Error("fail to process MsgNetworkFee", "error", err)
	}
	return result, err
}

func (h NetworkFeeHandler) validate(ctx cosmos.Context, msg MsgNetworkFee) error {
	version := h.mgr.GetVersion()
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return h.validateV3_0_0(ctx, msg)
	default:
		return errBadVersion
	}
}

func (h NetworkFeeHandler) validateV3_0_0(ctx cosmos.Context, msg MsgNetworkFee) error {
	// ValidateBasic is also executed in message service router's handler and isn't versioned there
	if err := msg.ValidateBasic(); err != nil {
		return err
	}

	if !isSignedByActiveNodeAccounts(ctx, h.mgr.Keeper(), msg.GetSigners()) {
		return cosmos.ErrUnauthorized(errNotAuthorized.Error())
	}
	return nil
}

// handle process MsgNetworkFee
func (h NetworkFeeHandler) handle(ctx cosmos.Context, msg MsgNetworkFee) (*cosmos.Result, error) {
	version := h.mgr.GetVersion()
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return h.handleV3_0_0(ctx, msg)
	default:
		return nil, errBadVersion
	}
}

func (h NetworkFeeHandler) handleV3_0_0(ctx cosmos.Context, msg MsgNetworkFee) (*cosmos.Result, error) {
	active, err := h.mgr.Keeper().ListActiveValidators(ctx)
	if err != nil {
		err = wrapError(ctx, err, "fail to get list of active node accounts")
		return nil, err
	}

	voter, err := h.mgr.Keeper().GetObservedNetworkFeeVoter(ctx, msg.BlockHeight, msg.Chain, int64(msg.TransactionFeeRate), int64(msg.TransactionSize))
	if err != nil {
		return nil, err
	}
	observeSlashPoints := h.mgr.GetConstants().GetInt64Value(constants.ObserveSlashPoints)
	lackOfObservationPenalty := h.mgr.GetConstants().GetInt64Value(constants.LackOfObservationPenalty)
	observeFlex := h.mgr.Keeper().GetConfigInt64(ctx, constants.ObservationDelayFlexibility)

	slashCtx := ctx.WithContext(context.WithValue(ctx.Context(), constants.CtxMetricLabels, []metrics.Label{
		telemetry.NewLabel("reason", "failed_observe_network_fee"),
		telemetry.NewLabel("chain", string(msg.Chain)),
	}))

	if !voter.Sign(msg.Signer) {
		// Slash for the network having to handle the extra message/s.
		h.mgr.Slasher().IncSlashPoints(slashCtx, observeSlashPoints, msg.Signer)
		ctx.Logger().Info("signer already signed MsgNetworkFee", "signer", msg.Signer.String(), "block height", msg.BlockHeight, "chain", msg.Chain.String())
		return &cosmos.Result{}, nil
	}
	h.mgr.Keeper().SetObservedNetworkFeeVoter(ctx, voter)
	// doesn't have consensus yet
	if !voter.HasConsensus(active) {
		// Before consensus, slash until consensus.
		h.mgr.Slasher().IncSlashPoints(slashCtx, observeSlashPoints, msg.Signer)
		return &cosmos.Result{}, nil
	}

	if voter.BlockHeight > 0 {
		// After consensus, only decrement slash points if within the ObservationDelayFlexibility period.
		if (voter.BlockHeight + observeFlex) >= ctx.BlockHeight() {
			h.mgr.Slasher().DecSlashPoints(slashCtx, lackOfObservationPenalty, msg.Signer)
		}
		// MsgNetworkFee tx already processed
		return &cosmos.Result{}, nil
	}

	voter.BlockHeight = ctx.BlockHeight()
	h.mgr.Keeper().SetObservedNetworkFeeVoter(ctx, voter)

	// This signer brings the voter to consensus; increment the signer's slash points like the before-consensus signers,
	// then decrement all the signers' slash points and increment the non-signers' slash points.
	h.mgr.Slasher().IncSlashPoints(slashCtx, observeSlashPoints, msg.Signer)
	signers := voter.GetSigners()
	nonSigners := getNonSigners(active, signers)
	h.mgr.Slasher().DecSlashPoints(slashCtx, observeSlashPoints, signers...)
	h.mgr.Slasher().IncSlashPoints(slashCtx, lackOfObservationPenalty, nonSigners...)

	ctx.Logger().Info("update network fee", "chain", msg.Chain.String(), "transaction-size", msg.TransactionSize, "fee-rate", msg.TransactionFeeRate)
	if err := h.mgr.Keeper().SaveNetworkFee(ctx, msg.Chain, NetworkFee{
		Chain:              msg.Chain,
		TransactionSize:    msg.TransactionSize,
		TransactionFeeRate: msg.TransactionFeeRate,
	}); err != nil {
		return nil, ErrInternal(err, "fail to save network fee")
	}
	return &cosmos.Result{}, nil
}

// NetworkFeeAnteHandler called by the ante handler to gate mempool entry
// and also during deliver. Store changes will persist if this function
// succeeds, regardless of the success of the transaction.
func NetworkFeeAnteHandler(ctx cosmos.Context, v semver.Version, k keeper.Keeper, msg MsgNetworkFee) error {
	if !isSignedByActiveNodeAccounts(ctx, k, msg.GetSigners()) {
		return cosmos.ErrUnauthorized(errNotAuthorized.Error())
	}
	return nil
}
