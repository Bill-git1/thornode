package utxo

import (
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient"
	btctxscript "gitlab.com/thorchain/thornode/v3/bifrost/txscript/txscript"
	"gitlab.com/thorchain/thornode/v3/cmd"
	"gitlab.com/thorchain/thornode/v3/common"
)

const testMnemonic = "master master master master master master master master master master master master master master master master master master master master master master master notice"

func TestNewClientAllowsSlowImportAddressDuringStartup(t *testing.T) {
	if utxoStartupRPCTimeout <= time.Second {
		t.Fatalf("expected startup RPC timeout to exceed 1s, got %s", utxoStartupRPCTimeout)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read request body: %v", err)
		}
		requestBody := string(body)

		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(requestBody, `"getaddressinfo"`):
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"ismine":false,"iswatchonly":false}}`))
		case strings.Contains(requestBody, `"importaddress"`):
			time.Sleep(1500 * time.Millisecond)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
		default:
			t.Fatalf("expected getaddressinfo or importaddress RPC during startup, got %s", body)
		}
	}))
	defer server.Close()

	keys := newTestKeys(t, "master", testMnemonic)
	if _, err := NewClient(common.LTCChain, server.URL, keys); err != nil {
		t.Fatalf("expected LTC startup importaddress to succeed with a slow local RPC, got %v", err)
	}
}

func TestNewClientSerializesConcurrentImportAddressStartup(t *testing.T) {
	var serverMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read request body: %v", err)
		}
		requestBody := string(body)

		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(requestBody, `"getaddressinfo"`):
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"ismine":false,"iswatchonly":false}}`))
		case strings.Contains(requestBody, `"importaddress"`):
			serverMu.Lock()
			defer serverMu.Unlock()

			time.Sleep(1500 * time.Millisecond)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
		default:
			t.Fatalf("expected getaddressinfo or importaddress RPC during startup, got %s", body)
		}
	}))
	defer server.Close()

	const clientCount = 4
	keys := make([]*thorclient.Keys, 0, clientCount)
	for i := 0; i < clientCount; i++ {
		keys = append(keys, newTestKeys(t, "user"+strconv.Itoa(i), testMnemonic))
	}

	errs := make(chan error, clientCount)
	start := make(chan struct{})
	for i := 0; i < clientCount; i++ {
		go func(i int) {
			<-start
			_, err := NewClient(common.LTCChain, server.URL, keys[i])
			errs <- err
		}(i)
	}
	close(start)

	for i := 0; i < clientCount; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("expected queued LTC startup imports to succeed without timing out, got %v", err)
		}
	}
}

func TestNewClientSkipsImportWhenAddressAlreadyTracked(t *testing.T) {
	importCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read request body: %v", err)
		}
		requestBody := string(body)

		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(requestBody, `"getaddressinfo"`):
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"ismine":false,"iswatchonly":true}}`))
		case strings.Contains(requestBody, `"importaddress"`):
			importCalls++
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
		default:
			t.Fatalf("unexpected RPC during startup: %s", requestBody)
		}
	}))
	defer server.Close()

	keys := newTestKeys(t, "master", testMnemonic)
	if _, err := NewClient(common.DOGEChain, server.URL, keys); err != nil {
		t.Fatalf("expected DOGE startup to skip import for an already tracked address, got %v", err)
	}
	if importCalls != 0 {
		t.Fatalf("expected no importaddress call when address is already tracked, got %d", importCalls)
	}
}

func TestNewClientSkipsImportWhenDogecoinUsesValidateAddress(t *testing.T) {
	importCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read request body: %v", err)
		}
		requestBody := string(body)

		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(requestBody, `"getaddressinfo"`):
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"Method not found"}}`))
		case strings.Contains(requestBody, `"validateaddress"`):
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"isvalid":true,"ismine":false,"iswatchonly":true}}`))
		case strings.Contains(requestBody, `"importaddress"`):
			importCalls++
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
		default:
			t.Fatalf("unexpected RPC during startup: %s", requestBody)
		}
	}))
	defer server.Close()

	keys := newTestKeys(t, "master", testMnemonic)
	if _, err := NewClient(common.DOGEChain, server.URL, keys); err != nil {
		t.Fatalf("expected DOGE startup to fall back to validateaddress, got %v", err)
	}
	if importCalls != 0 {
		t.Fatalf("expected no importaddress call when validateaddress reports watch-only, got %d", importCalls)
	}
}

func TestGetMemoScriptsSplitsLongMemoForUTXO(t *testing.T) {
	client := &Client{chain: common.BTCChain}

	scripts, err := client.getMemoScripts(strings.Repeat("a", 81))
	if err != nil {
		t.Fatalf("expected long memo to be split into scripts, got %v", err)
	}
	if len(scripts) != 2 {
		t.Fatalf("expected 2 scripts for an 81-byte BTC memo, got %d", len(scripts))
	}
}

func TestZECMemoScriptsUseTransparentContinuationOutputs(t *testing.T) {
	client := &Client{chain: common.ZECChain}

	scripts, err := client.getMemoScripts(strings.Repeat("a", 100))
	if err != nil {
		t.Fatalf("expected long ZEC memo to be split into scripts, got %v", err)
	}
	if len(scripts) != 3 {
		t.Fatalf("expected 3 scripts for a 100-byte ZEC memo, got %d", len(scripts))
	}

	asm, err := btctxscript.DisasmString(scripts[1])
	if err != nil {
		t.Fatalf("expected ZEC continuation script to disassemble, got %v", err)
	}
	if !strings.HasPrefix(asm, "OP_DUP OP_HASH160 ") || !strings.HasSuffix(asm, " OP_EQUALVERIFY OP_CHECKSIG") {
		t.Fatalf("expected ZEC continuation script to be transparent P2PKH, got %q", asm)
	}
}

func TestZECSpentUtxosOnlyFinalizedAfterSuccessfulBroadcast(t *testing.T) {
	client := &Client{
		chain:             common.ZECChain,
		spentUtxos:        make(map[string]bool),
		pendingSpentUtxos: make(map[string][]string),
	}
	payload := []byte{0x01, 0x02, 0x03}
	payloadHex := hex.EncodeToString(payload)
	const key = "txid-0"

	client.rememberPendingSpentUtxos(payload, map[string]int64{key: 1})
	if client.spentUtxos[key] || len(client.pendingSpentUtxos[payloadHex]) != 1 {
		t.Fatal("signing must reserve inputs without marking them spent")
	}
	client.finalizePendingSpentUtxos(payloadHex)
	if !client.spentUtxos[key] {
		t.Fatal("expected successful ZEC broadcast to mark selected UTXOs spent")
	}
}

func newTestKeys(t *testing.T, name, mnemonic string) *thorclient.Keys {
	t.Helper()

	registry := codectypes.NewInterfaceRegistry()
	cryptocodec.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)

	kr := keyring.NewInMemory(cdc, func(options *keyring.Options) {
		options.SupportedAlgos = keyring.SigningAlgoList{hd.Secp256k1}
	})

	if _, err := kr.NewAccount(name, mnemonic, "", cmd.THORChainHDPath, hd.Secp256k1); err != nil {
		t.Fatalf("failed to add account to keyring: %v", err)
	}

	return thorclient.NewKeysWithKeybase(kr, name, "")
}
