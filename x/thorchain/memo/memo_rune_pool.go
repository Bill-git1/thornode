package thorchain

import (
	"strings"

	"github.com/blang/semver"
	"gitlab.com/thorchain/thornode/common"
	cosmos "gitlab.com/thorchain/thornode/common/cosmos"
	"gitlab.com/thorchain/thornode/constants"
	"gitlab.com/thorchain/thornode/x/thorchain/types"
)

// "pool+"

type RunePoolDepositMemo struct {
	MemoBase
}

func (m RunePoolDepositMemo) String() string {
	return m.string(false)
}

func (m RunePoolDepositMemo) ShortString() string {
	return m.string(true)
}

func (m RunePoolDepositMemo) string(short bool) string {
	return "pool+"
}

func NewRunePoolDepositMemo() RunePoolDepositMemo {
	return RunePoolDepositMemo{
		MemoBase: MemoBase{TxType: TxRunePoolDeposit},
	}
}

func (p *parser) ParseRunePoolDepositMemo() (RunePoolDepositMemo, error) {
	switch {
	case p.version.GTE(semver.MustParse("1.134.0")):
		return p.ParseRunePoolDepositMemoV134()
	default:
		return RunePoolDepositMemo{}, nil
	}
}

func (p *parser) ParseRunePoolDepositMemoV134() (RunePoolDepositMemo, error) {
	return NewRunePoolDepositMemo(), nil
}

// "pool-:<basis-points>:<affiliate>:<affiliate-basis-points>"

type RunePoolWithdrawMemo struct {
	MemoBase
	BasisPoints          cosmos.Uint
	AffiliateAddress     common.Address
	AffiliateBasisPoints cosmos.Uint
	AffiliateTHORName    *types.THORName
}

func (m RunePoolWithdrawMemo) GetBasisPts() cosmos.Uint              { return m.BasisPoints }
func (m RunePoolWithdrawMemo) GetAffiliateAddress() common.Address   { return m.AffiliateAddress }
func (m RunePoolWithdrawMemo) GetAffiliateBasisPoints() cosmos.Uint  { return m.AffiliateBasisPoints }
func (m RunePoolWithdrawMemo) GetAffiliateTHORName() *types.THORName { return m.AffiliateTHORName }

func (m RunePoolWithdrawMemo) String() string {
	args := []string{TxRunePoolWithdraw.String(), m.BasisPoints.String(), m.AffiliateAddress.String(), m.AffiliateBasisPoints.String()}
	return strings.Join(args, ":")
}

func NewRunePoolWithdrawMemo(basisPoints cosmos.Uint, affAddr common.Address, affBps cosmos.Uint, tn types.THORName) RunePoolWithdrawMemo {
	mem := RunePoolWithdrawMemo{
		MemoBase:             MemoBase{TxType: TxRunePoolWithdraw},
		BasisPoints:          basisPoints,
		AffiliateAddress:     affAddr,
		AffiliateBasisPoints: affBps,
	}
	if !tn.Owner.Empty() {
		mem.AffiliateTHORName = &tn
	}
	return mem
}

func (p *parser) ParseRunePoolWithdrawMemo() (RunePoolWithdrawMemo, error) {
	switch {
	case p.version.GTE(semver.MustParse("1.134.0")):
		return p.ParseRunePoolWithdrawMemoV134()
	default:
		return RunePoolWithdrawMemo{}, nil
	}
}

func (p *parser) ParseRunePoolWithdrawMemoV134() (RunePoolWithdrawMemo, error) {
	basisPoints := p.getUint(1, true, cosmos.ZeroInt().Uint64())
	affiliateAddress := p.getAddressWithKeeper(2, false, common.NoAddress, common.THORChain)
	tn := p.getTHORName(2, false, types.NewTHORName("", 0, nil))
	affiliateBasisPoints := p.getUintWithMaxValue(3, false, 0, constants.MaxBasisPts)
	return NewRunePoolWithdrawMemo(basisPoints, affiliateAddress, affiliateBasisPoints, tn), p.Error()
}
