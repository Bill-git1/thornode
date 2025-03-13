package thorchain

import (
	cosmos "gitlab.com/thorchain/thornode/v3/common/cosmos"
)

type RotateMemo struct {
	MemoBase
	OperatorAddress cosmos.AccAddress
}

func NewRotateMemo(operatorAddr cosmos.AccAddress) RotateMemo {
	return RotateMemo{
		MemoBase:        MemoBase{TxType: TxRotate},
		OperatorAddress: operatorAddr,
	}
}

func (p *parser) ParseRotate() (RotateMemo, error) {
	operatorAddr := p.getAccAddress(1, true, nil)
	return NewRotateMemo(operatorAddr), p.Error()
}
