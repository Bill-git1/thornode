//go:build regtest
// +build regtest

package thorchain

import (
	abci "github.com/tendermint/tendermint/abci/types"
	"gitlab.com/thorchain/thornode/common/cosmos"
	"gitlab.com/thorchain/thornode/x/thorchain/keeper"
)

func InitGenesis(ctx cosmos.Context, keeper keeper.Keeper, data GenesisState) []abci.ValidatorUpdate {
	validators := initGenesis(ctx, keeper, data)
	// TODO: Remove this regtest SetRUNEPool on hard fork (necessary to prevent rune-pool/rune-pool.yaml deposit panic)
	keeper.SetRUNEPool(ctx, NewRUNEPool())
	return validators[:1]
}
