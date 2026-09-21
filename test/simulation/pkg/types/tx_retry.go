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
	zecSimTxMaxBroadcastAttempts    = 4
	zecSimTxBroadcastRetryDelay     = 2 * time.Second
)

// SignAndBroadcastTx retries transient failures with each chain's payload policy.
// ZEC must reuse signed bytes: a cancelled verification or transport error may
// still result in acceptance, so signing another transfer can pay twice.
func SignAndBroadcastTx(client LiteChainClient, tx SimTx) (string, error) {
	attempts, retryDelay := simTxRetryConfig(tx.Chain)

	var lastErr error
	var signed []byte
	for attempt := 1; attempt <= attempts; attempt++ {
		var err error
		if signed == nil || !tx.Chain.Equals(common.ZECChain) {
			signed, err = client.SignTx(tx)
		}
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
		// A returned txid plus a known submitted-but-unconfirmed error is the
		// safety condition here. This deliberately applies beyond OneShotMemo:
		// once the chain client has submitted a tx, replacing it can create a
		// duplicate inbound when bifrost later observes the original.
		if txid != "" && isSubmittedButUnconfirmedSimTxBroadcast(tx.Chain, err) {
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
	case chain.Equals(common.ZECChain):
		return zecSimTxMaxBroadcastAttempts, zecSimTxBroadcastRetryDelay
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
	case chain.Equals(common.ZECChain):
		return shouldRetryZecSimTxBroadcast(err)
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
		// TRON "not confirmed" means the fullnode could query the tx but it
		// did not mine it before the local wait expired. Keep that txid to
		// avoid replacing a possibly-valid inbound with another transfer.
		// "Not observed" is different: the fullnode never returned the tx, so
		// reporting it as broadcasted leaves the swap actor waiting on a txid
		// Bifrost cannot observe.
		return strings.Contains(message, "not confirmed on tron node")
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

func shouldRetryZecSimTxBroadcast(err error) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error())
	return strings.Contains(message, "verification cancelled") ||
		strings.Contains(message, "timeout waiting for verification result") ||
		strings.Contains(message, "already queued for download") ||
		isTransientSimTxRPCError(message)
}

func isTransientSimTxRPCError(message string) bool {
	return strings.Contains(message, "connect: connection refused") ||
		strings.Contains(message, "context deadline exceeded") ||
		strings.Contains(message, "connection reset by peer") ||
		strings.Contains(message, "i/o timeout") ||
		strings.Contains(message, "channel closed") ||
		strings.Contains(message, "eof")
}
