package types

import (
	stypes "gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

type Msg struct {
	Type  string                 `json:"type"`
	Value stypes.MsgObservedTxIn `json:"value"`
}
