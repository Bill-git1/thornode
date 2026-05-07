package sidecar

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"gitlab.com/thorchain/thornode/v3/config"
)

func TestClientChainHeight(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/monero/chain/height" {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(ChainHeightResp{Height: 123})
	}))
	defer server.Close()

	signer, err := NewClientWithEndpoint(
		config.BifrostChainConfiguration{},
		server.URL,
	)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}

	height, err := signer.ChainHeight()
	if err != nil {
		t.Fatalf("GetChainHeight failed: %v", err)
	}
	if height != 123 {
		t.Fatalf("expected height 123, got %d", height)
	}
}

func TestClientChainHeightPrefersDaemonRPC(t *testing.T) {
	t.Parallel()

	var sidecarCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sidecarCalls.Add(1)
		http.Error(w, "unexpected sidecar call", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json_rpc" {
			http.NotFound(w, r)
			return
		}

		var req struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		switch req.Method {
		case "get_info":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{
					"height": 124,
				},
			})
		default:
			http.Error(w, "unexpected method: "+req.Method, http.StatusBadRequest)
		}
	}))
	defer daemon.Close()

	signer, err := NewClientWithEndpoint(
		config.BifrostChainConfiguration{
			RPCHost: daemon.URL,
		},
		server.URL,
	)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}

	height, err := signer.ChainHeight()
	if err != nil {
		t.Fatalf("GetChainHeight failed: %v", err)
	}
	if height != 123 {
		t.Fatalf("expected daemon-backed height 123, got %d", height)
	}
	if sidecarCalls.Load() != 0 {
		t.Fatalf("expected daemon path to avoid sidecar, got %d sidecar calls", sidecarCalls.Load())
	}
}

func TestClientChainBlockMapsConflictToUnavailable(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"requested height above tip"}`))
	}))
	defer server.Close()

	signer, err := NewClientWithEndpoint(
		config.BifrostChainConfiguration{},
		server.URL,
	)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}

	_, err = signer.ChainBlock(200)
	if !errors.Is(err, ErrUnavailableBlock) {
		t.Fatalf("expected ErrUnavailableBlock, got %v", err)
	}
}

func TestClientChainBlockPrefersDaemonRPC(t *testing.T) {
	t.Parallel()

	var sidecarCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sidecarCalls.Add(1)
		http.Error(w, "unexpected sidecar call", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json_rpc" {
			http.NotFound(w, r)
			return
		}

		var req struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		switch req.Method {
		case "get_info":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{
					"height": 56,
				},
			})
		case "get_block":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{
					"block_header": map[string]any{
						"height":    42,
						"reward":    9001,
						"hash":      "hash-42",
						"prev_hash": "hash-41",
					},
					"json": `{"tx_hashes":["tx-1","tx-2"]}`,
				},
			})
		default:
			http.Error(w, "unexpected method: "+req.Method, http.StatusBadRequest)
		}
	}))
	defer daemon.Close()

	signer, err := NewClientWithEndpoint(
		config.BifrostChainConfiguration{
			RPCHost: daemon.URL,
		},
		server.URL,
	)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}

	block, err := signer.ChainBlock(42)
	if err != nil {
		t.Fatalf("GetChainBlock failed: %v", err)
	}
	if block.Height != 42 || block.Reward != 9001 || block.Hash != "hash-42" || block.PreviousHash != "hash-41" {
		t.Fatalf("unexpected block: %#v", block)
	}
	if len(block.TxHashes) != 2 || block.TxHashes[0] != "tx-1" || block.TxHashes[1] != "tx-2" {
		t.Fatalf("unexpected tx hashes: %#v", block.TxHashes)
	}
	if sidecarCalls.Load() != 0 {
		t.Fatalf("expected daemon path to avoid sidecar, got %d sidecar calls", sidecarCalls.Load())
	}
}

func TestClientChainHeightFallsBackToSidecar(t *testing.T) {
	t.Parallel()

	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "daemon unavailable", http.StatusServiceUnavailable)
	}))
	defer daemon.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/monero/chain/height" {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(ChainHeightResp{Height: 77})
	}))
	defer server.Close()

	signer, err := NewClientWithEndpoint(
		config.BifrostChainConfiguration{
			RPCHost: daemon.URL,
		},
		server.URL,
	)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}

	height, err := signer.ChainHeight()
	if err != nil {
		t.Fatalf("GetChainHeight failed: %v", err)
	}
	if height != 77 {
		t.Fatalf("expected sidecar fallback height 77, got %d", height)
	}
}

func TestClientChainBlockMapsDaemonTipToUnavailable(t *testing.T) {
	t.Parallel()

	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json_rpc" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Method != "get_info" {
			http.Error(w, "unexpected method: "+req.Method, http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{
				"height": 10,
			},
		})
	}))
	defer daemon.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unexpected sidecar call", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	signer, err := NewClientWithEndpoint(
		config.BifrostChainConfiguration{
			RPCHost: daemon.URL,
		},
		server.URL,
	)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}

	_, err = signer.ChainBlock(10)
	if !errors.Is(err, ErrUnavailableBlock) {
		t.Fatalf("expected ErrUnavailableBlock, got %v", err)
	}
}

func TestClientTxExists(t *testing.T) {
	t.Parallel()

	expectedPath := "/v1/monero/chain/tx/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/exists"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != expectedPath {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(TxExistsResp{Exists: true})
	}))
	defer server.Close()

	signer, err := NewClientWithEndpoint(
		config.BifrostChainConfiguration{},
		server.URL,
	)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}

	exists, err := signer.TxExists("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("TxExists failed: %v", err)
	}
	if !exists {
		t.Fatal("expected tx to exist")
	}
}

func TestClientTxExistsHTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	signer, err := NewClientWithEndpoint(
		config.BifrostChainConfiguration{},
		server.URL,
	)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}

	_, err = signer.TxExists("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if err == nil {
		t.Fatal("expected TxExists error")
	}
	if !strings.Contains(err.Error(), "status 503") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientResetScanner(t *testing.T) {
	t.Parallel()

	var receivedVault string
	var receivedBirthday int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/monero/scanner/reset" || r.Method != http.MethodPost {
			http.Error(w, "unexpected request: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		var req struct {
			VaultPubKey    string `json:"vault_pub_key"`
			BirthdayHeight int64  `json:"birthday_height"`
		}
		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		receivedVault = req.VaultPubKey
		receivedBirthday = req.BirthdayHeight
		_ = json.NewEncoder(w).Encode(map[string]any{
			"vault_pub_key":   req.VaultPubKey,
			"birthday_height": req.BirthdayHeight,
			"status":          "reset",
			"rescan":          true,
		})
	}))
	defer server.Close()

	signer, err := NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}

	err = signer.ResetScanner("thorpub1testvault", 50000)
	if err != nil {
		t.Fatalf("ResetScanner: %v", err)
	}
	if receivedVault != "thorpub1testvault" {
		t.Fatalf("expected vault thorpub1testvault, got %s", receivedVault)
	}
	if receivedBirthday != 50000 {
		t.Fatalf("expected birthday 50000, got %d", receivedBirthday)
	}
}

func TestNewClientAllowsAuthenticatedMocknetHTTP(t *testing.T) {
	t.Parallel()

	signer, err := NewClientWithEndpoint(
		config.BifrostChainConfiguration{
			ChainNetwork:        "mocknet",
			AuthorizationBearer: "test-token",
		},
		"http://xmr-frost-signer:8283",
	)
	if err != nil {
		t.Fatalf("expected mocknet service endpoint to be accepted, got %v", err)
	}
	if signer.endpoint != "http://xmr-frost-signer:8283" {
		t.Fatalf("expected signer endpoint to be preserved, got %q", signer.endpoint)
	}
}

func TestNewClientRejectsNonLoopbackWithoutAuth(t *testing.T) {
	t.Setenv("SIGNER_SEED_PHRASE", "test mocknet signer seed phrase")

	_, err := NewClientWithEndpoint(
		config.BifrostChainConfiguration{
			ChainNetwork: "mocknet",
		},
		"http://xmr-frost-signer:8283",
	)
	if err == nil {
		t.Fatal("expected non-loopback endpoint without explicit auth to be rejected")
	}
	if !strings.Contains(err.Error(), "requires authorization_bearer or username/password") {
		t.Fatalf("unexpected error: %v", err)
	}
}
