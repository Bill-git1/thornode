package abci

import (
	abci "github.com/cometbft/cometbft/abci/types"
	cmttypes "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	ebifrost "gitlab.com/thorchain/thornode/v3/x/thorchain/ebifrost"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/keeper"
)

type ProposalHandler struct {
	keeper  *keeper.Keeper
	bifrost *ebifrost.EnshrinedBifrost

	prepareProposalHandler sdk.PrepareProposalHandler
	processProposalHandler sdk.ProcessProposalHandler
}

func NewProposalHandler(
	k *keeper.Keeper,
	b *ebifrost.EnshrinedBifrost,
	nextPrepareProposalHandler sdk.PrepareProposalHandler,
	nextProcessProposalHandler sdk.ProcessProposalHandler,
) *ProposalHandler {
	return &ProposalHandler{
		keeper:                 k,
		bifrost:                b,
		prepareProposalHandler: nextPrepareProposalHandler,
		processProposalHandler: nextProcessProposalHandler,
	}
}

func (h *ProposalHandler) PrepareProposal(ctx sdk.Context, req *abci.RequestPrepareProposal) (*abci.ResponsePrepareProposal, error) {
	injectTxs, txBzLen := h.bifrost.ProposalInjectTxs(ctx, req.MaxTxBytes)

	// Modify request for upstream handler with reduced max tx size
	origMaxTxBytes := req.MaxTxBytes
	req.MaxTxBytes -= txBzLen

	// TODO remove this after upgrading to cosmos sdk that has this fix
	// https://github.com/cosmos/cosmos-sdk/pull/24074
	// Note: not critical to do ASAP but will open up some space for more txs
	var toRemove int64
	for _, txBz := range req.Txs {
		amount := int64(len(txBz))
		protoAmount := cmttypes.ComputeProtoSizeForTxs([]cmttypes.Tx{txBz})
		toRemove += protoAmount - amount
	}
	req.MaxTxBytes -= toRemove
	// END TODO

	// Let default handler process original txs with reduced size
	resp, err := h.prepareProposalHandler(ctx, req)
	if err != nil {
		return nil, err
	}

	// Combine ebifrost inject txs with the ones selected by default handler, up to tx limit
	combinedTxs := injectTxs

	defaultTxsSize := int64(0)
	totalSize := txBzLen
	for _, tx := range resp.Txs {
		txSize := cmttypes.ComputeProtoSizeForTxs([]cmttypes.Tx{tx})
		defaultTxsSize += txSize
		if totalSize+txSize <= origMaxTxBytes {
			totalSize += txSize
			combinedTxs = append(combinedTxs, tx)
		} else {
			ctx.Logger().Warn(
				"Dropping transaction that would exceed block size limit",
				"current_size", totalSize,
				"tx_size", txSize,
				"max_size", origMaxTxBytes,
			)
		}
	}

	ctx.Logger().Info(
		"Proposal Transaction sizes",
		"injected_txs_count", len(injectTxs),
		"injected_txs_size", txBzLen,
		"default_txs_count", len(resp.Txs),
		"default_txs_size", defaultTxsSize,
		"final_txs_size", totalSize,
		"final_txs_count", len(combinedTxs),
		"max_bytes", req.MaxTxBytes,
		"original_max_bytes", origMaxTxBytes,
	)

	return &abci.ResponsePrepareProposal{Txs: combinedTxs}, nil
}

func (h *ProposalHandler) ProcessProposal(ctx sdk.Context, req *abci.RequestProcessProposal) (*abci.ResponseProcessProposal, error) {
	return h.processProposalHandler(ctx, req)
}
