package monero

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

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

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/monero/outputs/mark-spent" {
			var req sidecar.MarkSpentReq
			_ = json.NewDecoder(r.Body).Decode(&req)
			mu.Lock()
			capturedVault = req.VaultPubKey
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
			Coins:       common.NewCoins(common.NewCoin(common.XMRAsset, cosmos.NewUint(50))),
			Gas:         common.Gas{common.NewCoin(common.XMRAsset, cosmos.NewUint(1))},
		},
		100,
		vaultPubKey,
		100,
	)
	obsTx.SpentOutputRefs = refs

	c.ApplySpentOutputRefs(obsTx)

	mu.Lock()
	defer mu.Unlock()

	assert.Equal(t, vaultPubKey.String(), capturedVault, "sidecar should receive the vault pubkey from ObservedPubKey")
	assert.Equal(t, spendTxHash, capturedTxHash)
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
