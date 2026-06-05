package core

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	atypes "gitlab.com/thorchain/thornode/v3/api/types"
	"gitlab.com/thorchain/thornode/v3/common"
	openapi "gitlab.com/thorchain/thornode/v3/openapi/gen"
	. "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/types"
)

func TestXMRFrostChurnReadinessPassesWhenClusterIsReady(t *testing.T) {
	deps := readyXMRFrostChurnDeps(t)

	result := checkXMRFrostChurnReadiness(&OpConfig{}, deps)
	if !result.ready {
		t.Fatalf("expected readiness gate to pass, got reason=%s err=%v", result.reason, result.err)
	}
	if result.readyValidators != 2 {
		t.Fatalf("expected two ready validators, got %d", result.readyValidators)
	}
	if result.moneroHeight != 42 {
		t.Fatalf("expected monero height 42, got %d", result.moneroHeight)
	}
}

func TestXMRFrostChurnReadinessAllowsPreChurnStandbyValidators(t *testing.T) {
	deps := readyXMRFrostChurnDeps(t)
	deps.nodes = func() ([]openapi.Node, error) {
		return []openapi.Node{
			{NodeAddress: "tthor1dog", Status: atypes.NodeStatus_Active.String(), PeerId: "dog-peer"},
			{NodeAddress: "tthor1cat", Status: atypes.NodeStatus_Standby.String(), PeerId: "cat-peer"},
		}, nil
	}

	result := checkXMRFrostChurnReadiness(&OpConfig{}, deps)
	if !result.ready {
		t.Fatalf("expected standby validator with peer id to pass pre-churn readiness, got reason=%s pending=%v", result.reason, result.pendingValidators)
	}
}

func TestXMRFrostChurnReadinessRequiresValidatorPeerIDs(t *testing.T) {
	deps := readyXMRFrostChurnDeps(t)
	deps.nodes = func() ([]openapi.Node, error) {
		return []openapi.Node{
			{NodeAddress: "tthor1dog", Status: atypes.NodeStatus_Active.String(), PeerId: "dog-peer"},
			{NodeAddress: "tthor1cat", Status: atypes.NodeStatus_Ready.String()},
		}, nil
	}

	result := checkXMRFrostChurnReadiness(&OpConfig{}, deps)
	if result.ready {
		t.Fatal("expected readiness gate to wait for missing peer id")
	}
	if result.reason != "validator nodes not ready" {
		t.Fatalf("unexpected reason: %s", result.reason)
	}
	if len(result.pendingValidators) != 1 || !strings.Contains(result.pendingValidators[0], "no_peer_id") {
		t.Fatalf("expected missing peer id in pending validators, got %v", result.pendingValidators)
	}
}

func TestXMRFrostChurnReadinessRequiresHTTPReadiness(t *testing.T) {
	deps := readyXMRFrostChurnDeps(t)
	deps.httpGet = func(endpoint string) (*http.Response, error) {
		if endpoint == "http://frost-cat/health" {
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Body:       io.NopCloser(strings.NewReader("warming up")),
			}, nil
		}
		return okHTTPResponse(), nil
	}

	result := checkXMRFrostChurnReadiness(&OpConfig{}, deps)
	if result.ready {
		t.Fatal("expected readiness gate to wait for unhealthy frost endpoint")
	}
	if result.reason != "xmr frost endpoint not ready" {
		t.Fatalf("unexpected reason: %s", result.reason)
	}
	if result.endpointName != "cat-xmr-frost" {
		t.Fatalf("expected cat frost endpoint, got %s", result.endpointName)
	}
}

func TestXMRFrostChurnReadinessRequiresPositiveMoneroHeight(t *testing.T) {
	deps := readyXMRFrostChurnDeps(t)
	deps.moneroHeight = func(string) (int64, error) {
		return 0, nil
	}

	result := checkXMRFrostChurnReadiness(&OpConfig{}, deps)
	if result.ready {
		t.Fatal("expected readiness gate to wait for monero height")
	}
	if result.reason != "monero daemon height is zero" {
		t.Fatalf("unexpected reason: %s", result.reason)
	}
}

func TestXMRFrostChurnReadinessSurfacesMoneroHeightErrors(t *testing.T) {
	deps := readyXMRFrostChurnDeps(t)
	deps.moneroHeight = func(string) (int64, error) {
		return 0, errors.New("daemon unavailable")
	}

	result := checkXMRFrostChurnReadiness(&OpConfig{}, deps)
	if result.ready {
		t.Fatal("expected readiness gate to wait for monero height")
	}
	if result.reason != "monero daemon height unavailable" {
		t.Fatalf("unexpected reason: %s", result.reason)
	}
	if result.err == nil || !strings.Contains(result.err.Error(), "daemon unavailable") {
		t.Fatalf("expected monero error, got %v", result.err)
	}
}

func readyXMRFrostChurnDeps(t *testing.T) xmrFrostChurnReadinessDeps {
	t.Helper()

	return xmrFrostChurnReadinessDeps{
		nodeAddresses: func(*OpConfig) ([]common.Address, error) {
			return []common.Address{"tthor1dog", "tthor1cat"}, nil
		},
		nodes: func() ([]openapi.Node, error) {
			return []openapi.Node{
				{NodeAddress: "tthor1dog", Status: atypes.NodeStatus_Active.String(), PeerId: "dog-peer"},
				{NodeAddress: "tthor1cat", Status: atypes.NodeStatus_Ready.String(), PeerId: "cat-peer"},
			}, nil
		},
		httpGet: func(string) (*http.Response, error) {
			return okHTTPResponse(), nil
		},
		moneroHeight: func(string) (int64, error) {
			return 42, nil
		},
		bifrostEndpoints: []readinessEndpoint{
			{name: "dog-bifrost", url: "http://bifrost-dog/p2pid"},
			{name: "cat-bifrost", url: "http://bifrost-cat/p2pid"},
		},
		frostEndpoints: []readinessEndpoint{
			{name: "dog-xmr-frost", url: "http://frost-dog/health"},
			{name: "cat-xmr-frost", url: "http://frost-cat/health"},
		},
		moneroRPC: "http://monero/json_rpc",
	}
}

func okHTTPResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("ok")),
	}
}
