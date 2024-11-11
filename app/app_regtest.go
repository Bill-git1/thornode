//go:build regtest
// +build regtest

package app

import (
	"net/http"
	"os"
	"strconv"

	"github.com/rs/zerolog/log"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

var (
	begin = make(chan struct{})
	end   = make(chan struct{})
)

func init() {
	count := 0

	// start an http server to unblock a block creation when a request is received
	newBlock := func(w http.ResponseWriter, r *http.Request) {
		begin <- struct{}{}
		<-end
		count++
		w.Write([]byte(strconv.Itoa(count)))
	}
	http.HandleFunc("/newBlock", newBlock)
	portString := os.Getenv("CREATE_BLOCK_PORT")
	go func() {
		err := http.ListenAndServe(":"+portString, nil)
		if err != nil {
			log.Fatal().Err(err).Msg("fail to start http server")
		}
	}()
}

func (app *THORChainApp) BeginBlocker(ctx sdk.Context) (sdk.BeginBlock, error) {
	<-begin
	return app.ModuleManager.BeginBlock(ctx)
}

// EndBlocker application updates every end block
func (app *THORChainApp) EndBlocker(ctx sdk.Context) (sdk.EndBlock, error) {
	defer func() { end <- struct{}{} }()
	return app.ModuleManager.EndBlock(ctx)
}
