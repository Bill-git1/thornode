package thorchain

import (
	"math"

	protov2 "google.golang.org/protobuf/proto"

	. "gopkg.in/check.v1"

	cosmos "gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
)

type HandlerWasmExecuteContractSuite struct{}

var _ = Suite(&HandlerWasmExecuteContractSuite{})

// mockFeeTx implements sdk.FeeTx for testing
type mockFeeTx struct {
	fee cosmos.Coins
	gas uint64
}

func (m mockFeeTx) GetMsgs() []cosmos.Msg {
	return nil
}

func (m mockFeeTx) GetMsgsV2() ([]protov2.Message, error) {
	return nil, nil
}

func (m mockFeeTx) GetFee() cosmos.Coins {
	return m.fee
}

func (m mockFeeTx) GetGas() uint64 {
	return m.gas
}

func (m mockFeeTx) FeePayer() []byte {
	return nil
}

func (m mockFeeTx) FeeGranter() []byte {
	return nil
}

func (s *HandlerWasmExecuteContractSuite) TestCheckTxFeeWithValidatorMinGasPrices_GasOverflow(c *C) {
	ctx, k := setupKeeperForTest(c)
	// Enable check tx mode to trigger the gas price validation
	ctx = ctx.WithIsCheckTx(true)

	// Set a minimum gas price via Mimir
	k.SetMimir(ctx, constants.MimirKeyWasmMinGasPrice, 1000000000000000000) // 1 rune per gas unit

	// Test case 1: Normal gas value should work
	normalTx := mockFeeTx{
		fee: cosmos.NewCoins(cosmos.NewCoin("rune", cosmos.NewInt(1000000000))),
		gas: 1000,
	}
	_, _, err := checkTxFeeWithValidatorMinGasPrices(ctx, k, normalTx)
	// This may fail due to insufficient fees, but should not fail due to overflow
	// The point is it should not panic or have overflow issues
	if err != nil {
		c.Assert(err.Error(), Not(Matches), ".*exceeds maximum.*")
	}

	// Test case 2: Gas value at MaxInt64 boundary should work
	maxInt64Tx := mockFeeTx{
		fee: cosmos.NewCoins(cosmos.NewCoin("rune", cosmos.NewInt(1000000000000000000))),
		gas: uint64(math.MaxInt64),
	}
	_, _, err = checkTxFeeWithValidatorMinGasPrices(ctx, k, maxInt64Tx)
	// Should not fail with overflow error
	if err != nil {
		c.Assert(err.Error(), Not(Matches), ".*exceeds maximum.*")
	}

	// Test case 3: Gas value exceeding MaxInt64 should return error
	overflowTx := mockFeeTx{
		fee: cosmos.NewCoins(cosmos.NewCoin("rune", cosmos.NewInt(1000000000000000000))),
		gas: uint64(math.MaxInt64) + 1,
	}
	_, _, err = checkTxFeeWithValidatorMinGasPrices(ctx, k, overflowTx)
	c.Assert(err, NotNil)
	c.Assert(err.Error(), Matches, ".*exceeds maximum allowed value.*")

	// Test case 4: Maximum uint64 gas value should return error
	maxUint64Tx := mockFeeTx{
		fee: cosmos.NewCoins(cosmos.NewCoin("rune", cosmos.NewInt(1000000000000000000))),
		gas: math.MaxUint64,
	}
	_, _, err = checkTxFeeWithValidatorMinGasPrices(ctx, k, maxUint64Tx)
	c.Assert(err, NotNil)
	c.Assert(err.Error(), Matches, ".*exceeds maximum allowed value.*")
}

func (s *HandlerWasmExecuteContractSuite) TestCheckTxFeeWithValidatorMinGasPrices_NoCheckTx(c *C) {
	ctx, k := setupKeeperForTest(c)
	// Not in check tx mode - should skip gas price validation
	ctx = ctx.WithIsCheckTx(false)

	// Set a minimum gas price via Mimir
	k.SetMimir(ctx, constants.MimirKeyWasmMinGasPrice, 1000000000000000000)

	// Even with overflow gas, non-CheckTx should pass (no validation done)
	overflowTx := mockFeeTx{
		fee: cosmos.NewCoins(cosmos.NewCoin("rune", cosmos.NewInt(1))),
		gas: math.MaxUint64,
	}
	fees, _, err := checkTxFeeWithValidatorMinGasPrices(ctx, k, overflowTx)
	c.Assert(err, IsNil)
	c.Assert(fees, DeepEquals, overflowTx.fee)
}

func (s *HandlerWasmExecuteContractSuite) TestCheckTxFeeWithValidatorMinGasPrices_ZeroMinGasPrice(c *C) {
	ctx, k := setupKeeperForTest(c)
	ctx = ctx.WithIsCheckTx(true)

	// Set zero minimum gas price
	k.SetMimir(ctx, constants.MimirKeyWasmMinGasPrice, 0)

	// With zero min gas price, overflow check should not be triggered since minGasPrices.IsZero() is true
	overflowTx := mockFeeTx{
		fee: cosmos.NewCoins(cosmos.NewCoin("rune", cosmos.NewInt(1))),
		gas: math.MaxUint64,
	}
	fees, _, err := checkTxFeeWithValidatorMinGasPrices(ctx, k, overflowTx)
	c.Assert(err, IsNil)
	c.Assert(fees, DeepEquals, overflowTx.fee)
}
