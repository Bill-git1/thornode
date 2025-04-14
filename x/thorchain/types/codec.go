package types

import (
	"github.com/cosmos/cosmos-sdk/codec"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/msgservice"
)

var ModuleCdc = codec.NewLegacyAmino()

func init() {
	RegisterLegacyAminoCodec(ModuleCdc)
}

// RegisterCodec register the msg types for amino
func RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {
	cdc.RegisterConcrete(&MsgSwap{}, ModuleName+"/Swap", nil)
	cdc.RegisterConcrete(&MsgTssPool{}, ModuleName+"/TssPool", nil)
	cdc.RegisterConcrete(&MsgTssKeysignFail{}, ModuleName+"/TssKeysignFail", nil)
	cdc.RegisterConcrete(&MsgAddLiquidity{}, ModuleName+"/AddLiquidity", nil)
	cdc.RegisterConcrete(&MsgWithdrawLiquidity{}, ModuleName+"/WidthdrawLiquidity", nil)
	cdc.RegisterConcrete(&MsgObservedTxIn{}, ModuleName+"/ObservedTxIn", nil)
	cdc.RegisterConcrete(&MsgObservedTxOut{}, ModuleName+"/ObservedTxOut", nil)
	cdc.RegisterConcrete(&MsgDonate{}, ModuleName+"/MsgDonate", nil)
	cdc.RegisterConcrete(&MsgBond{}, ModuleName+"/MsgBond", nil)
	cdc.RegisterConcrete(&MsgUnBond{}, ModuleName+"/MsgUnBond", nil)
	cdc.RegisterConcrete(&MsgLeave{}, ModuleName+"/MsgLeave", nil)
	cdc.RegisterConcrete(&MsgNoOp{}, ModuleName+"/MsgNoOp", nil)
	cdc.RegisterConcrete(&MsgOutboundTx{}, ModuleName+"/MsgOutboundTx", nil)
	cdc.RegisterConcrete(&MsgSetVersion{}, ModuleName+"/MsgSetVersion", nil)
	cdc.RegisterConcrete(&MsgProposeUpgrade{}, ModuleName+"/MsgProposeUpgrade", nil)
	cdc.RegisterConcrete(&MsgApproveUpgrade{}, ModuleName+"/MsgApproveUpgrade", nil)
	cdc.RegisterConcrete(&MsgRejectUpgrade{}, ModuleName+"/MsgRejectUpgrade", nil)
	cdc.RegisterConcrete(&MsgSetNodeKeys{}, ModuleName+"/MsgSetNodeKeys", nil)
	cdc.RegisterConcrete(&MsgSetIPAddress{}, ModuleName+"/MsgSetIPAddress", nil)
	cdc.RegisterConcrete(&MsgReserveContributor{}, ModuleName+"/MsgReserveContributor", nil)
	cdc.RegisterConcrete(&MsgErrataTx{}, ModuleName+"/MsgErrataTx", nil)
	cdc.RegisterConcrete(&MsgBan{}, ModuleName+"/MsgBan", nil)
	cdc.RegisterConcrete(&MsgMimir{}, ModuleName+"/MsgMimir", nil)
	cdc.RegisterConcrete(&MsgDeposit{}, ModuleName+"/MsgDeposit", nil)
	cdc.RegisterConcrete(&MsgNetworkFee{}, ModuleName+"/MsgNetworkFee", nil)
	cdc.RegisterConcrete(&MsgMigrate{}, ModuleName+"/MsgMigrate", nil)
	cdc.RegisterConcrete(&MsgRagnarok{}, ModuleName+"/MsgRagnarok", nil)
	cdc.RegisterConcrete(&MsgRefundTx{}, ModuleName+"/MsgRefundTx", nil)
	cdc.RegisterConcrete(&MsgSend{}, ModuleName+"/MsgSend", nil)
	cdc.RegisterConcrete(&MsgNodePauseChain{}, ModuleName+"/MsgNodePauseChain", nil)
	cdc.RegisterConcrete(&MsgSolvency{}, ModuleName+"/MsgSolvency", nil)
	cdc.RegisterConcrete(&MsgManageTHORName{}, ModuleName+"/MsgManageTHORName", nil)
	cdc.RegisterConcrete(&MsgTradeAccountDeposit{}, ModuleName+"/MsgTradeAccountDeposit", nil)
	cdc.RegisterConcrete(&MsgTradeAccountWithdrawal{}, ModuleName+"/MsgTradeAccountWithdrawal", nil)
	cdc.RegisterConcrete(&MsgSecuredAssetDeposit{}, ModuleName+"/MsgSecuredAssetDeposit", nil)
	cdc.RegisterConcrete(&MsgSecuredAssetWithdraw{}, ModuleName+"/MsgSecuredAssetWithdraw", nil)
	cdc.RegisterConcrete(&MsgTCYClaim{}, ModuleName+"/MsgTCYClaim", nil)
	cdc.RegisterConcrete(&MsgTCYStake{}, ModuleName+"/MsgTCYStake", nil)
	cdc.RegisterConcrete(&MsgTCYUnstake{}, ModuleName+"/MsgTCYUnstake", nil)
}

// RegisterInterfaces register the types
func RegisterInterfaces(registry cdctypes.InterfaceRegistry) {
	registry.RegisterImplementations(
		(*sdk.Msg)(nil),
		&MsgSwap{},
		&MsgTssPool{},
		&MsgTssKeysignFail{},
		&MsgAddLiquidity{},
		&MsgWithdrawLiquidity{},
		&MsgObservedTxIn{},
		&MsgObservedTxOut{},
		&MsgDonate{},
		&MsgBond{},
		&MsgUnBond{},
		&MsgLeave{},
		&MsgNoOp{},
		&MsgOutboundTx{},
		&MsgSetVersion{},
		&MsgProposeUpgrade{},
		&MsgApproveUpgrade{},
		&MsgRejectUpgrade{},
		&MsgSetNodeKeys{},
		&MsgSetIPAddress{},
		&MsgReserveContributor{},
		&MsgErrataTx{},
		&MsgBan{},
		&MsgMimir{},
		&MsgDeposit{},
		&MsgNetworkFee{},
		&MsgMigrate{},
		&MsgRagnarok{},
		&MsgRefundTx{},
		&MsgSend{},
		&MsgNodePauseChain{},
		&MsgManageTHORName{},
		&MsgSolvency{},
		&MsgTradeAccountDeposit{},
		&MsgTradeAccountWithdrawal{},
		&MsgSecuredAssetDeposit{},
		&MsgSecuredAssetWithdraw{},
		&MsgTCYClaim{},
		&MsgTCYStake{},
		&MsgTCYUnstake{},
	)

	msgservice.RegisterMsgServiceDesc(registry, &_Msg_serviceDesc)
}
