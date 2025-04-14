package thorchain

import (
	"fmt"
	"math/big"
	"strings"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/keeper"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"

	"github.com/blang/semver"
)

// TXTYPE:STATE1:STATE2:STATE3:FINALMEMO

type TxType uint8

const (
	TxUnknown TxType = iota
	TxAdd
	TxWithdraw
	TxSwap
	TxLimitOrder
	TxOutbound
	TxDonate
	TxBond
	TxUnbond
	TxLeave
	TxReserve
	TxRefund
	TxMigrate
	TxRagnarok
	TxNoOp
	TxConsolidate
	TxTHORName
	TxLoanOpen
	TxLoanRepayment
	TxTradeAccountDeposit
	TxTradeAccountWithdrawal
	TxSecuredAssetDeposit
	TxSecuredAssetWithdraw
	TxRunePoolDeposit
	TxRunePoolWithdraw
	TxExec
	TxSwitch
	TxTCYClaim
	TxTCYStake
	TxTCYUnstake
)

var stringToTxTypeMap = map[string]TxType{
	"add":         TxAdd,
	"+":           TxAdd,
	"withdraw":    TxWithdraw,
	"wd":          TxWithdraw,
	"-":           TxWithdraw,
	"swap":        TxSwap,
	"s":           TxSwap,
	"=":           TxSwap,
	"limito":      TxLimitOrder,
	"lo":          TxLimitOrder,
	"out":         TxOutbound,
	"donate":      TxDonate,
	"d":           TxDonate,
	"bond":        TxBond,
	"unbond":      TxUnbond,
	"leave":       TxLeave,
	"reserve":     TxReserve,
	"refund":      TxRefund,
	"migrate":     TxMigrate,
	"ragnarok":    TxRagnarok,
	"noop":        TxNoOp,
	"consolidate": TxConsolidate,
	"name":        TxTHORName,
	"n":           TxTHORName,
	"~":           TxTHORName,
	"$+":          TxLoanOpen,
	"loan+":       TxLoanOpen,
	"$-":          TxLoanRepayment,
	"loan-":       TxLoanRepayment,
	"trade+":      TxTradeAccountDeposit,
	"trade-":      TxTradeAccountWithdrawal,
	"secure+":     TxSecuredAssetDeposit,
	"secure-":     TxSecuredAssetWithdraw,
	"pool+":       TxRunePoolDeposit,
	"pool-":       TxRunePoolWithdraw,
	"x":           TxExec,
	"exec":        TxExec,
	"switch":      TxSwitch,
	"tcy":         TxTCYClaim,
	"tcy+":        TxTCYStake,
	"tcy-":        TxTCYUnstake,
}

var txToStringMap = map[TxType]string{
	TxAdd:                    "add",
	TxWithdraw:               "withdraw",
	TxSwap:                   "swap",
	TxLimitOrder:             "limito",
	TxOutbound:               "out",
	TxRefund:                 "refund",
	TxDonate:                 "donate",
	TxBond:                   "bond",
	TxUnbond:                 "unbond",
	TxLeave:                  "leave",
	TxReserve:                "reserve",
	TxMigrate:                "migrate",
	TxRagnarok:               "ragnarok",
	TxNoOp:                   "noop",
	TxConsolidate:            "consolidate",
	TxTHORName:               "thorname",
	TxLoanOpen:               "$+",
	TxLoanRepayment:          "$-",
	TxTradeAccountDeposit:    "trade+",
	TxTradeAccountWithdrawal: "trade-",
	TxSecuredAssetDeposit:    "secure+",
	TxSecuredAssetWithdraw:   "secure-",
	TxExec:                   "x",
	TxSwitch:                 "switch",
	TxTCYClaim:               "tcy",
	TxTCYStake:               "tcy+",
	TxTCYUnstake:             "tcy-",
}

// converts a string into a txType
func StringToTxType(s string) (TxType, error) {
	// THORNode can support Abbreviated MEMOs , usually it is only one character
	sl := strings.ToLower(s)
	if t, ok := stringToTxTypeMap[sl]; ok {
		return t, nil
	}

	return TxUnknown, fmt.Errorf("invalid tx type: %s", s)
}

func (tx TxType) IsInbound() bool {
	switch tx {
	case TxAdd,
		TxWithdraw,
		TxTradeAccountDeposit,
		TxTradeAccountWithdrawal,
		TxRunePoolDeposit,
		TxRunePoolWithdraw,
		TxSecuredAssetDeposit,
		TxSecuredAssetWithdraw,
		TxSwap,
		TxLimitOrder,
		TxDonate,
		TxBond,
		TxUnbond,
		TxLeave,
		TxReserve,
		TxNoOp,
		TxTHORName,
		TxLoanOpen,
		TxLoanRepayment,
		TxExec,
		TxSwitch,
		TxTCYClaim,
		TxTCYStake,
		TxTCYUnstake:
		return true
	default:
		return false
	}
}

func (tx TxType) IsOutbound() bool {
	switch tx {
	case TxOutbound, TxRefund, TxRagnarok:
		return true
	default:
		return false
	}
}

func (tx TxType) IsInternal() bool {
	switch tx {
	case TxMigrate, TxConsolidate:
		return true
	default:
		return false
	}
}

// HasOutbound whether the txtype might trigger outbound tx
func (tx TxType) HasOutbound() bool {
	switch tx {
	case TxAdd,
		TxBond,
		TxTradeAccountDeposit,
		TxSecuredAssetDeposit,
		TxRunePoolDeposit,
		TxDonate,
		TxReserve,
		TxMigrate,
		TxRagnarok:
		return false
	default:
		return true
	}
}

func (tx TxType) IsEmpty() bool {
	return tx == TxUnknown
}

// Check if two txTypes are the same
func (tx TxType) Equals(tx2 TxType) bool {
	return tx == tx2
}

// Converts a txType into a string
func (tx TxType) String() string {
	return txToStringMap[tx]
}

type Memo interface {
	IsType(tx TxType) bool
	GetType() TxType
	IsEmpty() bool
	IsInbound() bool
	IsOutbound() bool
	IsInternal() bool
	String() string
	GetAsset() common.Asset
	GetAmount() cosmos.Uint
	GetDestination() common.Address
	GetSlipLimit() cosmos.Uint
	GetTxID() common.TxID
	GetAccAddress() cosmos.AccAddress
	GetBlockHeight() int64
	GetDexAggregator() string
	GetDexTargetAddress() string
	GetDexTargetLimit() *cosmos.Uint
	GetAffiliateTHORName() *types.THORName
	GetRefundAddress() common.Address
	GetAffiliates() []string
	GetAffiliatesBasisPoints() []cosmos.Uint
	GetAddress() common.Address
}

type MemoBase struct {
	TxType TxType
	Asset  common.Asset
}

var EmptyMemo = MemoBase{TxType: TxUnknown, Asset: common.EmptyAsset}

func (m MemoBase) String() string                          { return "" }
func (m MemoBase) GetType() TxType                         { return m.TxType }
func (m MemoBase) IsType(tx TxType) bool                   { return m.TxType.Equals(tx) }
func (m MemoBase) GetAsset() common.Asset                  { return m.Asset }
func (m MemoBase) GetAmount() cosmos.Uint                  { return cosmos.ZeroUint() }
func (m MemoBase) GetDestination() common.Address          { return "" }
func (m MemoBase) GetSlipLimit() cosmos.Uint               { return cosmos.ZeroUint() }
func (m MemoBase) GetTxID() common.TxID                    { return "" }
func (m MemoBase) GetAccAddress() cosmos.AccAddress        { return cosmos.AccAddress{} }
func (m MemoBase) GetBlockHeight() int64                   { return 0 }
func (m MemoBase) IsOutbound() bool                        { return m.TxType.IsOutbound() }
func (m MemoBase) IsInbound() bool                         { return m.TxType.IsInbound() }
func (m MemoBase) IsInternal() bool                        { return m.TxType.IsInternal() }
func (m MemoBase) IsEmpty() bool                           { return m.TxType.IsEmpty() }
func (m MemoBase) GetDexAggregator() string                { return "" }
func (m MemoBase) GetDexTargetAddress() string             { return "" }
func (m MemoBase) GetDexTargetLimit() *cosmos.Uint         { return nil }
func (m MemoBase) GetAffiliateTHORName() *types.THORName   { return nil }
func (m MemoBase) GetRefundAddress() common.Address        { return common.NoAddress }
func (m MemoBase) GetAffiliates() []string                 { return nil }
func (m MemoBase) GetAffiliatesBasisPoints() []cosmos.Uint { return nil }
func (m MemoBase) GetAddress() common.Address              { return common.NoAddress }

func ParseMemo(version semver.Version, memo string) (mem Memo, err error) {
	defer func() {
		if r := recover(); r != nil {
			mem = EmptyMemo
			err = fmt.Errorf("panicked parsing memo(%s), err: %s", memo, r)
		}
	}()

	parser, err := newParser(cosmos.Context{}, nil, version, memo)
	if err != nil {
		return EmptyMemo, err
	}

	return parser.parse()
}

func ParseMemoWithTHORNames(ctx cosmos.Context, keeper keeper.Keeper, memo string) (mem Memo, err error) {
	defer func() {
		if r := recover(); r != nil {
			mem = EmptyMemo
			err = fmt.Errorf("panicked parsing memo(%s), err: %s", memo, r)
		}
	}()

	parser, err := newParser(ctx, keeper, keeper.GetVersion(), memo)
	if err != nil {
		return EmptyMemo, err
	}

	return parser.parse()
}

func FetchAddress(ctx cosmos.Context, keeper keeper.Keeper, name string, chain common.Chain) (common.Address, error) {
	// if name is an address, return as is
	addr, err := common.NewAddress(name)
	if err == nil {
		return addr, nil
	}

	parts := strings.SplitN(name, ".", 2)
	if len(parts) > 1 {
		chain, err = common.NewChain(parts[1])
		if err != nil {
			return common.NoAddress, err
		}
	}

	if keeper.THORNameExists(ctx, parts[0]) {
		var thorname types.THORName
		thorname, err = keeper.GetTHORName(ctx, parts[0])
		if err != nil {
			return common.NoAddress, err
		}
		return thorname.GetAlias(chain), nil
	}

	return common.NoAddress, fmt.Errorf("%s is not recognizable", name)
}

func parseTradeTarget(limit string) (cosmos.Uint, error) {
	f, _, err := big.ParseFloat(limit, 10, 0, big.ToZero)
	if err != nil {
		return cosmos.ZeroUint(), err
	}
	i := new(big.Int)
	f.Int(i) // Note: fractional part will be discarded
	result := cosmos.NewUintFromBigInt(i)
	return result, nil
}
