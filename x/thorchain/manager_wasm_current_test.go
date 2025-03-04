package thorchain

import (
	"os"
	"path/filepath"
	"time"

	"github.com/CosmWasm/wasmd/x/wasm/ioutils"
	"github.com/cosmos/cosmos-sdk/types"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
	. "gopkg.in/check.v1"
)

type WasmManagerVCURSuite struct{}

var _ = Suite(&WasmManagerVCURSuite{})

func (s WasmManagerVCURSuite) TestStoreCode(c *C) {
	ctx, mgr := setupManagerForTest(c)
	mgr.K.SetMimir(ctx, constants.MimirKeyWasmPermissionless, 0)

	_, _, err := mgr.WasmManager().StoreCode(ctx,
		GetRandomBech32Addr(),
		s.loadWasm(c, "simple.wasm"),
	)
	c.Assert(err, IsNil)
}

func (s WasmManagerVCURSuite) TestInstantiateContract(c *C) {
	ctx, mgr := setupManagerForTest(c)
	ctx = ctx.WithBlockTime(time.Unix(1, 0))
	mgr.K.SetMimir(ctx, constants.MimirKeyWasmPermissionless, 0)

	_, _, err := mgr.WasmManager().StoreCode(ctx,
		GetRandomBech32Addr(),
		s.loadWasm(c, "counter.wasm"),
	)
	c.Assert(err, IsNil)

	_, _, err = mgr.WasmManager().StoreCode(ctx,
		GetRandomBech32Addr(),
		s.loadWasm(c, "extended.wasm"),
	)
	c.Assert(err, IsNil)

	_, _, err = mgr.WasmManager().InstantiateContract(ctx,
		1,
		GetRandomBech32Addr(),
		nil,
		[]byte(`{}`),
		"label",
		[]types.Coin{},
	)
	c.Assert(err, IsNil)

	_, _, err = mgr.WasmManager().InstantiateContract(ctx,
		1,
		GetRandomBech32Addr(),
		nil,
		[]byte(`{}`),
		"label 2",
		[]types.Coin{},
	)
	c.Assert(err, IsNil)

	_, _, err = mgr.WasmManager().InstantiateContract(ctx,
		2,
		GetRandomBech32Addr(),
		nil,
		[]byte(`{}`),
		"label",
		[]types.Coin{},
	)
	c.Assert(err, IsNil)

	_, _, err = mgr.WasmManager().InstantiateContract(ctx,
		2,
		GetRandomBech32Addr(),
		nil,
		[]byte(`{}`),
		"label",
		[]types.Coin{},
	)
	c.Assert(err, IsNil)
}

func (s WasmManagerVCURSuite) TestMigrateCode(c *C) {
	ctx, mgr := setupManagerForTest(c)
	ctx = ctx.WithBlockTime(time.Unix(1, 0))
	mgr.K.SetMimir(ctx, constants.MimirKeyWasmPermissionless, 0)

	_, _, err := mgr.WasmManager().StoreCode(ctx,
		GetRandomBech32Addr(),
		s.loadWasm(c, "simple.wasm"),
	)
	c.Assert(err, IsNil)

	_, _, err = mgr.WasmManager().StoreCode(ctx,
		GetRandomBech32Addr(),
		s.loadWasm(c, "extended.wasm"),
	)
	c.Assert(err, IsNil)

	admin := GetRandomBech32Addr()
	_, _, err = mgr.WasmManager().InstantiateContract(ctx,
		1,
		admin,
		admin,
		[]byte(`{}`),
		"label",
		[]types.Coin{},
	)
	c.Assert(err, IsNil)

	_, err = mgr.WasmManager().MigrateContract(ctx,
		cosmos.MustAccAddressFromBech32("tthor14hj2tavq8fpesdwxxcu44rty3hh90vhujrvcmstl4zr3txmfvw9sw58u9f"),
		admin,
		2,
		[]byte(`{}`),
	)

	c.Assert(err, IsNil)
}

func (s WasmManagerVCURSuite) loadWasm(c *C, file string) []byte {
	wasmPath := filepath.Join("../../test/fixtures/wasm", file)
	wasm, err := os.ReadFile(wasmPath)
	c.Assert(err, IsNil)
	wasm, err = ioutils.GzipIt(wasm)
	c.Assert(err, IsNil)
	return wasm
}
