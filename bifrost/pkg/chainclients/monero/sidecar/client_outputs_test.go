package sidecar

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gitlab.com/thorchain/thornode/v3/config"
)

func TestClientOutputs(t *testing.T) {
	t.Parallel()

	expectedPath := "/v1/monero/outputs/thorpub1vault%2Fwith%3Fchars"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != expectedPath {
			http.Error(w, "unexpected path: "+r.URL.EscapedPath(), http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(Outputs{
			Outputs: []Output{
				{
					TxHash:          "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
					OutputIndex:     3,
					Amount:          12345,
					Height:          77,
					KeyImage:        "bbbb",
					Spent:           false,
					SubaddressMajor: 2,
					SubaddressMinor: 0,
				},
			},
		})
	}))
	defer server.Close()

	signer, err := NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}

	outputs, err := signer.Outputs("thorpub1vault/with?chars")
	if err != nil {
		t.Fatalf("GetOutputs failed: %v", err)
	}
	if len(outputs) != 1 {
		t.Fatalf("expected one output, got %d", len(outputs))
	}
	if outputs[0].OutputIndex != 3 || outputs[0].Amount != 12345 || outputs[0].Height != 77 {
		t.Fatalf("unexpected output payload: %+v", outputs[0])
	}
}

func TestClientOutputsHTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	signer, err := NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}

	_, err = signer.Outputs("thorpub1vault")
	if err == nil {
		t.Fatal("expected GetOutputs error")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("unexpected error: %v", err)
	}
}
