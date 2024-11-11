package types

import (
	coretypes "github.com/cometbft/cometbft/rpc/core/types"
)

type BroadcastResult struct {
	JSONRPC string                            `json:"jsonrpc"`
	Result  coretypes.ResultBroadcastTxCommit `json:"result"`
}
