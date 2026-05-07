package types

import (
	"errors"
	"testing"

	"gitlab.com/thorchain/thornode/v3/common"
)

type retryLiteChainClient struct {
	signCalls      int
	broadcastCalls int
	signErrs       []error
	broadcastErrs  []error
	broadcastTxIDs []string
}

func (r *retryLiteChainClient) GetAccount(_ *common.PubKey) (*common.Account, error) {
	return &common.Account{}, nil
}

func (r *retryLiteChainClient) SignTx(SimTx) ([]byte, error) {
	r.signCalls++
	idx := r.signCalls - 1
	if idx < len(r.signErrs) && r.signErrs[idx] != nil {
		return nil, r.signErrs[idx]
	}

	return []byte("signed"), nil
}

func (r *retryLiteChainClient) BroadcastTx([]byte) (string, error) {
	r.broadcastCalls++
	idx := r.broadcastCalls - 1
	if idx < len(r.broadcastErrs) && r.broadcastErrs[idx] != nil {
		if idx < len(r.broadcastTxIDs) {
			return r.broadcastTxIDs[idx], r.broadcastErrs[idx]
		}
		return "", r.broadcastErrs[idx]
	}

	if idx < len(r.broadcastTxIDs) && r.broadcastTxIDs[idx] != "" {
		return r.broadcastTxIDs[idx], nil
	}
	return "ok", nil
}

func TestSignAndBroadcastTxRetriesTronConfirmationFailures(t *testing.T) {
	prevDelay := tronSimTxBroadcastRetryDelay
	prevAttempts := tronSimTxMaxBroadcastAttempts
	tronSimTxBroadcastRetryDelay = 0
	tronSimTxMaxBroadcastAttempts = 4
	defer func() {
		tronSimTxBroadcastRetryDelay = prevDelay
		tronSimTxMaxBroadcastAttempts = prevAttempts
	}()

	client := &retryLiteChainClient{
		broadcastErrs: []error{
			errors.New("broadcasted tx a not confirmed on tron node"),
			errors.New("broadcasted tx b not observed on tron node"),
			nil,
		},
	}

	txid, err := SignAndBroadcastTx(client, SimTx{Chain: common.TRONChain})
	if err != nil {
		t.Fatalf("expected tron retry helper to recover, got %v", err)
	}
	if txid != "ok" {
		t.Fatalf("expected successful txid, got %s", txid)
	}
	if client.signCalls != 3 {
		t.Fatalf("expected 3 sign attempts, got %d", client.signCalls)
	}
	if client.broadcastCalls != 3 {
		t.Fatalf("expected 3 broadcast attempts, got %d", client.broadcastCalls)
	}
}

func TestSignAndBroadcastTxDoesNotReplaceSubmittedOneShotMemo(t *testing.T) {
	prevDelay := tronSimTxBroadcastRetryDelay
	prevAttempts := tronSimTxMaxBroadcastAttempts
	tronSimTxBroadcastRetryDelay = 0
	tronSimTxMaxBroadcastAttempts = 4
	defer func() {
		tronSimTxBroadcastRetryDelay = prevDelay
		tronSimTxMaxBroadcastAttempts = prevAttempts
	}()

	client := &retryLiteChainClient{
		broadcastTxIDs: []string{"submitted"},
		broadcastErrs: []error{
			errors.New("broadcasted tx submitted not confirmed on tron node"),
		},
	}

	txid, err := SignAndBroadcastTx(client, SimTx{
		Chain:       common.TRONChain,
		Memo:        "r:00001",
		OneShotMemo: true,
	})
	if err != nil {
		t.Fatalf("expected submitted one-shot txid to be accepted, got %v", err)
	}
	if txid != "submitted" {
		t.Fatalf("expected submitted txid, got %s", txid)
	}
	if client.signCalls != 1 {
		t.Fatalf("expected 1 sign attempt, got %d", client.signCalls)
	}
	if client.broadcastCalls != 1 {
		t.Fatalf("expected 1 broadcast attempt, got %d", client.broadcastCalls)
	}
}

func TestSignAndBroadcastTxRetriesTronTransportFailures(t *testing.T) {
	prevDelay := tronSimTxBroadcastRetryDelay
	prevAttempts := tronSimTxMaxBroadcastAttempts
	tronSimTxBroadcastRetryDelay = 0
	tronSimTxMaxBroadcastAttempts = 4
	defer func() {
		tronSimTxBroadcastRetryDelay = prevDelay
		tronSimTxMaxBroadcastAttempts = prevAttempts
	}()

	client := &retryLiteChainClient{
		broadcastErrs: []error{
			errors.New(`failed to call broadcasttransaction: failed to perform request: Post "http://127.0.0.1:8090/wallet/broadcasttransaction": context deadline exceeded`),
			nil,
		},
	}

	txid, err := SignAndBroadcastTx(client, SimTx{Chain: common.TRONChain})
	if err != nil {
		t.Fatalf("expected tron retry helper to recover from transport failure, got %v", err)
	}
	if txid != "ok" {
		t.Fatalf("expected successful txid, got %s", txid)
	}
	if client.signCalls != 2 {
		t.Fatalf("expected 2 sign attempts, got %d", client.signCalls)
	}
	if client.broadcastCalls != 2 {
		t.Fatalf("expected 2 broadcast attempts, got %d", client.broadcastCalls)
	}
}

func TestSignAndBroadcastTxDoesNotRetryNonTronChains(t *testing.T) {
	prevDelay := tronSimTxBroadcastRetryDelay
	prevAttempts := tronSimTxMaxBroadcastAttempts
	tronSimTxBroadcastRetryDelay = 0
	tronSimTxMaxBroadcastAttempts = 4
	defer func() {
		tronSimTxBroadcastRetryDelay = prevDelay
		tronSimTxMaxBroadcastAttempts = prevAttempts
	}()

	client := &retryLiteChainClient{
		broadcastErrs: []error{
			errors.New("broadcasted tx a not confirmed on tron node"),
		},
	}

	_, err := SignAndBroadcastTx(client, SimTx{Chain: common.BTCChain})
	if err == nil {
		t.Fatal("expected non-tron tx to fail without retry")
	}
	if client.signCalls != 1 {
		t.Fatalf("expected 1 sign attempt, got %d", client.signCalls)
	}
	if client.broadcastCalls != 1 {
		t.Fatalf("expected 1 broadcast attempt, got %d", client.broadcastCalls)
	}
}

func TestSignAndBroadcastTxRetriesSolanaConfirmationFailures(t *testing.T) {
	prevDelay := solanaSimTxBroadcastRetryDelay
	prevAttempts := solanaSimTxMaxBroadcastAttempts
	solanaSimTxBroadcastRetryDelay = 0
	solanaSimTxMaxBroadcastAttempts = 4
	defer func() {
		solanaSimTxBroadcastRetryDelay = prevDelay
		solanaSimTxMaxBroadcastAttempts = prevAttempts
	}()

	client := &retryLiteChainClient{
		broadcastErrs: []error{
			errors.New("broadcasted tx a not confirmed on solana node after 12s"),
			errors.New("failed to broadcast tx: dial tcp 127.0.0.1:8899: connect: connection refused"),
			nil,
		},
	}

	txid, err := SignAndBroadcastTx(client, SimTx{Chain: common.SOLChain})
	if err != nil {
		t.Fatalf("expected solana retry helper to recover, got %v", err)
	}
	if txid != "ok" {
		t.Fatalf("expected successful txid, got %s", txid)
	}
	if client.signCalls != 3 {
		t.Fatalf("expected 3 sign attempts, got %d", client.signCalls)
	}
	if client.broadcastCalls != 3 {
		t.Fatalf("expected 3 broadcast attempts, got %d", client.broadcastCalls)
	}
}

func TestSignAndBroadcastTxRetriesSolanaBlockhashFailures(t *testing.T) {
	prevDelay := solanaSimTxBroadcastRetryDelay
	prevAttempts := solanaSimTxMaxBroadcastAttempts
	solanaSimTxBroadcastRetryDelay = 0
	solanaSimTxMaxBroadcastAttempts = 4
	defer func() {
		solanaSimTxBroadcastRetryDelay = prevDelay
		solanaSimTxMaxBroadcastAttempts = prevAttempts
	}()

	client := &retryLiteChainClient{
		signErrs: []error{
			errors.New("fail to get latest blockhash: Post http://127.0.0.1:8899: dial tcp 127.0.0.1:8899: connect: connection refused"),
			nil,
		},
	}

	txid, err := SignAndBroadcastTx(client, SimTx{Chain: common.SOLChain})
	if err != nil {
		t.Fatalf("expected solana retry helper to recover from blockhash failure, got %v", err)
	}
	if txid != "ok" {
		t.Fatalf("expected successful txid, got %s", txid)
	}
	if client.signCalls != 2 {
		t.Fatalf("expected 2 sign attempts, got %d", client.signCalls)
	}
	if client.broadcastCalls != 1 {
		t.Fatalf("expected 1 broadcast attempt, got %d", client.broadcastCalls)
	}
}

func TestShouldRetryTronSimTxBroadcast(t *testing.T) {
	if !shouldRetryTronSimTxBroadcast(errors.New("broadcasted tx a not confirmed on tron node")) {
		t.Fatal("expected confirmation failure to be retriable")
	}
	if !shouldRetryTronSimTxBroadcast(errors.New("broadcasted tx a not observed on tron node")) {
		t.Fatal("expected observation failure to be retriable")
	}
	if !shouldRetryTronSimTxBroadcast(errors.New(`failed to call broadcasttransaction: failed to perform request: Post "http://127.0.0.1:8090/wallet/broadcasttransaction": context deadline exceeded`)) {
		t.Fatal("expected transient TRON transport failures to be retriable")
	}
	if shouldRetryTronSimTxBroadcast(errors.New("other error")) {
		t.Fatal("expected unrelated errors to be non-retriable")
	}
	if shouldRetryTronSimTxBroadcast(nil) {
		t.Fatal("expected nil error to be non-retriable")
	}
}

func TestShouldRetrySolanaSimTxFailures(t *testing.T) {
	if !shouldRetrySolanaSimTxBroadcast(errors.New("broadcasted tx a not confirmed on solana node after 12s")) {
		t.Fatal("expected confirmation failure to be retriable")
	}
	if !shouldRetrySolanaSimTxBroadcast(errors.New("failed to broadcast tx: dial tcp 127.0.0.1:8899: connect: connection refused")) {
		t.Fatal("expected transient solana RPC failure to be retriable")
	}
	if !shouldRetrySolanaSimTxSign(errors.New("fail to get latest blockhash: Post http://127.0.0.1:8899: dial tcp 127.0.0.1:8899: connect: connection refused")) {
		t.Fatal("expected latest blockhash failure to be retriable")
	}
	if shouldRetrySolanaSimTxBroadcast(errors.New("other error")) {
		t.Fatal("expected unrelated broadcast errors to be non-retriable")
	}
	if shouldRetrySolanaSimTxSign(errors.New("other error")) {
		t.Fatal("expected unrelated sign errors to be non-retriable")
	}
}
