package monero

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gitlab.com/thorchain/thornode/v3/bifrost/metrics"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/monero/sidecar"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient"
	tssp "gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/tss"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/config"
)

// failClosedRequiredCapabilities mirrors the capability set NewClient demands
// from the signer. Keeping the list here (rather than reaching into client.go)
// makes an accidental relaxation of the handshake fail this test rather than
// silently agreeing with itself.
var failClosedRequiredCapabilities = []string{
	sidecar.CapabilityAtomicOutgoingV2,
	sidecar.CapabilityBalanceReconciliation,
	sidecar.CapabilityBoundRecovery,
	sidecar.CapabilityPerVaultKeyStatus,
	sidecar.CapabilityRestartReservation,
	sidecar.CapabilityDeadOutgoingQuarantine,
	sidecar.CapabilityScannerContinuity,
	sidecar.CapabilityDurableInboundReplay,
	sidecar.CapabilityScannerBoundedRewind,
	sidecar.CapabilitySharePlanQuarantine,
	sidecar.CapabilityVerifiedAbsenceBalance,
}

// failClosedSignerNetwork is the network both the config and a well-behaved
// signer advertise in the control case.
const failClosedSignerNetwork = "mocknet"

// failClosedBridge is a non-nil ThorchainBridge that never needs to answer a
// call: NewClient only nil-checks it, and the block scanner short-circuits on
// a configured StartBlockHeight before it would reach the bridge.
type failClosedBridge struct {
	thorclient.ThorchainBridge
}

var (
	failClosedMetricsOnce sync.Once
	failClosedMetrics     *metrics.Metrics
	failClosedMetricsErr  error
)

// failClosedTestMetrics returns a process-wide Metrics instance. The prometheus
// collectors are package-level, so registering them twice panics.
func failClosedTestMetrics(t *testing.T) *metrics.Metrics {
	t.Helper()
	failClosedMetricsOnce.Do(func() {
		failClosedMetrics, failClosedMetricsErr = metrics.NewMetrics(config.BifrostMetricsConfiguration{
			Enabled:      false,
			ListenPort:   9000,
			ReadTimeout:  time.Second,
			WriteTimeout: time.Second,
			Chains:       common.Chains{common.XMRChain},
		})
	})
	if failClosedMetricsErr != nil {
		t.Fatalf("new metrics: %v", failClosedMetricsErr)
	}
	return failClosedMetrics
}

// newFailClosedSignerServer stands up a loopback signer that advertises exactly
// the compatibility Info it is handed.
func newFailClosedSignerServer(t *testing.T, info sidecar.Info) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/monero/info":
			if err := json.NewEncoder(w).Encode(info); err != nil {
				t.Errorf("encode signer info: %v", err)
			}
		case r.Method == http.MethodPost && r.URL.Path == "/v1/monero/sessions/clear":
			if err := json.NewEncoder(w).Encode(map[string]any{
				"cleared_sessions": 0,
				"status":           "ok",
			}); err != nil {
				t.Errorf("encode clear sessions: %v", err)
			}
		default:
			http.Error(w, "unexpected signer call: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// failClosedSignerInfo is the compatibility contract a fully-correct signer
// reports. Cases mutate a copy of it to model a bad signer.
func failClosedSignerInfo() sidecar.Info {
	return sidecar.Info{
		Protocol:        sidecar.ProtocolName,
		APIMajor:        sidecar.APIMajorV2,
		SoftwareVersion: "0.0.0-test",
		GitCommit:       strings.Repeat("a", 40),
		Network:         failClosedSignerNetwork,
		StateSchema:     sidecar.StateSchemaV4,
		Capabilities:    append([]string(nil), failClosedRequiredCapabilities...),
	}
}

// failClosedChainConfig is a config that is valid in every respect except for
// whatever the individual case chooses to break.
func failClosedChainConfig(endpoint, expectedNetwork string) config.BifrostChainConfiguration {
	return config.BifrostChainConfiguration{
		ChainID:               common.XMRChain,
		SignerEndpoint:        endpoint,
		SignerExpectedNetwork: expectedNetwork,
		BlockScanner: config.BifrostBlockScannerConfiguration{
			ChainID:            common.XMRChain,
			StartBlockHeight:   1,
			HTTPRequestTimeout: 5 * time.Second,
			PrefetchBlocks:     1,
		},
	}
}

// TestNewClientFailsClosedOnSignerContract asserts the headline fail-closed
// property of the XMR client: it refuses to construct unless the operator has
// pinned the signer's network AND the signer proves it speaks the full,
// reviewed capability contract.
func TestNewClientFailsClosedOnSignerContract(t *testing.T) {
	partialCapabilities := append([]string(nil), failClosedRequiredCapabilities[:len(failClosedRequiredCapabilities)-1]...)

	cases := []struct {
		name string
		// mutate the signer's advertised compatibility contract
		signerInfo func(sidecar.Info) sidecar.Info
		// the network the operator pinned in config
		expectedNetwork string
		// empty means the case must be accepted
		wantErrContains string
	}{
		{
			name:            "empty signer expected network is rejected",
			signerInfo:      func(info sidecar.Info) sidecar.Info { return info },
			expectedNetwork: "",
			wantErrContains: "signer_expected_network is required",
		},
		{
			name:            "whitespace signer expected network is rejected",
			signerInfo:      func(info sidecar.Info) sidecar.Info { return info },
			expectedNetwork: "   ",
			wantErrContains: "signer_expected_network is required",
		},
		{
			name: "signer advertising a different network is rejected",
			signerInfo: func(info sidecar.Info) sidecar.Info {
				info.Network = "mainnet"
				return info
			},
			expectedNetwork: failClosedSignerNetwork,
			wantErrContains: "does not match expected",
		},
		{
			name: "signer with an incomplete capability set is rejected",
			signerInfo: func(info sidecar.Info) sidecar.Info {
				info.Capabilities = partialCapabilities
				return info
			},
			expectedNetwork: failClosedSignerNetwork,
			wantErrContains: "sidecar missing required capability",
		},
		{
			name:            "fully compatible signer is accepted",
			signerInfo:      func(info sidecar.Info) sidecar.Info { return info },
			expectedNetwork: failClosedSignerNetwork,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := newFailClosedSignerServer(t, tc.signerInfo(failClosedSignerInfo()))
			cfg := failClosedChainConfig(server.URL, tc.expectedNetwork)

			client, err := NewClient(nil, cfg, &tssp.TssServer{}, &failClosedBridge{}, failClosedTestMetrics(t))

			if tc.wantErrContains == "" {
				if err != nil {
					t.Fatalf("expected the compatible signer to be accepted, got error: %v", err)
				}
				if client == nil {
					t.Fatal("expected a client for the compatible signer, got nil")
				}
				return
			}
			if err == nil {
				t.Fatalf("expected NewClient to fail closed with %q, got a client", tc.wantErrContains)
			}
			if !strings.Contains(err.Error(), tc.wantErrContains) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErrContains, err)
			}
			if client != nil {
				t.Fatal("expected no client when the signer contract is not satisfied")
			}
		})
	}
}

// TestNewClientRejectsEachMissingSignerCapability walks the capability list and
// drops one capability at a time, so weakening any single element of the
// eleven-capability handshake fails here.
func TestNewClientRejectsEachMissingSignerCapability(t *testing.T) {
	for _, dropped := range failClosedRequiredCapabilities {
		t.Run(dropped, func(t *testing.T) {
			info := failClosedSignerInfo()
			capabilities := make([]string, 0, len(failClosedRequiredCapabilities)-1)
			for _, capability := range failClosedRequiredCapabilities {
				if capability == dropped {
					continue
				}
				capabilities = append(capabilities, capability)
			}
			info.Capabilities = capabilities

			server := newFailClosedSignerServer(t, info)
			cfg := failClosedChainConfig(server.URL, failClosedSignerNetwork)

			client, err := NewClient(nil, cfg, &tssp.TssServer{}, &failClosedBridge{}, failClosedTestMetrics(t))
			if err == nil {
				t.Fatalf("expected NewClient to reject a signer missing %q", dropped)
			}
			if !strings.Contains(err.Error(), dropped) {
				t.Fatalf("expected error to name the missing capability %q, got %v", dropped, err)
			}
			if client != nil {
				t.Fatal("expected no client when a required capability is missing")
			}
		})
	}
}
