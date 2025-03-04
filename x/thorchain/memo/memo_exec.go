package thorchain

import (
	"encoding/base64"
	"strings"

	"gitlab.com/thorchain/thornode/v3/common/cosmos"
)

type ExecMemo struct {
	MemoBase
	ContractAddress cosmos.AccAddress
	Msg             []byte
}

func (m ExecMemo) GetAccAddress() cosmos.AccAddress { return m.ContractAddress }
func (m ExecMemo) String() string {
	args := []string{TxExec.String(), m.ContractAddress.String(), base64.StdEncoding.EncodeToString(m.Msg)}
	return strings.Join(args, ":")
}

func NewExecMemo(contract cosmos.AccAddress, msg []byte) ExecMemo {
	return ExecMemo{
		MemoBase:        MemoBase{TxType: TxExec},
		ContractAddress: contract,
		Msg:             msg,
	}
}

func (p *parser) ParseExecMemo() (ExecMemo, error) {
	contract := p.getAccAddress(1, true, nil)
	msg := p.getBase64Bytes(2, true, []byte{})
	return NewExecMemo(contract, msg), p.Error()
}
