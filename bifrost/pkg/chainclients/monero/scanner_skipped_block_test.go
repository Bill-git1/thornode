package monero

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/monero/sidecar"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/config"
)

// TestVaultRegistrationFailurePropagatesError validates the bug where a sidecar
// vault registration failure (HTTP 500) is silently swallowed, causing the
// bifrost to skip the block entirely.
//
// Scenario:
//  1. Sidecar has vault registered under frost key
//  2. Bifrost calls ensureVaultRegistered with eddsa key (bug)
//  3. Sidecar returns 500 ("already registered with different mapping")
//  4. refreshAsgardLookup returns error
//  5. OLD CODE: fetchTransfersByHeight returns (nil, nil) → bifrost advances
//     past block → output at that block is PERMANENTLY LOST
//  6. NEW CODE: fetchTransfersByHeight returns (nil, ErrUnavailableBlock) →
//     bifrost retries the block
//
// This test validates that the registration failure IS an error (step 4),
// proving that the old (nil, nil) return was wrong.
func TestVaultRegistrationFailurePropagatesError(t *testing.T) {
	t.Parallel()

	eddsaKey := testVaultPubKey(t, "eddsa-reg-fail-test")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/monero/vaults/register" {
			// Sidecar rejects: this vault_pub_key was already registered
			// with a different group_key (the frost key's group_key).
			http.Error(
				w,
				`{"error":"vault is already registered with a different mapping"}`,
				http.StatusInternalServerError,
			)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	signer, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	require.NoError(t, err)

	// ensureVaultRegistered with eddsa key → sidecar returns 500.
	// This error MUST propagate — not be swallowed.
	_, regErr := ensureVaultRegistered(
		testLogger(),
		signer,
		common.XMRChain,
		eddsaKey,
		nil,
		"test",
		0,
	)

	// The registration must fail — this is the error that refreshAsgardLookup
	// propagates. If this were silently swallowed, fetchTransfersByHeight would
	// return (nil, nil) and the bifrost would advance past the block.
	require.Error(t, regErr, "registration failure must propagate as an error")
	assert.Contains(t, regErr.Error(), "register",
		"error should indicate registration failure")
}

// TestRegistrationFailureSkipsOnlyBadVault verifies that a stale or malformed
// XMR vault registration entry does not wedge scanner refresh for every other
// vault. The failed vault is skipped; valid vaults remain usable.
//
// This test simulates the exact refreshAsgardLookup logic: iterate vaults,
// call ensureVaultRegistered, and verify the fail-open path.
func TestRegistrationFailureSkipsOnlyBadVault(t *testing.T) {
	t.Parallel()

	frostKey := testVaultPubKey(t, "frost-refresh-fail")
	badKey := testVaultPubKey(t, "bad-refresh-fail")

	var registeredKeys []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/monero/vaults/register" {
			var req sidecar.RegisterVaultReq
			_ = json.NewDecoder(r.Body).Decode(&req)
			registeredKeys = append(registeredKeys, req.VaultPubKey)

			// First vault succeeds, second vault fails.
			if req.VaultPubKey == badKey.String() {
				http.Error(
					w,
					`{"error":"already registered with different mapping"}`,
					http.StatusInternalServerError,
				)
				return
			}
			_ = json.NewEncoder(w).Encode(sidecar.RegisterVaultResp{
				Registered:         true,
				ExpectedNextHeight: uint64Ptr(0),
			})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	signer, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	require.NoError(t, err)

	// Simulate the refreshAsgardLookup logic: register each vault.
	type testVault struct {
		pubKey common.PubKey
	}
	vaults := []testVault{
		{pubKey: frostKey},
		{pubKey: badKey},
	}

	var registrationErrors int
	for _, v := range vaults {
		_, regErr := ensureVaultRegistered(
			testLogger(),
			signer,
			common.XMRChain,
			v.pubKey,
			nil,
			"test",
			0,
		)
		if regErr != nil {
			registrationErrors++
			continue
		}
	}

	// Verify: first vault registered successfully, second vault failed.
	assert.Equal(t, 2, len(registeredKeys))
	assert.Equal(t, frostKey.String(), registeredKeys[0])
	assert.Equal(t, badKey.String(), registeredKeys[1])
	assert.Equal(t, 1, registrationErrors)
}
