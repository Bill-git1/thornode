package thorchain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/blang/semver"
	"github.com/hashicorp/go-multierror"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
)

// TradeAccountWithdrawalHandler is handler to process MsgTradeAccountWithdrawal
type TradeAccountWithdrawalHandler struct {
	mgr Manager
}

// NewTradeAccountWithdrawalHandler create a new instance of TradeAccountWithdrawalHandler
func NewTradeAccountWithdrawalHandler(mgr Manager) TradeAccountWithdrawalHandler {
	return TradeAccountWithdrawalHandler{
		mgr: mgr,
	}
}

// Run is the main entry point for TradeAccountWithdrawalHandler
func (h TradeAccountWithdrawalHandler) Run(ctx cosmos.Context, m cosmos.Msg) (*cosmos.Result, error) {
	msg, ok := m.(*MsgTradeAccountWithdrawal)
	if !ok {
		return nil, errInvalidMessage
	}
	if err := h.validate(ctx, *msg); err != nil {
		ctx.Logger().Error("MsgTradeAccountWithdrawal failed validation", "error", err)
		return nil, err
	}
	err := h.handle(ctx, *msg)
	if err != nil {
		ctx.Logger().Error("fail to process MsgTradeAccountWithdrawal", "error", err)
	}
	return &cosmos.Result{}, err
}

func (h TradeAccountWithdrawalHandler) validate(ctx cosmos.Context, msg MsgTradeAccountWithdrawal) error {
	version := h.mgr.GetVersion()
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return h.validateV3_0_0(ctx, msg)
	default:
		return errBadVersion
	}
}

func (h TradeAccountWithdrawalHandler) validateV3_0_0(ctx cosmos.Context, msg MsgTradeAccountWithdrawal) error {
	tradeAccountsEnabled := h.mgr.Keeper().GetConfigInt64(ctx, constants.TradeAccountsEnabled)
	if tradeAccountsEnabled <= 0 {
		return fmt.Errorf("trade accounts are disabled")
	}
	return msg.ValidateBasic()
}

func (h TradeAccountWithdrawalHandler) handle(ctx cosmos.Context, msg MsgTradeAccountWithdrawal) error {
	version := h.mgr.GetVersion()
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return h.handleV3_0_0(ctx, msg)
	default:
		return errBadVersion
	}
}

// handle process MsgTradeAccountWithdrawal
func (h TradeAccountWithdrawalHandler) handleV3_0_0(ctx cosmos.Context, msg MsgTradeAccountWithdrawal) error {
	withdraw, err := h.mgr.TradeAccountManager().Withdrawal(ctx, msg.Asset, msg.Amount, msg.Signer, msg.AssetAddress, msg.Tx.ID)
	if err != nil {
		return err
	}

	var ok bool
	layer1Asset := msg.Asset.GetLayer1Asset()

	rawHash := sha256.Sum256(ctx.TxBytes())
	hash := hex.EncodeToString(rawHash[:])
	txID, err := common.NewTxID(hash)
	if err != nil {
		return err
	}
	toi := TxOutItem{
		Chain:     layer1Asset.GetChain(),
		InHash:    txID,
		ToAddress: msg.AssetAddress,
		Coin:      common.NewCoin(layer1Asset, withdraw),
	}

	ok, err = h.mgr.TxOutStore().TryAddTxOutItem(ctx, h.mgr, toi, cosmos.ZeroUint())
	if err != nil {
		return multierror.Append(errFailAddOutboundTx, err)
	}
	if !ok {
		return errFailAddOutboundTx
	}

	return nil
}
