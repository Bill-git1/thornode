package thorchain

import (
	"fmt"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

func (qs queryServer) queryReferenceMemo(ctx cosmos.Context, req *types.QueryReferenceMemoRequest) (*types.QueryReferenceMemoResponse, error) {
	asset, err := common.NewAsset(req.Asset)
	if err != nil {
		return nil, fmt.Errorf("invalid asset: %w", err)
	}

	if req.Reference == "" {
		return nil, fmt.Errorf("reference cannot be empty")
	}

	memo, err := qs.mgr.Keeper().GetReferenceMemo(ctx, asset, req.Reference)
	if err != nil {
		return nil, fmt.Errorf("failed to get reference memo: %w", err)
	}

	return &types.QueryReferenceMemoResponse{
		Asset:            memo.Asset,
		Memo:             memo.Memo,
		Reference:        memo.Reference,
		Height:           memo.Height,
		RegistrationHash: memo.RegistrationHash,
		RegisteredBy:     memo.RegisteredBy,
	}, nil
}

func (qs queryServer) queryReferenceMemoByHash(ctx cosmos.Context, req *types.QueryReferenceMemoByHashRequest) (*types.QueryReferenceMemoByHashResponse, error) {
	if req.Hash == "" {
		return nil, fmt.Errorf("hash cannot be empty")
	}

	hash, err := common.NewTxID(req.Hash)
	if err != nil {
		return nil, fmt.Errorf("invalid hash: %w", err)
	}

	memo, err := qs.mgr.Keeper().GetReferenceMemoByTxnHash(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("failed to get reference memo by hash: %w", err)
	}

	return &types.QueryReferenceMemoByHashResponse{
		Asset:            memo.Asset,
		Memo:             memo.Memo,
		Reference:        memo.Reference,
		Height:           memo.Height,
		RegistrationHash: memo.RegistrationHash,
		RegisteredBy:     memo.RegisteredBy,
	}, nil
}

func (qs queryServer) queryReferenceMemoPreflight(ctx cosmos.Context, req *types.QueryReferenceMemoPreflightRequest) (*types.QueryReferenceMemoPreflightResponse, error) {
	asset, err := common.NewAsset(req.Asset)
	if err != nil {
		return nil, fmt.Errorf("invalid asset: %w", err)
	}

	amount, err := cosmos.ParseUint(req.Amount)
	if err != nil {
		return nil, fmt.Errorf("invalid amount: %w", err)
	}

	// Calculate the reference ID that would be generated from this amount
	reference, err := ExtractReferenceFromAmount(ctx, qs.mgr, asset, amount.Uint64())
	if err != nil {
		return nil, fmt.Errorf("failed to calculate reference: %w", err)
	}

	// Query the reference memo to check availability
	refMemo, err := qs.mgr.Keeper().GetReferenceMemo(ctx, asset, reference)
	if err != nil {
		return nil, fmt.Errorf("failed to get reference memo: %w", err)
	}

	response := &types.QueryReferenceMemoPreflightResponse{
		Reference:  reference,
		UsageCount: refMemo.GetUsageCount(),
		MaxUse:     qs.mgr.Keeper().GetConfigInt64(ctx, constants.MemolessTxnMaxUse),
	}

	// Check if the reference is available (not registered or expired)
	ttl := qs.mgr.Keeper().GetConfigInt64(ctx, constants.MemolessTxnTTL)
	if refMemo.Height == 0 || refMemo.IsExpired(ctx.BlockHeight(), ttl) {
		response.Available = true
		response.CanRegister = true
		response.ExpiresAt = 0
	} else {
		response.Available = false
		response.Memo = refMemo.Memo
		response.ExpiresAt = refMemo.Height + ttl

		// Can register if it's expired
		response.CanRegister = refMemo.IsExpired(ctx.BlockHeight(), ttl)
	}

	return response, nil
}
