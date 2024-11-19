package thorchain

import (
	"github.com/blang/semver"

	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256r1"
	"github.com/cosmos/cosmos-sdk/crypto/types/multisig"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"

	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/keeper"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

type AnteDecorator struct {
	keeper keeper.Keeper
}

func NewAnteDecorator(keeper keeper.Keeper) AnteDecorator {
	return AnteDecorator{
		keeper: keeper,
	}
}

func (ad AnteDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (newCtx sdk.Context, err error) {
	if err = ad.rejectMultipleDepositMsgs(ctx, tx.GetMsgs()); err != nil {
		return ctx, err
	}

	// TODO remove on hard fork, when all signers will be allowed (v47+)
	if err = ad.rejectInvalidSigners(tx); err != nil {
		return ctx, err
	}

	// run the message-specific ante for each msg, all must succeed
	version, _ := ad.keeper.GetVersionWithCtx(ctx)
	for _, msg := range tx.GetMsgs() {
		if err = ad.anteHandleMessage(ctx, version, msg); err != nil {
			return ctx, err
		}
	}

	return next(ctx, tx, simulate)
}

// rejectInvalidSigners reject txs if they are signed with secp256r1 keys
func (ad AnteDecorator) rejectInvalidSigners(tx sdk.Tx) error {
	sigTx, okTx := tx.(authsigning.SigVerifiableTx)
	if !okTx {
		return cosmos.ErrUnknownRequest("invalid transaction type")
	}
	sigs, err := sigTx.GetSignaturesV2()
	if err != nil {
		return err
	}
	for _, sig := range sigs {
		pubkey := sig.PubKey
		switch pubkey := pubkey.(type) {
		case *secp256r1.PubKey:
			return cosmos.ErrUnknownRequest("secp256r1 keys not allowed")
		case multisig.PubKey:
			for _, pk := range pubkey.GetPubKeys() {
				if _, okPk := pk.(*secp256r1.PubKey); okPk {
					return cosmos.ErrUnknownRequest("secp256r1 keys not allowed")
				}
			}
		}
	}
	return nil
}

// rejectMultipleDepositMsgs only one deposit msg allowed per tx
func (ad AnteDecorator) rejectMultipleDepositMsgs(ctx cosmos.Context, msgs []cosmos.Msg) error {
	hasDeposit := false
	for _, msg := range msgs {
		switch msg.(type) {
		case *types.MsgDeposit:
			if hasDeposit {
				return cosmos.ErrUnknownRequest("only one deposit msg per tx")
			}
			hasDeposit = true
		default:
			continue
		}
	}
	return nil
}

// anteHandleMessage calls the msg-specific ante handling for a given msg
func (ad AnteDecorator) anteHandleMessage(ctx sdk.Context, version semver.Version, msg cosmos.Msg) error {
	// ideally each handler would impl an ante func and we could instantiate
	// handlers and call ante, but handlers require mgr which is unavailable
	switch m := msg.(type) {

	// consensus handlers
	case *types.MsgBan:
		return BanAnteHandler(ctx, version, ad.keeper, *m)
	case *types.MsgErrataTx:
		return ErrataTxAnteHandler(ctx, version, ad.keeper, *m)
	case *types.MsgNetworkFee:
		return NetworkFeeAnteHandler(ctx, version, ad.keeper, *m)
	case *types.MsgObservedTxIn:
		return ObservedTxInAnteHandler(ctx, version, ad.keeper, *m)
	case *types.MsgObservedTxOut:
		return ObservedTxOutAnteHandler(ctx, version, ad.keeper, *m)
	case *types.MsgSolvency:
		return SolvencyAnteHandler(ctx, version, ad.keeper, *m)
	case *types.MsgTssPool:
		return TssAnteHandler(ctx, version, ad.keeper, *m)
	case *types.MsgTssKeysignFail:
		return TssKeysignFailAnteHandler(ctx, version, ad.keeper, *m)

	// cli handlers (non-consensus)
	case *types.MsgSetIPAddress:
		return IPAddressAnteHandler(ctx, version, ad.keeper, *m)
	case *types.MsgMimir:
		return MimirAnteHandler(ctx, version, ad.keeper, *m)
	case *types.MsgNodePauseChain:
		return NodePauseChainAnteHandler(ctx, version, ad.keeper, *m)
	case *types.MsgSetNodeKeys:
		return SetNodeKeysAnteHandler(ctx, version, ad.keeper, *m)
	case *types.MsgSetVersion:
		return VersionAnteHandler(ctx, version, ad.keeper, *m)
	case *types.MsgProposeUpgrade, *types.MsgApproveUpgrade, *types.MsgRejectUpgrade:
		if version.GTE(semver.MustParse("2.136.0")) {
			legacyMsg, ok := msg.(sdk.LegacyMsg)
			if !ok {
				return cosmos.ErrUnknownRequest("invalid message type")
			}
			return ActiveValidatorAnteHandler(ctx, version, ad.keeper, legacyMsg.GetSigners()[0])
		}
		return cosmos.ErrUnknownRequest("invalid message type")

	// native handlers (non-consensus)
	case *types.MsgDeposit:
		return DepositAnteHandler(ctx, version, ad.keeper, *m)
	case *types.MsgSend:
		return SendAnteHandler(ctx, version, ad.keeper, *m)

	default:
		return cosmos.ErrUnknownRequest("invalid message type")
	}
}

// InfiniteGasDecorator uses an infinite gas meter to prevent out-of-gas panics
// and allow non-versioned changes to be made without breaking consensus,
// as long as the resulting state is consistent.
type InfiniteGasDecorator struct {
	keeper keeper.Keeper
}

func NewInfiniteGasDecorator(keeper keeper.Keeper) InfiniteGasDecorator {
	return InfiniteGasDecorator{
		keeper: keeper,
	}
}

func (d InfiniteGasDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (newCtx sdk.Context, err error) {
	ctx = ctx.WithGasMeter(storetypes.NewInfiniteGasMeter())
	return next(ctx, tx, simulate)
}
