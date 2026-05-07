package thornode

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gitlab.com/thorchain/thornode/v3/common"
	sdk "gitlab.com/thorchain/thornode/v3/common/cosmos"
)

func TestGetSwapQuoteAddsExtendedQueryForUTXOSources(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("extended"); got != "true" {
			t.Fatalf("expected extended=true query param, got %q", got)
		}
		if got := r.URL.Query().Get("destination"); got != "destination" {
			t.Fatalf("expected destination query param, got %q", got)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	originalURL := thornodeURL
	originalClient := httpClient
	defer func() {
		thornodeURL = originalURL
		httpClient = originalClient
	}()

	thornodeURL = server.URL
	httpClient = server.Client()

	if _, err := GetSwapQuote(common.BTCAsset, common.XMRAsset, sdk.NewUint(1), common.Address("destination"), true); err != nil {
		t.Fatalf("expected swap quote request to succeed, got %v", err)
	}
}

func TestGetSwapQuoteOmitsExtendedQueryForNonUTXOSources(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("extended"); got != "" {
			t.Fatalf("expected extended query param to be omitted, got %q", got)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	originalURL := thornodeURL
	originalClient := httpClient
	defer func() {
		thornodeURL = originalURL
		httpClient = originalClient
	}()

	thornodeURL = server.URL
	httpClient = server.Client()

	if _, err := GetSwapQuote(common.ETHAsset, common.XMRAsset, sdk.NewUint(1), common.Address("destination"), false); err != nil {
		t.Fatalf("expected swap quote request to succeed, got %v", err)
	}
}

func TestGetRetriesRetryableTimeout(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			time.Sleep(50 * time.Millisecond)
		}
		_, _ = w.Write([]byte(`{"ok":"true"}`))
	}))
	defer server.Close()

	originalClient := httpClient
	originalAttempts := thornodeHTTPRetryAttempts
	originalDelay := thornodeHTTPRetryDelay
	originalSleep := thornodeHTTPSleep
	defer func() {
		httpClient = originalClient
		thornodeHTTPRetryAttempts = originalAttempts
		thornodeHTTPRetryDelay = originalDelay
		thornodeHTTPSleep = originalSleep
	}()

	client := server.Client()
	client.Timeout = 10 * time.Millisecond
	httpClient = client
	thornodeHTTPRetryAttempts = 2
	thornodeHTTPRetryDelay = 0
	thornodeHTTPSleep = func(time.Duration) {}

	var got map[string]string
	if err := Get(server.URL, &got); err != nil {
		t.Fatalf("expected retryable timeout to recover, got %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
	if got["ok"] != "true" {
		t.Fatalf("expected decoded response after retry, got %#v", got)
	}
}

func TestGetDoesNotRetryPermanentHTTPError(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		http.Error(w, `{"code":2,"message":"permanent"}`, http.StatusInternalServerError)
	}))
	defer server.Close()

	originalClient := httpClient
	originalAttempts := thornodeHTTPRetryAttempts
	originalDelay := thornodeHTTPRetryDelay
	originalSleep := thornodeHTTPSleep
	defer func() {
		httpClient = originalClient
		thornodeHTTPRetryAttempts = originalAttempts
		thornodeHTTPRetryDelay = originalDelay
		thornodeHTTPSleep = originalSleep
	}()

	httpClient = server.Client()
	thornodeHTTPRetryAttempts = 2
	thornodeHTTPRetryDelay = 0
	thornodeHTTPSleep = func(time.Duration) {}

	var got map[string]string
	if err := Get(server.URL, &got); err == nil {
		t.Fatal("expected permanent HTTP 500 response to fail")
	}
	if attempts != 1 {
		t.Fatalf("expected permanent HTTP error to avoid retries, got %d attempts", attempts)
	}
}
