package rpc

import (
	"fmt"
)

// Response struct for getSignaturesForAddress
type GetSignaturesForAddressResponse struct {
	Jsonrpc string                       `json:"jsonrpc"`
	Id      int                          `json:"id"`
	Result  []SignaturesForAddressResult `json:"result"`
	Error   *RPCError                    `json:"error"`
}

// getSignaturesForAddress response data
type SignaturesForAddressResult struct {
	Signature string `json:"signature"`
	Slot      uint64 `json:"slot"`
	Err       any    `json:"err"`
}

type GetSignaturesForAddressUntilParams struct {
	Commitment     string `json:"commitment,omitempty"` // e.g., "finalized"
	MinContextSlot uint64 `json:"minContextSlot,omitempty"`
	Before         string `json:"before,omitempty"`
	Until          string `json:"until,omitempty"`
	Limit          int    `json:"limit,omitempty"`
}

// GetSignaturesForAddressUntil - makes a getSignaturesForAddress RPC call to the Solana RPC.
func (s *SolRPC) GetSignaturesForAddressUntil(address string, params GetSignaturesForAddressUntilParams) ([]SignaturesForAddressResult, error) {
	p := []interface{}{
		address,
		params,
	}

	var response GetSignaturesForAddressResponse
	if err := s.post("getSignaturesForAddress", p, &response); err != nil {
		return nil, fmt.Errorf("failed to call getSignaturesForAddress: %w", err)
	}

	if response.Error != nil && response.Error.Code != 0 {
		return nil, fmt.Errorf("RPC Error %d: %s", response.Error.Code, response.Error.Message)
	}

	return response.Result, nil
}

// Response struct for getSignaturesForAddress
type GetTransactionResponse struct {
	Jsonrpc string            `json:"jsonrpc"`
	Id      int               `json:"id"`
	Result  TransactionResult `json:"result"`
	Error   *RPCError         `json:"error"`
}

// getTransaction response data
type TransactionResult struct {
	Slot        uint64     `json:"slot"`
	Meta        RPCMeta    `json:"meta"`
	Transaction RPCTxnData `json:"transaction"`
}

// GetTransactionUntil - makes a getTransaction RPC call to the Solana RPC. Will only return "finalized" data.
func (s *SolRPC) GetTransaction(signature string) (*TransactionResult, error) {
	params := []interface{}{
		signature,
		map[string]interface{}{
			"commitment": "finalized",
			"encoding":   "json",
		},
	}

	var response GetTransactionResponse
	if err := s.post("getTransaction", params, &response); err != nil {
		return nil, fmt.Errorf("failed to call getTransaction: %w", err)
	}

	if response.Error != nil && response.Error.Code != 0 {
		return nil, fmt.Errorf("RPC Error %d: %s", response.Error.Code, response.Error.Message)
	}

	return &response.Result, nil
}
