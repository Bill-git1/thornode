package params

import (
	"github.com/cosmos/gogoproto/proto"

	"cosmossdk.io/x/tx/signing"

	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/auth/tx"

	"gitlab.com/thorchain/thornode/v3/x/thorchain"
)

// MakeEncodingConfig creates an EncodingConfig for an amino based test configuration.
func MakeEncodingConfig() EncodingConfig {
	amino := codec.NewLegacyAmino()

	interfaceRegistrySigningOptions := signing.Options{
		AddressCodec: address.Bech32Codec{
			Bech32Prefix: sdk.GetConfig().GetBech32AccountAddrPrefix(),
		},
		ValidatorAddressCodec: address.Bech32Codec{
			Bech32Prefix: sdk.GetConfig().GetBech32ValidatorAddrPrefix(),
		},
	}
	thorchain.DefineCustomGetSigners(&interfaceRegistrySigningOptions)
	interfaceRegistry, err := types.NewInterfaceRegistryWithOptions(types.InterfaceRegistryOptions{
		ProtoFiles:     proto.HybridResolver,
		SigningOptions: interfaceRegistrySigningOptions,
	})
	if err != nil {
		panic(err)
	}

	marshaler := codec.NewProtoCodec(interfaceRegistry)

	txSigningOptions, err := tx.NewDefaultSigningOptions()
	if err != nil {
		panic(err)
	}
	thorchain.DefineCustomGetSigners(txSigningOptions)
	txConfigOpts := tx.ConfigOptions{
		EnabledSignModes: tx.DefaultSignModes,
		SigningOptions:   txSigningOptions,
	}
	txCfg, err := tx.NewTxConfigWithOptions(
		marshaler,
		txConfigOpts,
	)
	if err != nil {
		panic(err)
	}
	return EncodingConfig{
		InterfaceRegistry: interfaceRegistry,
		Codec:             marshaler,
		TxConfig:          txCfg,
		Amino:             amino,
	}
}
