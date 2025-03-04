package thorchain

import (
	"context"

	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
)

func (ms msgServer) StoreCode(goCtx context.Context, msg *wasmtypes.MsgStoreCode) (*wasmtypes.MsgStoreCodeResponse, error) {
	handler := NewWasmStoreCodeHandler(ms.mgr)
	_, err := externalHandler(goCtx, handler, msg)
	return &wasmtypes.MsgStoreCodeResponse{}, err
}

func (ms msgServer) InstantiateContract(goCtx context.Context, msg *wasmtypes.MsgInstantiateContract) (*wasmtypes.MsgInstantiateContractResponse, error) {
	handler := NewWasmInstantiateContractHandler(ms.mgr)
	_, err := externalHandler(goCtx, handler, msg)
	return &wasmtypes.MsgInstantiateContractResponse{}, err
}

func (ms msgServer) InstantiateContract2(goCtx context.Context, msg *wasmtypes.MsgInstantiateContract2) (*wasmtypes.MsgInstantiateContract2Response, error) {
	handler := NewWasmInstantiateContract2Handler(ms.mgr)
	_, err := externalHandler(goCtx, handler, msg)
	return &wasmtypes.MsgInstantiateContract2Response{}, err
}

func (ms msgServer) ExecuteContract(goCtx context.Context, msg *wasmtypes.MsgExecuteContract) (*wasmtypes.MsgExecuteContractResponse, error) {
	handler := NewWasmExecuteContractHandler(ms.mgr)
	_, err := externalHandler(goCtx, handler, msg)
	return &wasmtypes.MsgExecuteContractResponse{}, err
}

func (ms msgServer) MigrateContract(goCtx context.Context, msg *wasmtypes.MsgMigrateContract) (*wasmtypes.MsgMigrateContractResponse, error) {
	handler := NewWasmMigrateContractHandler(ms.mgr)
	_, err := externalHandler(goCtx, handler, msg)
	return &wasmtypes.MsgMigrateContractResponse{}, err
}

func (ms msgServer) SudoContract(goCtx context.Context, msg *wasmtypes.MsgSudoContract) (*wasmtypes.MsgSudoContractResponse, error) {
	handler := NewWasmSudoContractHandler(ms.mgr)
	_, err := externalHandler(goCtx, handler, msg)
	return &wasmtypes.MsgSudoContractResponse{}, err
}
