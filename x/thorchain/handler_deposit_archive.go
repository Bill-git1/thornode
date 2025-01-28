package thorchain

import (
	"fmt"

	tmtypes "github.com/cometbft/cometbft/types"
	se "github.com/cosmos/cosmos-sdk/types/errors"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
)

func (h DepositHandler) handleV3_0_0(ctx cosmos.Context, msg MsgDeposit) (*cosmos.Result, error) {
	if h.mgr.Keeper().IsChainHalted(ctx, common.THORChain) {
		return nil, fmt.Errorf("unable to use MsgDeposit while THORChain is halted")
	}

	asset := msg.Coins[0].Asset

	switch {
	case asset.IsTradeAsset():
		balance := h.mgr.TradeAccountManager().BalanceOf(ctx, asset, msg.Signer)
		if msg.Coins[0].Amount.GT(balance) {
			return nil, se.ErrInsufficientFunds
		}
	case asset.IsSecuredAsset():
		balance := h.mgr.SecuredAssetManager().BalanceOf(ctx, asset, msg.Signer)
		if msg.Coins[0].Amount.GT(balance) {
			return nil, se.ErrInsufficientFunds
		}
	default:
		coins, err := msg.Coins.Native()
		if err != nil {
			return nil, ErrInternal(err, "coins are native to THORChain")
		}

		if !h.mgr.Keeper().HasCoins(ctx, msg.GetSigners()[0], coins) {
			return nil, se.ErrInsufficientFunds
		}
	}

	hash := tmtypes.Tx(ctx.TxBytes()).Hash()
	txID, err := common.NewTxID(fmt.Sprintf("%X", hash))
	if err != nil {
		return nil, fmt.Errorf("fail to get tx hash: %w", err)
	}
	existingVoter, err := h.mgr.Keeper().GetObservedTxInVoter(ctx, txID)
	if err != nil {
		return nil, fmt.Errorf("fail to get existing voter")
	}
	if len(existingVoter.Txs) > 0 {
		return nil, fmt.Errorf("txid: %s already exist", txID.String())
	}
	from, err := common.NewAddress(msg.GetSigners()[0].String())
	if err != nil {
		return nil, fmt.Errorf("fail to get from address: %w", err)
	}

	handler := NewInternalHandler(h.mgr)

	memo, err := ParseMemoWithTHORNames(ctx, h.mgr.Keeper(), msg.Memo)
	if err != nil {
		return nil, ErrInternal(err, "invalid memo")
	}

	if memo.IsOutbound() || memo.IsInternal() {
		return nil, fmt.Errorf("cannot send inbound an outbound or internal transaction")
	}

	var targetModule string
	switch memo.GetType() {
	case TxBond, TxUnBond, TxLeave:
		targetModule = BondName
	case TxReserve, TxTHORName:
		targetModule = ReserveName
	default:
		targetModule = AsgardName
	}
	coinsInMsg := msg.Coins
	if !coinsInMsg.IsEmpty() && !coinsInMsg[0].Asset.IsTradeAsset() && !coinsInMsg[0].Asset.IsSecuredAsset() {
		// send funds to target module
		// trunk-ignore(golangci-lint/govet): shadow
		err := h.mgr.Keeper().SendFromAccountToModule(ctx, msg.GetSigners()[0], targetModule, msg.Coins)
		if err != nil {
			return nil, err
		}
	}

	to, err := h.mgr.Keeper().GetModuleAddress(targetModule)
	if err != nil {
		return nil, fmt.Errorf("fail to get to address: %w", err)
	}

	tx := common.NewTx(txID, from, to, coinsInMsg, common.Gas{}, msg.Memo)
	tx.Chain = common.THORChain

	// construct msg from memo
	txIn := ObservedTx{Tx: tx}
	txInVoter := NewObservedTxVoter(txIn.Tx.ID, []ObservedTx{txIn})
	txInVoter.Height = ctx.BlockHeight() // While FinalisedHeight may be overwritten, Height records the consensus height
	txInVoter.FinalisedHeight = ctx.BlockHeight()
	txInVoter.Tx = txIn
	h.mgr.Keeper().SetObservedTxInVoter(ctx, txInVoter)

	m, txErr := processOneTxIn(ctx, h.mgr.Keeper(), txIn, msg.Signer)
	if txErr != nil {
		ctx.Logger().Error("fail to process native inbound tx", "error", txErr.Error(), "tx hash", tx.ID.String())
		return nil, txErr
	}

	// check if we've halted trading
	_, isSwap := m.(*MsgSwap)
	_, isAddLiquidity := m.(*MsgAddLiquidity)
	if isSwap || isAddLiquidity {
		if h.mgr.Keeper().IsTradingHalt(ctx, m) || h.mgr.Keeper().RagnarokInProgress(ctx) {
			return nil, fmt.Errorf("trading is halted")
		}
	}

	// if its a swap, send it to our queue for processing later
	if isSwap {
		msg, ok := m.(*MsgSwap)
		if ok {
			h.addSwap(ctx, *msg)
		}
		return &cosmos.Result{}, nil
	}

	// if it is a loan, inject the TxID and ToAddress into the context
	_, isLoanOpen := m.(*MsgLoanOpen)
	_, isLoanRepayment := m.(*MsgLoanRepayment)
	mCtx := ctx
	if isLoanOpen || isLoanRepayment {
		mCtx = ctx.WithValue(constants.CtxLoanTxID, txIn.Tx.ID)
		mCtx = mCtx.WithValue(constants.CtxLoanToAddress, txIn.Tx.ToAddress)
	}

	result, err := handler(mCtx, m)
	if err != nil {
		return nil, err
	}

	// if an outbound is not expected, mark the voter as done
	if !memo.GetType().HasOutbound() {
		// retrieve the voter from store in case the handler caused a change
		// trunk-ignore(golangci-lint/govet): shadow
		voter, err := h.mgr.Keeper().GetObservedTxInVoter(ctx, txID)
		if err != nil {
			return nil, fmt.Errorf("fail to get voter")
		}
		voter.SetDone()
		h.mgr.Keeper().SetObservedTxInVoter(ctx, voter)
	}
	return result, nil
}
