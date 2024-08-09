//go:build !regtest
// +build !regtest

package app

import (
	"os"

	sdk "github.com/cosmos/cosmos-sdk/types"
	abci "github.com/tendermint/tendermint/abci/types"
	"gitlab.com/thorchain/thornode/config"
)

// BeginBlocker application updates every begin block
func (app *THORChainApp) BeginBlocker(ctx sdk.Context, req abci.RequestBeginBlock) abci.ResponseBeginBlock {
	haltHeight := config.GetThornode().Cosmos.HaltHeight
	if haltHeight > 0 && ctx.BlockHeight() > haltHeight {
		ctx.Logger().Info("halt height reached", "height", ctx.BlockHeight(), "halt height", haltHeight)
		os.Exit(0)
	}
	return app.mm.BeginBlock(ctx, req)
}

// EndBlocker application updates every end block
func (app *THORChainApp) EndBlocker(ctx sdk.Context, req abci.RequestEndBlock) abci.ResponseEndBlock {
	return app.mm.EndBlock(ctx, req)
}
