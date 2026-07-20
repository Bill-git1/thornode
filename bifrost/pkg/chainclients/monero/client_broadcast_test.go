package monero

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/monero/sidecar"
	stypes "gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
	tssp "gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/tss"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/config"
)

func testBroadcastPayload(t *testing.T, txHash, txMetadata, msgID string) signedTxPayload {
	t.Helper()
	txKey := strings.Repeat("1", 64)
	txKeyHash, err := tssp.FrostTxKeyHash(txKey)
	if err != nil {
		t.Fatalf("hash test tx key: %v", err)
	}
	return signedTxPayload{
		TxHash:     txHash,
		TxMetadata: txMetadata,
		TxKey:      txKey,
		MsgID:      msgID,
		Eventuality: tssp.FrostEventuality{
			TxKeyHash: txKeyHash,
		},
	}
}

func TestBroadcastTxPreservesSignedHashWhenSidecarHashEmpty(t *testing.T) {
	t.Parallel()

	var (
		mu     sync.Mutex
		gotReq sidecar.BroadcastReq
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/monero/broadcast" {
			http.NotFound(w, r)
			return
		}
		var req sidecar.BroadcastReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		gotReq = req
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(sidecar.BroadcastResp{TxHash: "", TxKey: req.TxKey})
	}))
	defer server.Close()

	signer, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}

	vaultPubKey, _ := testVaultPubKeyAndAddress(t)
	client := &Client{
		sidecar:            signer,
		signerCacheManager: newTestSignerCacheManager(t),
	}
	tx := stypes.TxOutItem{
		Chain:            common.XMRChain,
		VaultPubKey:      vaultPubKey,
		VaultPubKeyEddsa: vaultPubKey,
		VaultPubKeyFrost: vaultPubKey,
	}
	payload := testBroadcastPayload(t, "signed-hash", "deadbeef", "broadcast-msg-id-1")

	txHash, err := client.broadcastTx(tx, payload)
	if err != nil {
		t.Fatalf("broadcast tx: %v", err)
	}
	if txHash != payload.TxHash {
		t.Fatalf("expected signed tx hash %q, got %q", payload.TxHash, txHash)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.TrimSpace(gotReq.MsgID) == "" {
		t.Fatalf("expected broadcast request to include msg_id")
	}
	if gotReq.MsgID != payload.MsgID {
		t.Fatalf("expected msg_id %q, got %q", payload.MsgID, gotReq.MsgID)
	}
	if strings.TrimSpace(gotReq.VaultPubKey) == "" {
		t.Fatalf("expected broadcast request to include vault_pub_key")
	}
	if gotReq.VaultPubKey != vaultPubKey.String() {
		t.Fatalf("expected vault_pub_key %q, got %q", vaultPubKey, gotReq.VaultPubKey)
	}
}

func TestBroadcastTxIgnoresSidecarHashOverride(t *testing.T) {
	t.Parallel()

	var (
		mu     sync.Mutex
		gotReq sidecar.BroadcastReq
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/monero/broadcast" {
			http.NotFound(w, r)
			return
		}
		var req sidecar.BroadcastReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		gotReq = req
		mu.Unlock()
		// Sidecar returns a different hash than FROST-validated one.
		_ = json.NewEncoder(w).Encode(sidecar.BroadcastResp{TxHash: "daemon-hash", TxKey: req.TxKey})
	}))
	defer server.Close()

	signer, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}

	vaultPubKey, _ := testVaultPubKeyAndAddress(t)
	client := &Client{
		sidecar:            signer,
		signerCacheManager: newTestSignerCacheManager(t),
	}
	tx := stypes.TxOutItem{
		Chain:            common.XMRChain,
		VaultPubKey:      vaultPubKey,
		VaultPubKeyEddsa: vaultPubKey,
		VaultPubKeyFrost: vaultPubKey,
	}
	payload := testBroadcastPayload(t, "signed-hash", "deadbeef", "broadcast-msg-id-2")

	txHash, err := client.broadcastTx(tx, payload)
	if err != nil {
		t.Fatalf("broadcast tx: %v", err)
	}
	// FROST-validated hash must be preserved; sidecar override must be ignored.
	if txHash != payload.TxHash {
		t.Fatalf("expected FROST-validated hash %q, got %q (sidecar override should be ignored)", payload.TxHash, txHash)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.TrimSpace(gotReq.MsgID) == "" {
		t.Fatalf("expected broadcast request to include msg_id")
	}
	if gotReq.MsgID != payload.MsgID {
		t.Fatalf("expected msg_id %q, got %q", payload.MsgID, gotReq.MsgID)
	}
	if strings.TrimSpace(gotReq.VaultPubKey) == "" {
		t.Fatalf("expected broadcast request to include vault_pub_key")
	}
	if gotReq.VaultPubKey != vaultPubKey.String() {
		t.Fatalf("expected vault_pub_key %q, got %q", vaultPubKey, gotReq.VaultPubKey)
	}
}

func TestBroadcastTxRequiresMsgID(t *testing.T) {
	t.Parallel()

	var sidecarCalled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sidecarCalled.Store(true)
		_ = json.NewEncoder(w).Encode(sidecar.BroadcastResp{TxHash: "daemon-hash"})
	}))
	defer server.Close()

	signer, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}

	vaultPubKey, _ := testVaultPubKeyAndAddress(t)
	client := &Client{
		sidecar:            signer,
		signerCacheManager: newTestSignerCacheManager(t),
	}
	tx := stypes.TxOutItem{
		Chain:            common.XMRChain,
		VaultPubKey:      vaultPubKey,
		VaultPubKeyEddsa: vaultPubKey,
		VaultPubKeyFrost: vaultPubKey,
	}
	payload := testBroadcastPayload(t, "signed-hash", "deadbeef", "   ")

	_, err = client.broadcastTx(tx, payload)
	if err == nil {
		t.Fatalf("expected broadcast tx to fail when msg_id is missing")
	}
	if !strings.Contains(err.Error(), "missing msg_id") {
		t.Fatalf("expected missing msg_id error, got %v", err)
	}
	if sidecarCalled.Load() {
		t.Fatalf("expected no sidecar broadcast call when msg_id is missing")
	}
}

func TestBroadcastTxRequiresFrostVaultPubKey(t *testing.T) {
	t.Parallel()

	var sidecarCalled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sidecarCalled.Store(true)
		_ = json.NewEncoder(w).Encode(sidecar.BroadcastResp{TxHash: "daemon-hash"})
	}))
	defer server.Close()

	signer, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}

	vaultPubKey, _ := testVaultPubKeyAndAddress(t)
	client := &Client{
		sidecar:            signer,
		signerCacheManager: newTestSignerCacheManager(t),
	}
	tx := stypes.TxOutItem{
		Chain:       common.XMRChain,
		VaultPubKey: vaultPubKey,
	}
	payload := testBroadcastPayload(t, "signed-hash", "deadbeef", "broadcast-msg-id-missing-vault")

	_, err = client.broadcastTx(tx, payload)
	if err == nil {
		t.Fatalf("expected broadcast tx to fail when frost vault pubkey is missing")
	}
	if !strings.Contains(err.Error(), "missing vault_pub_key_frost") {
		t.Fatalf("expected missing vault_pub_key_frost error, got %v", err)
	}
	if sidecarCalled.Load() {
		t.Fatalf("expected no sidecar broadcast call when frost vault pubkey is missing")
	}
}

func TestBroadcastTxIgnoresBroadcastedFlagAndStillCallsSidecar(t *testing.T) {
	t.Parallel()

	var (
		sidecarCalls int32
		mu           sync.Mutex
		gotReq       sidecar.BroadcastReq
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&sidecarCalls, 1)
		if r.URL.Path != "/v1/monero/broadcast" {
			http.NotFound(w, r)
			return
		}
		var req sidecar.BroadcastReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		gotReq = req
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(sidecar.BroadcastResp{TxHash: "daemon-hash", TxKey: req.TxKey})
	}))
	defer server.Close()

	signer, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}

	vaultPubKey, _ := testVaultPubKeyAndAddress(t)
	client := &Client{
		sidecar:            signer,
		signerCacheManager: newTestSignerCacheManager(t),
	}
	tx := stypes.TxOutItem{
		Chain:            common.XMRChain,
		VaultPubKey:      vaultPubKey,
		VaultPubKeyEddsa: vaultPubKey,
		VaultPubKeyFrost: vaultPubKey,
	}
	payload := testBroadcastPayload(t, "signed-hash", "deadbeef", "broadcast-msg-id-3")
	payload.Broadcasted = true

	txHash, err := client.broadcastTx(tx, payload)
	if err != nil {
		t.Fatalf("broadcast tx: %v", err)
	}
	// FROST-validated hash must be preserved regardless of Broadcasted flag.
	if txHash != payload.TxHash {
		t.Fatalf("expected FROST-validated hash %q, got %q", payload.TxHash, txHash)
	}
	if got := atomic.LoadInt32(&sidecarCalls); got != 1 {
		t.Fatalf("expected exactly one sidecar broadcast call, got %d", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotReq.MsgID != payload.MsgID {
		t.Fatalf("expected msg_id %q, got %q", payload.MsgID, gotReq.MsgID)
	}
	if gotReq.TxHex != payload.TxMetadata {
		t.Fatalf("expected tx hex %q, got %q", payload.TxMetadata, gotReq.TxHex)
	}
}

func TestBroadcastTxBroadcastedFlagStillRequiresTxMetadata(t *testing.T) {
	t.Parallel()

	var sidecarCalled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sidecarCalled.Store(true)
		_ = json.NewEncoder(w).Encode(sidecar.BroadcastResp{TxHash: "daemon-hash"})
	}))
	defer server.Close()

	signer, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}

	vaultPubKey, _ := testVaultPubKeyAndAddress(t)
	client := &Client{
		sidecar:            signer,
		signerCacheManager: newTestSignerCacheManager(t),
	}
	tx := stypes.TxOutItem{
		Chain:            common.XMRChain,
		VaultPubKey:      vaultPubKey,
		VaultPubKeyEddsa: vaultPubKey,
		VaultPubKeyFrost: vaultPubKey,
	}
	payload := testBroadcastPayload(t, "signed-hash", "   ", "broadcast-msg-id-4")
	payload.Broadcasted = true

	_, err = client.broadcastTx(tx, payload)
	if err == nil {
		t.Fatalf("expected broadcast tx to fail when tx_metadata is missing")
	}
	if !strings.Contains(err.Error(), "missing tx_hex") {
		t.Fatalf("expected missing tx_hex error, got %v", err)
	}
	if sidecarCalled.Load() {
		t.Fatalf("expected no sidecar broadcast call when tx_metadata is missing")
	}
}
