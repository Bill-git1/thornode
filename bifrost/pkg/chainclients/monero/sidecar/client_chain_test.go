package sidecar

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
			"reset":                true,
			"birthday_height":      req.BirthdayHeight,
			"reset_epoch":          7,
			"expected_next_height": req.BirthdayHeight,
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

func TestSyncAtRecoversDurableResetEpochAfterClientRestart(t *testing.T) {
	t.Parallel()

	postCalls := 0
	nextHeight := uint64(50)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/monero/scanner/state":
			_ = json.NewEncoder(w).Encode(ScannerState{
				ResetEpoch: 7, NextHeight: &nextHeight, StrictAfterReset: true,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/monero/scanner/sync":
			postCalls++
			var req SyncReq
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if postCalls == 1 {
				if req.ResetEpoch != nil || req.ExpectedNextHeight != nil {
					t.Errorf("restart request unexpectedly had continuity fields: %+v", req)
				}
				http.Error(w, "reset epoch required", http.StatusConflict)
				return
			}
			if req.ResetEpoch == nil || *req.ResetEpoch != 7 ||
				req.ExpectedNextHeight == nil || *req.ExpectedNextHeight != 50 {
				t.Errorf("retry did not bind recovered continuity: %+v", req)
			}
			_ = json.NewEncoder(w).Encode(Transfers{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, err = client.SyncAt(50, 60, strings.Repeat("a", 64), strings.Repeat("b", 64), 10); err != nil {
		t.Fatalf("SyncAt continuity recovery: %v", err)
	}
	if postCalls != 2 {
		t.Fatalf("expected one failed request and one bound retry, got %d", postCalls)
	}
}

func TestSyncAtReturnsTypedRewindForOlderAuthenticatedCursor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		epoch  uint64
		strict bool
	}{
		{name: "strict reset cursor", epoch: 7, strict: true},
		{name: "fresh durable baseline", epoch: 0, strict: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var postCalls atomic.Int32
			nextHeight := uint64(40)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == scannerStatePath:
					_ = json.NewEncoder(w).Encode(ScannerState{
						ResetEpoch:       tt.epoch,
						NextHeight:       &nextHeight,
						StrictAfterReset: tt.strict,
					})
				case r.Method == http.MethodPost && r.URL.Path == "/v1/monero/scanner/sync":
					postCalls.Add(1)
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(`{"code":"scanner_cursor_conflict"}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			client, err := NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			_, err = client.SyncAt(50, 60, strings.Repeat("a", 64), strings.Repeat("b", 64), 10)
			var rewindErr *ScannerRewindRequiredError
			if !errors.As(err, &rewindErr) {
				t.Fatalf("expected typed rewind error, got %v", err)
			}
			if rewindErr.RequestedHeight != 50 || rewindErr.NextHeight != 40 || rewindErr.ResetEpoch != tt.epoch {
				t.Fatalf("unexpected rewind error: %+v", rewindErr)
			}
			if got := postCalls.Load(); got != 1 {
				t.Fatalf("older cursor must not be retried at the wrong height; got %d POSTs", got)
			}
		})
	}
}

func TestSyncAtRetriesAuthenticatedExactReplayOnce(t *testing.T) {
	t.Parallel()

	blockHash := strings.Repeat("a", 64)
	parentHash := strings.Repeat("b", 64)
	nextHeight := uint64(51)
	lastHeight := uint64(50)
	var postCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == scannerStatePath:
			_ = json.NewEncoder(w).Encode(ScannerState{
				ResetEpoch:     0,
				NextHeight:     &nextHeight,
				LastHeight:     &lastHeight,
				LastBlockHash:  blockHash,
				LastParentHash: parentHash,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/monero/scanner/sync":
			call := postCalls.Add(1)
			var req SyncReq
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if call == 1 {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"code":"scanner_cursor_conflict"}`))
				return
			}
			if req.ResetEpoch == nil || *req.ResetEpoch != 0 ||
				req.ExpectedNextHeight == nil || *req.ExpectedNextHeight != 50 {
				t.Errorf("exact replay retry omitted authenticated continuity: %+v", req)
			}
			_ = json.NewEncoder(w).Encode(Transfers{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, err = client.SyncAt(50, 60, blockHash, parentHash, 10); err != nil {
		t.Fatalf("exact replay recovery: %v", err)
	}
	if got := postCalls.Load(); got != 2 {
		t.Fatalf("expected one conflict and one exact replay, got %d POSTs", got)
	}
}

func TestSyncAtCursorAheadNonReplayIsNotTypedOrRetried(t *testing.T) {
	t.Parallel()

	nextHeight := uint64(60)
	lastHeight := uint64(59)
	var postCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == scannerStatePath:
			_ = json.NewEncoder(w).Encode(ScannerState{
				ResetEpoch:     0,
				NextHeight:     &nextHeight,
				LastHeight:     &lastHeight,
				LastBlockHash:  strings.Repeat("c", 64),
				LastParentHash: strings.Repeat("d", 64),
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/monero/scanner/sync":
			postCalls.Add(1)
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":"scanner_cursor_conflict"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	_, err = client.SyncAt(50, 60, strings.Repeat("a", 64), strings.Repeat("b", 64), 10)
	if err == nil {
		t.Fatal("expected cursor-ahead conflict")
	}
	var rewindErr *ScannerRewindRequiredError
	if errors.As(err, &rewindErr) {
		t.Fatalf("cursor ahead must not be reported as a rewind: %+v", rewindErr)
	}
	if !IsHTTPStatus(err, http.StatusConflict) {
		t.Fatalf("expected original cursor-ahead conflict, got %v", err)
	}
	if got := postCalls.Load(); got != 1 {
		t.Fatalf("cursor-ahead request must not be retried, got %d POSTs", got)
	}
}

func TestConcurrentStaleRegistrationCannotDowngradeContinuity(t *testing.T) {
	t.Parallel()

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var closeFirst sync.Once
	var registerCalls atomic.Int32
	var syncCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/monero/vaults/register":
			call := registerCalls.Add(1)
			if call == 1 {
				closeFirst.Do(func() { close(firstStarted) })
				<-releaseFirst
				next := uint64(100)
				_ = json.NewEncoder(w).Encode(RegisterVaultResp{
					Registered:         true,
					ResetEpoch:         1,
					ExpectedNextHeight: &next,
				})
				return
			}
			next := uint64(40)
			_ = json.NewEncoder(w).Encode(RegisterVaultResp{
				Registered:         true,
				ResetEpoch:         2,
				ExpectedNextHeight: &next,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/monero/scanner/sync":
			syncCalls.Add(1)
			_ = json.NewEncoder(w).Encode(Transfers{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	firstResult := make(chan error, 1)
	go func() {
		_, callErr := client.RegisterVault(RegisterVaultReq{
			VaultPubKey: "first", VaultCreationHeight: 100,
		})
		firstResult <- callErr
	}()
	<-firstStarted
	if _, err = client.RegisterVault(RegisterVaultReq{
		VaultPubKey: "second", VaultCreationHeight: 40,
	}); err != nil {
		t.Fatalf("newer registration: %v", err)
	}
	close(releaseFirst)
	if err = <-firstResult; err != nil {
		t.Fatalf("stale registration response: %v", err)
	}

	continuity, ok := client.cachedScannerContinuity()
	if !ok || continuity.resetEpoch != 2 || continuity.nextHeight != 40 {
		t.Fatalf("stale response downgraded continuity: cached=%+v present=%t", continuity, ok)
	}
	_, err = client.SyncAt(50, 60, strings.Repeat("a", 64), strings.Repeat("b", 64), 10)
	var rewindErr *ScannerRewindRequiredError
	if !errors.As(err, &rewindErr) || rewindErr.ResetEpoch != 2 || rewindErr.NextHeight != 40 {
		t.Fatalf("expected cached epoch-2 rewind, got %v", err)
	}
	if got := syncCalls.Load(); got != 0 {
		t.Fatalf("cached registration rewind should stop the wrong POST, got %d", got)
	}
}

func TestPreflightScannerHeightRequiresExactAheadReplay(t *testing.T) {
	t.Parallel()

	blockHash := strings.Repeat("a", 64)
	parentHash := strings.Repeat("b", 64)
	nextHeight := uint64(51)
	lastHeight := uint64(50)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != scannerStatePath {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(ScannerState{
			ResetEpoch:     0,
			NextHeight:     &nextHeight,
			LastHeight:     &lastHeight,
			LastBlockHash:  blockHash,
			LastParentHash: parentHash,
		})
	}))
	defer server.Close()

	client, err := NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if err = client.PreflightScannerHeight(50, blockHash, parentHash); err != nil {
		t.Fatalf("exact replay preflight: %v", err)
	}
	if err = client.PreflightScannerHeight(50, strings.Repeat("c", 64), parentHash); err == nil {
		t.Fatal("expected non-exact cursor-ahead preflight to fail")
	} else {
		var rewindErr *ScannerRewindRequiredError
		if errors.As(err, &rewindErr) {
			t.Fatalf("cursor ahead must not be typed as a rewind: %+v", rewindErr)
		}
	}
}

func TestSyncAtRejectsMalformedScannerContinuityWithoutRetry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		stateBody string
	}{
		{
			name:      "nil next height",
			stateBody: `{"reset_epoch":0,"next_height":null,"strict_after_reset":false}`,
		},
		{
			name:      "next height outside int64 range",
			stateBody: `{"reset_epoch":0,"next_height":9223372036854775808,"strict_after_reset":false}`,
		},
		{
			name: "non-contiguous last height",
			stateBody: `{"reset_epoch":0,"next_height":51,"last_height":40,` +
				`"last_block_hash":"` + strings.Repeat("a", 64) + `",` +
				`"last_parent_hash":"` + strings.Repeat("b", 64) + `",` +
				`"strict_after_reset":false}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var postCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == scannerStatePath:
					_, _ = w.Write([]byte(tt.stateBody))
				case r.Method == http.MethodPost && r.URL.Path == "/v1/monero/scanner/sync":
					postCalls.Add(1)
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(`{"code":"scanner_cursor_conflict"}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			client, err := NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			_, err = client.SyncAt(50, 60, strings.Repeat("a", 64), strings.Repeat("b", 64), 10)
			if err == nil {
				t.Fatal("expected malformed continuity to fail closed")
			}
			var rewindErr *ScannerRewindRequiredError
			if errors.As(err, &rewindErr) {
				t.Fatalf("malformed continuity must not become a rewind instruction: %+v", rewindErr)
			}
			if got := postCalls.Load(); got != 1 {
				t.Fatalf("malformed continuity must not be retried, got %d POSTs", got)
			}
		})
	}
}

func TestRegisterVaultRejectsNilContinuityCursor(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/monero/vaults/register" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"registered":true,"reset_epoch":0}`))
	}))
	defer server.Close()

	client, err := NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, err = client.RegisterVault(RegisterVaultReq{VaultPubKey: "vault"}); err == nil {
		t.Fatal("expected nil registration cursor to fail closed")
	}
	if _, ok := client.cachedScannerContinuity(); ok {
		t.Fatal("invalid registration response must not populate continuity cache")
	}
}

func TestNewClientAllowsAuthenticatedMocknetHTTP(t *testing.T) {
	t.Parallel()

	signer, err := NewClientWithEndpoint(
		config.BifrostChainConfiguration{
			ChainNetwork:              "mocknet",
			SignerAuthorizationBearer: "test-token",
			SignerExpectedNetwork:     "mocknet",
			SignerAllowInsecureHTTP:   true,
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
			ChainNetwork:            "mocknet",
			SignerExpectedNetwork:   "mocknet",
			SignerAllowInsecureHTTP: true,
		},
		"http://xmr-frost-signer:8283",
	)
	if err == nil {
		t.Fatal("expected non-loopback endpoint without explicit auth to be rejected")
	}
	if !strings.Contains(err.Error(), "requires signer_authorization_bearer or signer_username/signer_password") {
		t.Fatalf("unexpected error: %v", err)
	}
}
