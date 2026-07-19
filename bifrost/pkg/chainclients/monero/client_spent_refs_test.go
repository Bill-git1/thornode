package monero

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/monero/sidecar"
	stypes "gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/config"
)

func testLogger() zerolog.Logger {
	return zerolog.Nop()
}

// Test 2: buildOutboundObservation includes SpentOutputRefs in the TxInItem.
func TestBuildOutboundObservationIncludesSpentRefs(t *testing.T) {
	t.Parallel()

	vaultPubKey := testVaultPubKey(t, "test-vault-spent-refs-obs")
	vaultAddr, err := vaultPubKey.GetAddress(common.XMRChain)
	require.NoError(t, err)

	c := &Client{
		chain: common.XMRChain,
	}
	c.vaultAddressMap = map[string]common.Address{
		vaultPubKey.String(): vaultAddr,
	}
	c.vaultAddrToKey = map[string]string{}

	tx := stypes.TxOutItem{
		Chain:            common.XMRChain,
		ToAddress:        testOutboundXMRDestination(),
		VaultPubKeyFrost: vaultPubKey,
		Coins:            common.NewCoins(common.NewCoin(common.XMRAsset, cosmos.NewUint(100))),
		Memo:             "OUT:inhash",
	}

	spentRefs := []sidecar.SpentRef{
		{TxHash: "aaa111", OutputIndex: 0, KeyImage: "ki_aaa"},
		{TxHash: "bbb222", OutputIndex: 1, KeyImage: "ki_bbb"},
		{TxHash: "ccc333", OutputIndex: 2, KeyImage: "ki_ccc"},
	}

	obs := c.buildOutboundObservation(tx, "txhash123", 5000000, spentRefs)
	require.NotNil(t, obs, "observation should not be nil")
	require.Equal(t, 3, len(obs.SpentOutputRefs), "observation should carry all spent refs")

	assert.Equal(t, "aaa111", obs.SpentOutputRefs[0].TxHash)
	assert.Equal(t, uint32(0), obs.SpentOutputRefs[0].OutputIndex)
	assert.Equal(t, "ki_aaa", obs.SpentOutputRefs[0].KeyImage)
	assert.Equal(t, "txhash123", obs.SpentOutputRefs[0].SpendTxHash)
	assert.Equal(t, "bbb222", obs.SpentOutputRefs[1].TxHash)
	assert.Equal(t, uint32(1), obs.SpentOutputRefs[1].OutputIndex)
	assert.Equal(t, "ki_bbb", obs.SpentOutputRefs[1].KeyImage)
	assert.Equal(t, "ccc333", obs.SpentOutputRefs[2].TxHash)
	assert.Equal(t, uint32(2), obs.SpentOutputRefs[2].OutputIndex)
	assert.Equal(t, "ki_ccc", obs.SpentOutputRefs[2].KeyImage)

	assert.Equal(t, vaultPubKey, obs.ObservedVaultPubKey, "observation vault pubkey should be the frost key")
}

// Test 2b: buildOutboundObservation with empty spent refs produces no refs.
func TestBuildOutboundObservationNoSpentRefs(t *testing.T) {
	t.Parallel()

	vaultPubKey := testVaultPubKey(t, "test-vault-no-spent-refs")
	vaultAddr, err := vaultPubKey.GetAddress(common.XMRChain)
	require.NoError(t, err)

	c := &Client{
		chain: common.XMRChain,
	}
	c.vaultAddressMap = map[string]common.Address{
		vaultPubKey.String(): vaultAddr,
	}
	c.vaultAddrToKey = map[string]string{}

	tx := stypes.TxOutItem{
		Chain:            common.XMRChain,
		ToAddress:        testOutboundXMRDestination(),
		VaultPubKeyFrost: vaultPubKey,
		Coins:            common.NewCoins(common.NewCoin(common.XMRAsset, cosmos.NewUint(100))),
		Memo:             "OUT:inhash",
	}

	obs := c.buildOutboundObservation(tx, "txhash456", 5000000, nil)
	require.NotNil(t, obs)
	assert.Empty(t, obs.SpentOutputRefs)
}

// Test 3b: ApplySpentOutputRefs calls MarkOutputsSpent on the sidecar with
// the correct vault pubkey and refs from the observed tx.
func TestApplySpentOutputRefsSidecarCall(t *testing.T) {
	t.Parallel()

	vaultPubKey := testVaultPubKey(t, "test-vault-apply-spent")
	vaultAddr, err := vaultPubKey.GetAddress(common.XMRChain)
	require.NoError(t, err)
	spendTxHash := testXMRHash("apply-spent-output-refs")

	var mu sync.Mutex
	var capturedVault string
	var capturedTxHash string
	var capturedRefs []sidecar.SpentRef
	var capturedSkipVerification bool
	var capturedFromAddress string
	var capturedToAddress string
	var capturedAmount uint64
	var capturedFee uint64
	var capturedMemo string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/monero/outputs/mark-spent" {
			var req sidecar.MarkSpentReq
			_ = json.NewDecoder(r.Body).Decode(&req)
			mu.Lock()
			capturedVault = req.VaultPubKey
			capturedTxHash = req.TxHash
			capturedRefs = req.SpentRefs
			capturedSkipVerification = req.SkipVerification
			capturedFromAddress = req.FromAddress
			capturedToAddress = req.ToAddress
			capturedAmount = req.Amount
			capturedFee = req.Fee
			capturedMemo = req.Memo
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(sidecar.MarkSpentResp{Marked: len(req.SpentRefs)})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	signer, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	require.NoError(t, err)

	c := &Client{
		chain:   common.XMRChain,
		sidecar: signer,
		logger:  testLogger(),
	}

	refs := []common.OutputRef{
		*testXMROutputRef("spent-a", 0, spendTxHash),
		*testXMROutputRef("spent-b", 3, spendTxHash),
	}
	obsTx := common.NewObservedTx(
		common.Tx{
			ID:          common.TxID(spendTxHash),
			Chain:       common.XMRChain,
			FromAddress: vaultAddr,
			ToAddress:   testOutboundXMRDestination(),
			Coins:       common.NewCoins(common.NewCoin(common.XMRAsset, cosmos.NewUint(500_000))),
			Gas:         common.Gas{common.NewCoin(common.XMRAsset, cosmos.NewUint(10_000))},
			Memo:        "MIGRATE:100",
		},
		100,
		vaultPubKey,
		100,
	)
	obsTx.SpentOutputRefs = refs

	c.ApplySpentOutputRefs(obsTx)

	mu.Lock()
	defer mu.Unlock()

	// The sidecar receives atomic-unit outbound metadata. This is separate from
	// the consensus ObservedTx shape and is what lets the scanner later
	// attribute the verified XMR spend back to the THORChain outbound.
	expectedAmount, err := thorchainAmountToAtomic(obsTx.Tx.Coins.GetCoin(common.XMRAsset).Amount)
	require.NoError(t, err)
	expectedFee, err := thorchainAmountToAtomic(obsTx.Tx.Gas.ToCoins().GetCoin(common.XMRAsset).Amount)
	require.NoError(t, err)

	assert.Equal(t, vaultPubKey.String(), capturedVault, "sidecar should receive the vault pubkey from ObservedPubKey")
	assert.Equal(t, spendTxHash, capturedTxHash)
	// Peer-imported refs must still be verified by the sidecar. This explicitly
	// guards against accidentally switching the import path to trusted refs.
	assert.False(t, capturedSkipVerification)
	assert.Equal(t, vaultAddr.String(), capturedFromAddress)
	assert.Equal(t, obsTx.Tx.ToAddress.String(), capturedToAddress)
	assert.Equal(t, expectedAmount, capturedAmount)
	assert.Equal(t, expectedFee, capturedFee)
	assert.Equal(t, "MIGRATE:100", capturedMemo)
	require.Equal(t, 2, len(capturedRefs))
	assert.Equal(t, refs[0].TxHash, capturedRefs[0].TxHash)
	assert.Equal(t, uint32(0), capturedRefs[0].OutputIndex)
	assert.Equal(t, refs[0].KeyImage, capturedRefs[0].KeyImage)
	assert.Equal(t, spendTxHash, capturedRefs[0].SpendTxHash)
	assert.Equal(t, refs[1].TxHash, capturedRefs[1].TxHash)
	assert.Equal(t, uint32(3), capturedRefs[1].OutputIndex)
	assert.Equal(t, refs[1].KeyImage, capturedRefs[1].KeyImage)
	assert.Equal(t, spendTxHash, capturedRefs[1].SpendTxHash)
}

// TestValidateXMRSpentRefsForSidecarRejectsDuplicateKeyImage protects the
// sidecar binding invariant that one Monero key image can only spend one output.
func TestValidateXMRSpentRefsForSidecarRejectsDuplicateKeyImage(t *testing.T) {
	t.Parallel()

	vaultPubKey := testVaultPubKey(t, "test-vault-duplicate-key-image")
	vaultAddr, err := vaultPubKey.GetAddress(common.XMRChain)
	require.NoError(t, err)
	spendTxHash := testXMRHash("duplicate-key-image")

	obsTx := common.NewObservedTx(
		common.Tx{
			ID:          common.TxID(spendTxHash),
			Chain:       common.XMRChain,
			FromAddress: vaultAddr,
			ToAddress:   testOutboundXMRDestination(),
			Coins:       common.NewCoins(common.NewCoin(common.XMRAsset, cosmos.NewUint(50))),
			Gas:         common.Gas{common.NewCoin(common.XMRAsset, cosmos.NewUint(1))},
		},
		100,
		vaultPubKey,
		100,
	)
	first := *testXMROutputRef("duplicate-key-image-a", 0, spendTxHash)
	second := *testXMROutputRef("duplicate-key-image-b", 1, spendTxHash)
	second.KeyImage = strings.ToUpper(first.KeyImage)
	obsTx.SpentOutputRefs = []common.OutputRef{first, second}

	err = validateXMRSpentRefsForSidecar(obsTx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate xmr spent ref key image")
}

// TestApplySpentOutputRefsRetriesTransientNotFound covers the race where peer
// refs arrive before the Monero daemon/sidecar can query the spend tx. The
// import should retry boundedly instead of relying only on committed catchup.
func TestApplySpentOutputRefsRetriesTransientNotFound(t *testing.T) {
	t.Parallel()

	vaultPubKey := testVaultPubKey(t, "test-vault-apply-spent-retry")
	vaultAddr, err := vaultPubKey.GetAddress(common.XMRChain)
	require.NoError(t, err)
	spendTxHash := testXMRHash("apply-spent-output-refs-retry")

	var mu sync.Mutex
	calls := 0
	done := make(chan struct{})
	var closeDone sync.Once

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/monero/outputs/mark-spent" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		mu.Lock()
		calls++
		call := calls
		mu.Unlock()

		if call == 1 {
			// First attempt simulates the sidecar get_transactions race window.
			// The second attempt proves ApplySpentOutputRefs queues a retry.
			http.Error(w, "tx not mined yet", http.StatusNotFound)
			return
		}

		var req sidecar.MarkSpentReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(sidecar.MarkSpentResp{Marked: len(req.SpentRefs)})
		closeDone.Do(func() { close(done) })
	}))
	defer server.Close()

	signer, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	require.NoError(t, err)

	c := &Client{
		chain:                     common.XMRChain,
		sidecar:                   signer,
		logger:                    testLogger(),
		wg:                        &sync.WaitGroup{},
		stopchan:                  make(chan struct{}),
		spentRefsRetryInterval:    10 * time.Millisecond,
		spentRefsRetryMaxAttempts: 3,
	}
	defer func() {
		close(c.stopchan)
		c.wg.Wait()
	}()

	refs := []common.OutputRef{
		*testXMROutputRef("spent-retry-a", 0, spendTxHash),
	}
	obsTx := common.NewObservedTx(
		common.Tx{
			ID:          common.TxID(spendTxHash),
			Chain:       common.XMRChain,
			FromAddress: vaultAddr,
			ToAddress:   testOutboundXMRDestination(),
			Coins:       common.NewCoins(common.NewCoin(common.XMRAsset, cosmos.NewUint(50))),
			Gas:         common.Gas{common.NewCoin(common.XMRAsset, cosmos.NewUint(1))},
		},
		100,
		vaultPubKey,
		100,
	)
	obsTx.SpentOutputRefs = refs

	c.ApplySpentOutputRefs(obsTx)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for mark-spent retry")
	}

	mu.Lock()
	defer mu.Unlock()
	assert.GreaterOrEqual(t, calls, 2)
}

// TestApplySpentOutputRefsOnceRetriesPartialNotFound verifies an HTTP-200
// response with a partially missing ref keeps the bounded retry window open.
func TestApplySpentOutputRefsOnceRetriesPartialNotFound(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(sidecar.MarkSpentResp{Marked: 1, NotFound: 1})
	}))
	defer server.Close()

	signer, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	require.NoError(t, err)
	c := &Client{
		chain:   common.XMRChain,
		sidecar: signer,
		logger:  testLogger(),
	}

	obsTx := testXMRSpentRefsObservedTx(t, "test-vault-partial-not-found", "partial-not-found")
	obsTx.SpentOutputRefs = append(obsTx.SpentOutputRefs,
		*testXMROutputRef("partial-not-found-second", 1, obsTx.Tx.ID.String()))
	applied, retry := c.applySpentOutputRefsOnce(obsTx)
	assert.False(t, applied)
	assert.True(t, retry)
}

// TestApplySpentOutputRefsRetriesTransientSidecarUnavailable covers the peer
// import case where the local sidecar is reachable but not ready yet. This must
// not permanently consume the observer bucket's spent-ref import opportunity.
func TestApplySpentOutputRefsRetriesTransientSidecarUnavailable(t *testing.T) {
	t.Parallel()

	vaultPubKey := testVaultPubKey(t, "test-vault-apply-spent-retry-503")
	vaultAddr, err := vaultPubKey.GetAddress(common.XMRChain)
	require.NoError(t, err)
	spendTxHash := testXMRHash("apply-spent-output-refs-retry-503")

	var mu sync.Mutex
	calls := 0
	done := make(chan struct{})
	var closeDone sync.Once

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/monero/outputs/mark-spent" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		mu.Lock()
		calls++
		call := calls
		mu.Unlock()

		if call == 1 {
			// A 503 is a sidecar readiness/health failure, not a ref validation
			// failure. The import should retry just like the get_transactions
			// 404 race.
			http.Error(w, "sidecar not ready", http.StatusServiceUnavailable)
			return
		}

		var req sidecar.MarkSpentReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(sidecar.MarkSpentResp{Marked: len(req.SpentRefs)})
		closeDone.Do(func() { close(done) })
	}))
	defer server.Close()

	signer, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	require.NoError(t, err)

	c := &Client{
		chain:                     common.XMRChain,
		sidecar:                   signer,
		logger:                    testLogger(),
		wg:                        &sync.WaitGroup{},
		stopchan:                  make(chan struct{}),
		spentRefsRetryInterval:    10 * time.Millisecond,
		spentRefsRetryMaxAttempts: 3,
	}
	defer func() {
		close(c.stopchan)
		c.wg.Wait()
	}()

	obsTx := common.NewObservedTx(
		common.Tx{
			ID:          common.TxID(spendTxHash),
			Chain:       common.XMRChain,
			FromAddress: vaultAddr,
			ToAddress:   testOutboundXMRDestination(),
			Coins:       common.NewCoins(common.NewCoin(common.XMRAsset, cosmos.NewUint(50))),
			Gas:         common.Gas{common.NewCoin(common.XMRAsset, cosmos.NewUint(1))},
		},
		100,
		vaultPubKey,
		100,
	)
	obsTx.SpentOutputRefs = []common.OutputRef{*testXMROutputRef("spent-retry-503-a", 0, spendTxHash)}

	c.ApplySpentOutputRefs(obsTx)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for mark-spent retry after 503")
	}

	mu.Lock()
	defer mu.Unlock()
	assert.GreaterOrEqual(t, calls, 2)
}

// TestApplySpentOutputRefsDoesNotRetryTerminalSidecarError verifies that the
// broader transient retry policy does not retry sidecar validation failures.
// Retrying 400-class ref errors would only add load and hide bad inputs.
func TestApplySpentOutputRefsDoesNotRetryTerminalSidecarError(t *testing.T) {
	t.Parallel()

	vaultPubKey := testVaultPubKey(t, "test-vault-apply-spent-no-retry-400")
	vaultAddr, err := vaultPubKey.GetAddress(common.XMRChain)
	require.NoError(t, err)
	spendTxHash := testXMRHash("apply-spent-output-refs-no-retry-400")

	var mu sync.Mutex
	calls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/monero/outputs/mark-spent" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		mu.Lock()
		calls++
		mu.Unlock()
		http.Error(w, "invalid spent refs", http.StatusBadRequest)
	}))
	defer server.Close()

	signer, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	require.NoError(t, err)

	c := &Client{
		chain:    common.XMRChain,
		sidecar:  signer,
		logger:   testLogger(),
		wg:       &sync.WaitGroup{},
		stopchan: make(chan struct{}),
	}
	defer close(c.stopchan)

	obsTx := common.NewObservedTx(
		common.Tx{
			ID:          common.TxID(spendTxHash),
			Chain:       common.XMRChain,
			FromAddress: vaultAddr,
			ToAddress:   testOutboundXMRDestination(),
			Coins:       common.NewCoins(common.NewCoin(common.XMRAsset, cosmos.NewUint(50))),
			Gas:         common.Gas{common.NewCoin(common.XMRAsset, cosmos.NewUint(1))},
		},
		100,
		vaultPubKey,
		100,
	)
	obsTx.SpentOutputRefs = []common.OutputRef{*testXMROutputRef("spent-no-retry-400-a", 0, spendTxHash)}

	c.ApplySpentOutputRefs(obsTx)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, calls)
}

// TestApplySpentOutputRefsFromTxInItemUsesObservationHeight verifies the
// observer-to-sidecar conversion preserves the observed tx hash/ref set when a
// TxInItem is reconstructed into an ObservedTx for spent-ref import.
func TestApplySpentOutputRefsFromTxInItemUsesObservationHeight(t *testing.T) {
	t.Parallel()

	vaultPubKey := testVaultPubKey(t, "test-vault-apply-spent-txin")
	vaultAddr, err := vaultPubKey.GetAddress(common.XMRChain)
	require.NoError(t, err)
	spendTxHash := testXMRHash("apply-spent-output-refs-txin")

	var mu sync.Mutex
	var capturedTxHash string
	var capturedRefs []sidecar.SpentRef

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/monero/outputs/mark-spent" {
			var req sidecar.MarkSpentReq
			_ = json.NewDecoder(r.Body).Decode(&req)
			mu.Lock()
			capturedTxHash = req.TxHash
			capturedRefs = req.SpentRefs
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(sidecar.MarkSpentResp{Marked: len(req.SpentRefs)})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	signer, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	require.NoError(t, err)

	c := &Client{
		chain:   common.XMRChain,
		sidecar: signer,
		logger:  testLogger(),
	}
	refs := []*common.OutputRef{
		testXMROutputRef("spent-txin-a", 0, spendTxHash),
	}

	c.applySpentOutputRefsFromTxInItem(&stypes.TxInItem{
		BlockHeight:         123,
		Tx:                  spendTxHash,
		Sender:              vaultAddr.String(),
		To:                  testOutboundXMRDestination().String(),
		Coins:               common.NewCoins(common.NewCoin(common.XMRAsset, cosmos.NewUint(50))),
		Gas:                 common.Gas{common.NewCoin(common.XMRAsset, cosmos.NewUint(1))},
		ObservedVaultPubKey: vaultPubKey,
		SpentOutputRefs:     refs,
	})

	mu.Lock()
	defer mu.Unlock()

	assert.Equal(t, spendTxHash, capturedTxHash)
	require.Equal(t, 1, len(capturedRefs))
	assert.Equal(t, refs[0].TxHash, capturedRefs[0].TxHash)
	assert.Equal(t, spendTxHash, capturedRefs[0].SpendTxHash)
}

// Test 2c: txVaultPubKey only accepts the frost vault key for XMR routing.
func TestTxVaultPubKeyPreference(t *testing.T) {
	t.Parallel()

	frost := testVaultPubKey(t, "frost-key")
	eddsa := testVaultPubKey(t, "eddsa-key")
	ecdsa := testVaultPubKey(t, "ecdsa-key")

	tx := stypes.TxOutItem{
		VaultPubKey:      ecdsa,
		VaultPubKeyEddsa: eddsa,
		VaultPubKeyFrost: frost,
	}
	assert.Equal(t, frost, txVaultPubKey(tx), "should prefer frost key")

	txNoFrost := stypes.TxOutItem{
		VaultPubKey:      ecdsa,
		VaultPubKeyEddsa: eddsa,
	}
	assert.True(t, txVaultPubKey(txNoFrost).IsEmpty(), "should reject eddsa-only xmr vault routing")

	txOnlyEcdsa := stypes.TxOutItem{
		VaultPubKey: ecdsa,
	}
	assert.True(t, txVaultPubKey(txOnlyEcdsa).IsEmpty(), "should be empty when only ecdsa set")
}

// testXMRSpentRefsObservedTx builds the minimal valid outbound shape for
// outcome-callback tests, keeping each case focused on retry semantics.
func testXMRSpentRefsObservedTx(t *testing.T, vaultLabel, txLabel string) common.ObservedTx {
	t.Helper()

	vaultPubKey := testVaultPubKey(t, vaultLabel)
	vaultAddr, err := vaultPubKey.GetAddress(common.XMRChain)
	require.NoError(t, err)
	spendTxHash := testXMRHash(txLabel)

	obsTx := common.NewObservedTx(
		common.Tx{
			ID:          common.TxID(spendTxHash),
			Chain:       common.XMRChain,
			FromAddress: vaultAddr,
			ToAddress:   testOutboundXMRDestination(),
			Coins:       common.NewCoins(common.NewCoin(common.XMRAsset, cosmos.NewUint(50))),
			Gas:         common.Gas{common.NewCoin(common.XMRAsset, cosmos.NewUint(1))},
		},
		100,
		vaultPubKey,
		100,
	)
	obsTx.SpentOutputRefs = []common.OutputRef{
		*testXMROutputRef(txLabel+"-ref", 0, spendTxHash),
	}
	return obsTx
}

// TestApplySpentOutputRefsWithOutcomeReportsRetrySuccess verifies the outcome
// callback fires true when a transient sidecar failure succeeds on a bounded
// retry, so the observer latch can close even when the first attempt raced the
// daemon.
func TestApplySpentOutputRefsWithOutcomeReportsRetrySuccess(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		call := calls
		mu.Unlock()
		if call == 1 {
			http.Error(w, "tx not mined yet", http.StatusNotFound)
			return
		}
		var req sidecar.MarkSpentReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(sidecar.MarkSpentResp{Marked: len(req.SpentRefs)})
	}))
	defer server.Close()

	signer, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	require.NoError(t, err)

	c := &Client{
		chain:                     common.XMRChain,
		sidecar:                   signer,
		logger:                    testLogger(),
		wg:                        &sync.WaitGroup{},
		stopchan:                  make(chan struct{}),
		spentRefsRetryInterval:    10 * time.Millisecond,
		spentRefsRetryMaxAttempts: 3,
	}
	defer func() {
		close(c.stopchan)
		c.wg.Wait()
	}()

	outcome := make(chan bool, 1)
	c.ApplySpentOutputRefsWithOutcome(
		testXMRSpentRefsObservedTx(t, "test-vault-outcome-retry", "outcome-retry-success"),
		func(applied bool) { outcome <- applied },
	)

	select {
	case applied := <-outcome:
		assert.True(t, applied, "outcome should report the retry success")
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for import outcome")
	}
}

// TestApplySpentOutputRefsWithOutcomeReportsTerminalFailure verifies the
// outcome callback fires false, without retries, when the sidecar rejects the
// refs terminally, so the observer can release its conflict-map entry.
func TestApplySpentOutputRefsWithOutcomeReportsTerminalFailure(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		http.Error(w, "spent ref key image conflicts with tracked output", http.StatusBadRequest)
	}))
	defer server.Close()

	signer, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	require.NoError(t, err)

	c := &Client{
		chain:                     common.XMRChain,
		sidecar:                   signer,
		logger:                    testLogger(),
		wg:                        &sync.WaitGroup{},
		stopchan:                  make(chan struct{}),
		spentRefsRetryInterval:    10 * time.Millisecond,
		spentRefsRetryMaxAttempts: 3,
	}
	defer func() {
		close(c.stopchan)
		c.wg.Wait()
	}()

	outcome := make(chan bool, 1)
	c.ApplySpentOutputRefsWithOutcome(
		testXMRSpentRefsObservedTx(t, "test-vault-outcome-terminal", "outcome-terminal"),
		func(applied bool) { outcome <- applied },
	)

	select {
	case applied := <-outcome:
		assert.False(t, applied, "terminal sidecar failure must report a failed outcome")
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for import outcome")
	}
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, calls, "terminal failure must not be retried")
}

// TestApplySpentOutputRefsWithOutcomeReportsExhaustion verifies the outcome
// callback fires false after the bounded retry window is used up.
func TestApplySpentOutputRefsWithOutcomeReportsExhaustion(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		http.Error(w, "tx not mined yet", http.StatusNotFound)
	}))
	defer server.Close()

	signer, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	require.NoError(t, err)

	c := &Client{
		chain:                     common.XMRChain,
		sidecar:                   signer,
		logger:                    testLogger(),
		wg:                        &sync.WaitGroup{},
		stopchan:                  make(chan struct{}),
		spentRefsRetryInterval:    5 * time.Millisecond,
		spentRefsRetryMaxAttempts: 2,
	}
	defer func() {
		close(c.stopchan)
		c.wg.Wait()
	}()

	outcome := make(chan bool, 1)
	c.ApplySpentOutputRefsWithOutcome(
		testXMRSpentRefsObservedTx(t, "test-vault-outcome-exhausted", "outcome-exhausted"),
		func(applied bool) { outcome <- applied },
	)

	select {
	case applied := <-outcome:
		assert.False(t, applied, "exhausted retries must report a failed outcome")
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for import outcome")
	}
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 3, calls, "expected the first attempt plus both retries")
}
