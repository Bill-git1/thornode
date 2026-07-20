package sidecar

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gitlab.com/thorchain/thornode/v3/config"
)

func TestSignerAndDaemonCredentialsAreIsolated(t *testing.T) {
	t.Parallel()

	var signerAuthorization string
	signerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signerAuthorization = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(Info{
			Protocol:     ProtocolName,
			APIMajor:     APIMajorV2,
			GitCommit:    strings.Repeat("a", 40),
			Network:      "mocknet",
			StateSchema:  StateSchemaV4,
			Capabilities: []string{CapabilityBalanceReconciliation},
		})
	}))
	defer signerServer.Close()

	var daemonAuthorization string
	daemonServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		daemonAuthorization = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      "0",
			"result":  map[string]any{"height": 2},
		})
	}))
	defer daemonServer.Close()

	client, err := NewClientWithEndpoint(config.BifrostChainConfiguration{
		RPCHost:                   daemonServer.URL,
		UserName:                  "daemon-user",
		Password:                  "daemon-password",
		SignerAuthorizationBearer: "signer-token",
	}, signerServer.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, err = client.Info(); err != nil {
		t.Fatalf("signer info: %v", err)
	}
	if _, err = client.ChainHeight(); err != nil {
		t.Fatalf("daemon height: %v", err)
	}

	if signerAuthorization != "Bearer signer-token" {
		t.Fatalf("unexpected signer authorization: %q", signerAuthorization)
	}
	if !strings.HasPrefix(daemonAuthorization, "Basic ") {
		t.Fatalf("expected daemon Basic authorization, got %q", daemonAuthorization)
	}
	if strings.Contains(daemonAuthorization, "signer-token") {
		t.Fatal("signer bearer leaked to monerod")
	}
}

func TestNewClientRejectsAmbiguousSignerAuthentication(t *testing.T) {
	t.Parallel()

	_, err := NewClientWithEndpoint(config.BifrostChainConfiguration{
		SignerAuthorizationBearer: "bearer",
		SignerUserName:            "user",
		SignerPassword:            "password",
	}, "http://127.0.0.1:8283")
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected ambiguous signer auth rejection, got %v", err)
	}
}

func TestClientEndpointsRejectEmbeddedCredentialsAndURLMetadata(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		signer   string
		daemon   string
		contains string
	}{
		{name: "signer userinfo", signer: "https://user:pass@signer.example", contains: "must not contain URL credentials"},
		{name: "signer query", signer: "https://signer.example?token=secret", contains: "query or fragment"},
		{name: "signer path", signer: "https://signer.example/prefix", contains: "must not contain a path"},
		{name: "malformed signer", signer: "https://user:pass@%zz", contains: "invalid sidecar endpoint URL"},
		{name: "daemon userinfo", signer: "http://127.0.0.1:8283", daemon: "https://user:pass@daemon.example", contains: "must not contain URL credentials"},
		{name: "daemon query", signer: "http://127.0.0.1:8283", daemon: "https://daemon.example?token=secret", contains: "query or fragment"},
		{name: "malformed daemon", signer: "http://127.0.0.1:8283", daemon: "https://user:pass@%zz", contains: "invalid monero rpc host URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.BifrostChainConfiguration{
				RPCHost:                   tc.daemon,
				SignerAuthorizationBearer: "explicit-signer-token",
			}
			_, err := NewClientWithEndpoint(cfg, tc.signer)
			if err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("expected %q rejection, got %v", tc.contains, err)
			}
			if strings.Contains(fmt.Sprint(err), "user:pass") || strings.Contains(fmt.Sprint(err), "token=secret") {
				t.Fatalf("endpoint error leaked credential material: %v", err)
			}
		})
	}
}

func TestRestoreKeysRequiresExplicitTrue(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "missing", body: `{}`, wantErr: true},
		{name: "null", body: `{"restored":null}`, wantErr: true},
		{name: "false", body: `{"restored":false}`, wantErr: true},
		{name: "true", body: `{"restored":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			client, err := NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			_, err = client.RestoreKeys("vault", []byte("encrypted"))
			if (err != nil) != tc.wantErr {
				t.Fatalf("RestoreKeys error = %v, wantErr %t", err, tc.wantErr)
			}
		})
	}
}

func TestRequireCompatibilityFailsClosed(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(Info{
			Protocol:    ProtocolName,
			APIMajor:    APIMajorV2,
			GitCommit:   strings.Repeat("b", 40),
			Network:     "chainnet",
			StateSchema: StateSchemaV4,
			Capabilities: []string{
				CapabilityBalanceReconciliation,
				CapabilityScannerBoundedRewind,
				CapabilitySharePlanQuarantine,
			},
		})
	}))
	defer server.Close()

	client, err := NewClientWithEndpoint(config.BifrostChainConfiguration{
		SignerExpectedNetwork: "chainnet",
		SignerExpectedCommit:  strings.Repeat("b", 40),
	}, server.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, err = client.RequireCompatibility(
		CapabilityBalanceReconciliation,
		CapabilityScannerBoundedRewind,
		CapabilitySharePlanQuarantine,
	); err != nil {
		t.Fatalf("expected compatible signer: %v", err)
	}
	if _, err = client.RequireCompatibility(CapabilityAtomicOutgoingV2); err == nil {
		t.Fatal("expected missing capability to fail closed")
	}
}

func TestRequireCompatibilityTreatsRevisionAsMetadataAndRejectsSchemaMismatch(t *testing.T) {
	t.Parallel()

	info := Info{
		Protocol: ProtocolName, APIMajor: APIMajorV2, GitCommit: strings.Repeat("c", 40),
		Network: "chainnet", StateSchema: StateSchemaV4,
		Capabilities: []string{CapabilityBalanceReconciliation},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(info)
	}))
	defer server.Close()

	client, err := NewClientWithEndpoint(config.BifrostChainConfiguration{
		SignerExpectedNetwork: "chainnet",
		SignerExpectedCommit:  strings.Repeat("d", 40),
	}, server.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, err = client.RequireCompatibility(CapabilityBalanceReconciliation); err != nil {
		t.Fatalf("source revision mismatch should be deployment metadata, got: %v", err)
	}

	info.StateSchema = 2
	if _, err = client.RequireCompatibility(CapabilityBalanceReconciliation); err == nil {
		t.Fatal("expected unreviewed state schema rejection")
	}
}

func TestNonLoopbackHTTPRequiresExplicitDevelopmentEscapeHatch(t *testing.T) {
	t.Parallel()

	cfg := config.BifrostChainConfiguration{
		SignerAuthorizationBearer: "token",
		SignerExpectedNetwork:     "chainnet",
	}
	if _, err := NewClientWithEndpoint(cfg, "http://signer.internal:8283"); err == nil {
		t.Fatal("expected non-loopback cleartext signer rejection")
	}
	cfg.SignerAllowInsecureHTTP = true
	if _, err := NewClientWithEndpoint(cfg, "http://signer.internal:8283"); err != nil {
		t.Fatalf("explicit development escape hatch rejected: %v", err)
	}
	cfg.SignerExpectedNetwork = "mainnet"
	if _, err := NewClientWithEndpoint(cfg, "http://signer.internal:8283"); err == nil {
		t.Fatal("mainnet must reject non-loopback cleartext even with escape hatch")
	}
}

func TestHTTPErrorKeepsOnlySanitizedMachineCode(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"scanner_hash_mismatch","error":"secret upstream detail"}`))
	}))
	defer server.Close()
	client, err := NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	_, err = client.Info()
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("expected HTTPError, got %v", err)
	}
	if httpErr.Code != "scanner_hash_mismatch" {
		t.Fatalf("unexpected machine code %q", httpErr.Code)
	}
	if strings.Contains(err.Error(), "secret upstream detail") {
		t.Fatal("remote error body leaked into local error text")
	}
}
