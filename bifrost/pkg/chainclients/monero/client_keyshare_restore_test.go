package monero

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog/log"

	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/monero/sidecar"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/config"
)

// recoverySidecarState mimics the real sidecar's restore contract: bech32
// vault identifiers resolve only for registered vaults, unregistered restores
// are rejected with a 400.
type recoverySidecarState struct {
	mu            sync.Mutex
	registered    map[string]bool
	calls         []string
	failRegisters int
}

func (s *recoverySidecarState) callOrder() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func newRecoverySidecarServer(t *testing.T) (*httptest.Server, *recoverySidecarState) {
	t.Helper()

	state := &recoverySidecarState{registered: map[string]bool{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/monero/vaults/register":
			var req sidecar.RegisterVaultReq
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			state.mu.Lock()
			state.calls = append(state.calls, "register")
			if state.failRegisters > 0 {
				state.failRegisters--
				state.mu.Unlock()
				http.Error(w, "sidecar starting", http.StatusServiceUnavailable)
				return
			}
			state.registered[req.VaultPubKey] = true
			state.mu.Unlock()
			_ = json.NewEncoder(w).Encode(sidecar.RegisterVaultResp{Registered: true})
		case "/v1/monero/keys/restore":
			var req struct {
				VaultPubKey string `json:"vault_pub_key"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			state.mu.Lock()
			state.calls = append(state.calls, "restore")
			registered := state.registered[req.VaultPubKey]
			state.mu.Unlock()
			if !registered {
				http.Error(w, "vault_pub_key does not match restored FROST keys", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]bool{"restored": true})
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server, state
}

func newRecoveryTestClient(t *testing.T, endpoint string) *Client {
	t.Helper()

	frostSigner, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, endpoint)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}
	return &Client{
		logger:                   log.Logger,
		chain:                    common.XMRChain,
		sidecar:                  frostSigner,
		vaultAddressMap:          map[string]common.Address{},
		recoveryRetryInterval:    time.Millisecond,
		recoveryRetryMaxAttempts: 5,
	}
}

func writeRecoveryFile(t *testing.T, dir string, vaultPubKey common.PubKey) string {
	t.Helper()

	path := filepath.Join(dir, "frost-recovery-"+vaultPubKey.String()+".hex")
	if err := os.WriteFile(path, []byte("deadbeef"), 0o600); err != nil {
		t.Fatalf("write recovery file: %v", err)
	}
	return path
}

func TestRestoreKeyshareRejectedWhileVaultUnregistered(t *testing.T) {
	t.Parallel()

	server, _ := newRecoverySidecarServer(t)
	c := newRecoveryTestClient(t, server.URL)
	vaultPubKey, _ := testVaultPubKeyAndAddress(t)

	err := c.RestoreKeyshare(vaultPubKey, []byte("deadbeef"))
	if err == nil {
		t.Fatal("expected restore to fail for unregistered vault")
	}
	if !strings.Contains(err.Error(), "does not match restored FROST keys") {
		t.Fatalf("expected unregistered-vault rejection, got: %v", err)
	}
}

func TestRestoreKeysharesRegistersVaultBeforeRestore(t *testing.T) {
	t.Parallel()

	server, state := newRecoverySidecarServer(t)
	c := newRecoveryTestClient(t, server.URL)
	vaultPubKey, vaultAddress := testVaultPubKeyAndAddress(t)

	dir := t.TempDir()
	path := writeRecoveryFile(t, dir, vaultPubKey)

	c.restoreKeysharesFromDir(dir)

	calls := state.callOrder()
	if len(calls) != 2 || calls[0] != "register" || calls[1] != "restore" {
		t.Fatalf("expected register before restore, got calls %v", calls)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected recovery file to be removed after restore, stat err: %v", err)
	}
	if got, ok := c.vaultAddressMap[vaultPubKey.String()]; !ok || !got.Equals(vaultAddress) {
		t.Fatalf("expected vault address mapping %s, got %s (present=%t)", vaultAddress, got, ok)
	}
}

func TestRestoreKeysharesRetriesWhileSidecarStarting(t *testing.T) {
	t.Parallel()

	server, state := newRecoverySidecarServer(t)
	state.failRegisters = 2
	c := newRecoveryTestClient(t, server.URL)
	vaultPubKey, _ := testVaultPubKeyAndAddress(t)

	dir := t.TempDir()
	path := writeRecoveryFile(t, dir, vaultPubKey)

	c.restoreKeysharesFromDir(dir)

	calls := state.callOrder()
	if calls[len(calls)-1] != "restore" {
		t.Fatalf("expected restore to eventually succeed, got calls %v", calls)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected recovery file to be removed after retried restore, stat err: %v", err)
	}
}

func TestRestoreKeysharesKeepsFileWhenSidecarUnavailable(t *testing.T) {
	t.Parallel()

	server, state := newRecoverySidecarServer(t)
	state.failRegisters = 100 // more than max attempts
	c := newRecoveryTestClient(t, server.URL)
	vaultPubKey, _ := testVaultPubKeyAndAddress(t)

	dir := t.TempDir()
	path := writeRecoveryFile(t, dir, vaultPubKey)

	c.restoreKeysharesFromDir(dir)

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected recovery file to be kept for next startup, stat err: %v", err)
	}
}
