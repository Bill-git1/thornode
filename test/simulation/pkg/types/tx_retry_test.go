package types

import (
	"errors"
	"testing"

	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/common"
)

type TxRetrySuite struct{}

var _ = Suite(&TxRetrySuite{})

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

// TestSignAndBroadcastTxRetriesTronUnobservedTxID covers the failure mode
// where the TRON node accepts a broadcast response shape but the transaction
// never becomes queryable by txid. Treating that as submitted makes the
// simulation wait for an inbound Bifrost can never observe.
func TestSignAndBroadcastTxRetriesTronUnobservedTxID(t *testing.T) {
	prevDelay := tronSimTxBroadcastRetryDelay
	prevAttempts := tronSimTxMaxBroadcastAttempts
	tronSimTxBroadcastRetryDelay = 0
	tronSimTxMaxBroadcastAttempts = 4
	defer func() {
		tronSimTxBroadcastRetryDelay = prevDelay
		tronSimTxMaxBroadcastAttempts = prevAttempts
	}()

	// The first attempt returns a txid, but the TRON client reported that the
	// tx never materialized on the fullnode. The retry helper should build and
	// broadcast a replacement instead of exposing that invisible txid.
	client := &retryLiteChainClient{
		broadcastTxIDs: []string{"invisible", "confirmed"},
		broadcastErrs: []error{
			errors.New("broadcasted tx invisible not observed on tron node"),
			nil,
		},
	}

	txid, err := SignAndBroadcastTx(client, SimTx{Chain: common.TRONChain})
	if err != nil {
		t.Fatalf("expected tron retry helper to recover from unobserved txid, got %v", err)
	}
	if txid != "confirmed" {
		t.Fatalf("expected replacement txid, got %s", txid)
	}
	if client.signCalls != 2 {
		t.Fatalf("expected 2 sign attempts, got %d", client.signCalls)
	}
	if client.broadcastCalls != 2 {
		t.Fatalf("expected 2 broadcast attempts, got %d", client.broadcastCalls)
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

// TestSignAndBroadcastTxAcceptsSubmittedSolanaTransaction verifies Solana
// submitted-but-unconfirmed broadcasts are treated as accepted one-shot results.
func (s *TxRetrySuite) TestSignAndBroadcastTxAcceptsSubmittedSolanaTransaction(c *C) {
	prevDelay := solanaSimTxBroadcastRetryDelay
	prevAttempts := solanaSimTxMaxBroadcastAttempts
	solanaSimTxBroadcastRetryDelay = 0
	solanaSimTxMaxBroadcastAttempts = 4
	defer func() {
		solanaSimTxBroadcastRetryDelay = prevDelay
		solanaSimTxMaxBroadcastAttempts = prevAttempts
	}()

	client := &retryLiteChainClient{
		broadcastTxIDs: []string{"submitted"},
		broadcastErrs: []error{
			errors.New("broadcasted tx submitted not confirmed on solana node after 1m0s"),
		},
	}

	txid, err := SignAndBroadcastTx(client, SimTx{Chain: common.SOLChain})
	c.Assert(err, IsNil)
	c.Assert(txid, Equals, "submitted")
	c.Assert(client.signCalls, Equals, 1)
	c.Assert(client.broadcastCalls, Equals, 1)
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

func TestSignAndBroadcastTxRetriesZecQueuedDownloadFailures(t *testing.T) {
	prevDelay := zecSimTxBroadcastRetryDelay
	prevAttempts := zecSimTxMaxBroadcastAttempts
	zecSimTxBroadcastRetryDelay = 0
	zecSimTxMaxBroadcastAttempts = 4
	defer func() {
		zecSimTxBroadcastRetryDelay = prevDelay
		zecSimTxMaxBroadcastAttempts = prevAttempts
	}()

	client := &retryLiteChainClient{
		broadcastErrs: []error{
			errors.New("transaction dropped because it is already queued for download"),
			nil,
		},
	}

	txid, err := SignAndBroadcastTx(client, SimTx{Chain: common.ZECChain})
	if err != nil {
		t.Fatalf("expected zec retry helper to recover from queued-download failure, got %v", err)
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

// TestSignAndBroadcastTxRetriesZecChannelClosedFailures covers the transient
// Zebra RPC channel close seen during mocknet ZEC account seeding.
func TestSignAndBroadcastTxRetriesZecChannelClosedFailures(t *testing.T) {
	prevDelay := zecSimTxBroadcastRetryDelay
	prevAttempts := zecSimTxMaxBroadcastAttempts
	zecSimTxBroadcastRetryDelay = 0
	zecSimTxMaxBroadcastAttempts = 4
	defer func() {
		zecSimTxBroadcastRetryDelay = prevDelay
		zecSimTxMaxBroadcastAttempts = prevAttempts
	}()

	// A closed RPC channel does not prove the funding transaction is invalid,
	// so the simulation should rebuild and rebroadcast instead of exiting.
	client := &retryLiteChainClient{
		broadcastErrs: []error{
			errors.New("channel closed"),
			nil,
		},
	}

	txid, err := SignAndBroadcastTx(client, SimTx{Chain: common.ZECChain})
	if err != nil {
		t.Fatalf("expected zec retry helper to recover from channel-closed failure, got %v", err)
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

func TestShouldRetryZecSimTxBroadcast(t *testing.T) {
	if !shouldRetryZecSimTxBroadcast(errors.New("transaction dropped because it is already queued for download")) {
		t.Fatal("expected queued-download failure to be retriable")
	}
	if !shouldRetryZecSimTxBroadcast(errors.New("failed to broadcast tx: context deadline exceeded")) {
		t.Fatal("expected transient zec RPC failure to be retriable")
	}
	if !shouldRetryZecSimTxBroadcast(errors.New("channel closed")) {
		t.Fatal("expected closed zec RPC channel failure to be retriable")
	}
	if shouldRetryZecSimTxBroadcast(errors.New("other error")) {
		t.Fatal("expected unrelated errors to be non-retriable")
	}
	if shouldRetryZecSimTxBroadcast(nil) {
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
