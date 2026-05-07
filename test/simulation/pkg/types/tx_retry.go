package types

import (
	"strings"
	"time"

	"gitlab.com/thorchain/thornode/v3/common"
)

var (
	tronSimTxMaxBroadcastAttempts   = 4
	tronSimTxBroadcastRetryDelay    = 2 * time.Second
	solanaSimTxMaxBroadcastAttempts = 4
	solanaSimTxBroadcastRetryDelay  = 2 * time.Second
)

// SignAndBroadcastTx signs a fresh transaction payload for each attempt so
// chain-specific retries can recover from transient broadcast failures without
// reusing stale signed payloads.
func SignAndBroadcastTx(client LiteChainClient, tx SimTx) (string, error) {
	attempts, retryDelay := simTxRetryConfig(tx.Chain)

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		signed, err := client.SignTx(tx)
		if err != nil {
			lastErr = err
			if attempt < attempts && shouldRetrySimTxSign(tx.Chain, err) {
				time.Sleep(retryDelay)
				continue
			}
			return "", err
		}

		txid, err := client.BroadcastTx(signed)
		if err == nil {
			return txid, nil
		}

		lastErr = err
		if tx.OneShotMemo && txid != "" && isSubmittedButUnconfirmedSimTxBroadcast(tx.Chain, err) {
			return txid, nil
		}
		if attempt < attempts && shouldRetrySimTxBroadcast(tx.Chain, err) {
			time.Sleep(retryDelay)
			continue
		}

		return "", err
	}

	return "", lastErr
}

func simTxRetryConfig(chain common.Chain) (int, time.Duration) {
	switch {
	case chain.Equals(common.TRONChain):
		return tronSimTxMaxBroadcastAttempts, tronSimTxBroadcastRetryDelay
	case chain.Equals(common.SOLChain):
		return solanaSimTxMaxBroadcastAttempts, solanaSimTxBroadcastRetryDelay
	default:
		return 1, 0
	}
}

func shouldRetrySimTxSign(chain common.Chain, err error) bool {
	switch {
	case chain.Equals(common.SOLChain):
		return shouldRetrySolanaSimTxSign(err)
	default:
		return false
	}
}

func shouldRetrySimTxBroadcast(chain common.Chain, err error) bool {
	switch {
	case chain.Equals(common.TRONChain):
		return shouldRetryTronSimTxBroadcast(err)
	case chain.Equals(common.SOLChain):
		return shouldRetrySolanaSimTxBroadcast(err)
	default:
		return false
	}
}

func isSubmittedButUnconfirmedSimTxBroadcast(chain common.Chain, err error) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error())
	switch {
	case chain.Equals(common.TRONChain):
		return strings.Contains(message, "not confirmed on tron node") ||
			strings.Contains(message, "not observed on tron node")
	case chain.Equals(common.SOLChain):
		return strings.Contains(message, "not confirmed on solana node")
	default:
		return false
	}
}

func shouldRetryTronSimTxBroadcast(err error) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error())
	return strings.Contains(message, "not confirmed on tron node") ||
		strings.Contains(message, "not observed on tron node") ||
		isTransientSimTxRPCError(message)
}

func shouldRetrySolanaSimTxSign(err error) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error())
	return strings.Contains(message, "fail to get latest blockhash") ||
		isTransientSimTxRPCError(message)
}

func shouldRetrySolanaSimTxBroadcast(err error) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error())
	return strings.Contains(message, "not confirmed on solana node") ||
		isTransientSimTxRPCError(message)
}

func isTransientSimTxRPCError(message string) bool {
	return strings.Contains(message, "connect: connection refused") ||
		strings.Contains(message, "context deadline exceeded") ||
		strings.Contains(message, "connection reset by peer") ||
		strings.Contains(message, "i/o timeout") ||
		strings.Contains(message, "eof")
}
