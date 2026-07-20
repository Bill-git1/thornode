package monero

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blang/semver"

	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/messages"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/monero/sidecar"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/shared/signercache"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient"
	stypes "gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
	bifrosttss "gitlab.com/thorchain/thornode/v3/bifrost/tss"
	gotssblame "gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/blame"
	gotsscommon "gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/common"
	tssp "gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/tss"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/config"
	ttypes "gitlab.com/thorchain/thornode/v3/x/thorchain/types"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/storage"
)

type keysignFailureBridge struct {
	thorclient.ThorchainBridge

	calls      int
	lastBlame  ttypes.Blame
	lastHeight int64
	lastMemo   string
	lastCoins  common.Coins
	lastPubKey common.PubKey
	retErr     error
	vaults     map[string]ttypes.Vault
}

func (b *keysignFailureBridge) GetVault(pubkey string) (ttypes.Vault, error) {
	if b.vaults == nil {
		return ttypes.Vault{}, errors.New("vault lookup is unavailable in test bridge")
	}
	vault, ok := b.vaults[pubkey]
	if !ok {
		return ttypes.Vault{}, errors.New("vault not found")
	}
	return vault, nil
}

func (b *keysignFailureBridge) GetAsgards() (ttypes.Vaults, error) {
	if b.vaults == nil {
		return nil, errors.New("asgards lookup is unavailable in test bridge")
	}
	out := make(ttypes.Vaults, 0, len(b.vaults))
	for _, vault := range b.vaults {
		out = append(out, vault)
	}
	return out, nil
}

// GetKeysignParty exposes vault membership to keysign-failure tests through
// the legacy bridge method.
func (b *keysignFailureBridge) GetKeysignParty(vaultPubKey common.PubKey, _ int64) (common.PubKeys, error) {
	vault, err := b.GetVault(vaultPubKey.String())
	if err != nil {
		return nil, err
	}
	members := vault.GetMembership()
	if len(members) > 0 {
		return members, nil
	}
	for _, member := range vault.Membership {
		if member != "" {
			members = append(members, common.PubKey(member))
		}
	}
	return members, nil
}

// GetKeysignPolicy returns an all-members policy so tests can focus on failure
// reporting without introducing signer filtering as another variable.
func (b *keysignFailureBridge) GetKeysignPolicy(vaultPubKey common.PubKey, blockHeight int64) (thorclient.KeysignPolicy, error) {
	signers, err := b.GetKeysignParty(vaultPubKey, blockHeight)
	if err != nil {
		return thorclient.KeysignPolicy{}, err
	}
	return thorclient.KeysignPolicy{PubKey: vaultPubKey, Signers: signers, RequiredSigners: len(signers), ActiveSignerCount: len(signers)}, nil
}

func (b *keysignFailureBridge) GetThorchainVersion() (semver.Version, error) {
	return semver.MustParse("1.134.0"), nil
}

func (b *keysignFailureBridge) GetMimir(key string) (int64, error) {
	return -1, nil
}

func (b *keysignFailureBridge) PostKeysignFailure(
	blame ttypes.Blame,
	height int64,
	memo string,
	coins common.Coins,
	pubkey common.PubKey,
) (common.TxID, error) {
	b.calls++
	b.lastBlame = blame
	b.lastHeight = height
	b.lastMemo = memo
	b.lastCoins = coins
	b.lastPubKey = pubkey
	if b.retErr != nil {
		return "", b.retErr
	}
	return common.TxID("XMR_TEST_KEYSIGN_FAILURE_TX"), nil
}

func newTestSignerCacheManager(t *testing.T) *signercache.CacheManager {
	t.Helper()

	memStorage := storage.NewMemStorage()
	db, err := leveldb.Open(memStorage, nil)
	if err != nil {
		t.Fatalf("open in-memory leveldb: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	signerCache, err := signercache.NewSignerCacheManager(db)
	if err != nil {
		t.Fatalf("new signer cache manager: %v", err)
	}
	return signerCache
}

type mockFrostTssServer struct {
	tssp.Server
	lastReq tssp.FrostKeySignRequest
	resp    tssp.FrostKeySignResponse
	err     error
}

func (m *mockFrostTssServer) FrostKeySign(req tssp.FrostKeySignRequest, _ tssp.FrostSidecarClient) (tssp.FrostKeySignResponse, error) {
	m.lastReq = req
	return m.resp, m.err
}

type blockingFrostTssServer struct {
	tssp.Server

	calls         int32
	firstEntered  chan struct{}
	secondEntered chan struct{}
	releaseFirst  chan struct{}
	onceFirst     sync.Once
	onceSecond    sync.Once
}

func newBlockingFrostTssServer() *blockingFrostTssServer {
	return &blockingFrostTssServer{
		firstEntered:  make(chan struct{}),
		secondEntered: make(chan struct{}),
		releaseFirst:  make(chan struct{}),
	}
}

func (m *blockingFrostTssServer) FrostKeySign(req tssp.FrostKeySignRequest, _ tssp.FrostSidecarClient) (tssp.FrostKeySignResponse, error) {
	call := atomic.AddInt32(&m.calls, 1)
	switch call {
	case 1:
		m.onceFirst.Do(func() { close(m.firstEntered) })
		<-m.releaseFirst
		return tssp.FrostKeySignResponse{}, errors.New("first frost execution stopped")
	default:
		m.onceSecond.Do(func() { close(m.secondEntered) })
		return tssp.FrostKeySignResponse{Status: gotsscommon.NA}, nil
	}
}

func newFrostSignTxCleanupFixture(t *testing.T, tssServer *mockFrostTssServer, clearStatus int) (*Client, stypes.TxOutItem, *httptest.Server, *int32) {
	t.Helper()

	vaultPubKey, vaultAddress := testVaultPubKeyAndAddress(t)
	groupKey, err := groupKeyHexFromVaultPubKey(vaultPubKey)
	if err != nil {
		t.Fatalf("group key: %v", err)
	}
	clearHits := int32(0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/v1/monero/keys" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"vaults": []sidecar.VaultKey{{VaultPubKey: vaultPubKey.String(), GroupKey: groupKey, HasFrostKeys: true, Signable: true}},
			})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/v1/monero/vaults/register" {
			var req sidecar.RegisterVaultReq
			if decodeErr := json.NewDecoder(r.Body).Decode(&req); decodeErr != nil {
				http.Error(w, "invalid register request", http.StatusBadRequest)
				return
			}
			nextHeight := uint64(req.VaultCreationHeight)
			_ = json.NewEncoder(w).Encode(sidecar.RegisterVaultResp{
				Registered:         true,
				ExpectedNextHeight: &nextHeight,
			})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/v1/monero/sessions/clear" {
			atomic.AddInt32(&clearHits, 1)
			if clearStatus != 0 {
				http.Error(w, "clear sessions failed", clearStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"cleared_sessions": 1,
				"status":           "ok",
			})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/monero/balance/") {
			_ = json.NewEncoder(w).Encode(sidecar.Balance{
				Balance:          10 * moneroAtomicToThorchain,
				UnlockedBalance:  10 * moneroAtomicToThorchain,
				SpendableBalance: uint64Ptr(10 * moneroAtomicToThorchain),
			})
			return
		}
		http.NotFound(w, r)
	}))

	frostSigner, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	if err != nil {
		server.Close()
		t.Fatalf("new frost signer: %v", err)
	}

	bridge := &keysignFailureBridge{
		vaults: map[string]ttypes.Vault{
			vaultPubKey.String(): {
				PubKey:      vaultPubKey,
				PubKeyFrost: vaultPubKey,
				Status:      ttypes.VaultStatus_ActiveVault,
				StatusSince: 12000,
				Membership:  []string{"member-a"},
			},
		},
	}
	client := &Client{
		chain:              common.XMRChain,
		localNodePubKey:    common.PubKey("member-a"),
		sidecar:            frostSigner,
		tssServer:          tssServer,
		signerCacheManager: newTestSignerCacheManager(t),
		thorchainBridge:    bridge,
		vaultAddressMap: map[string]common.Address{
			vaultPubKey.String(): vaultAddress,
		},
	}
	tx := stypes.TxOutItem{
		Chain:            common.XMRChain,
		VaultPubKey:      vaultPubKey,
		VaultPubKeyEddsa: vaultPubKey,
		VaultPubKeyFrost: vaultPubKey,
		ToAddress:        testOutboundXMRDestination(),
		Coins: common.Coins{
			common.NewCoin(common.XMRAsset, cosmos.NewUint(1)),
		},
		Memo: "OUT:0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF",
	}
	return client, tx, server, &clearHits
}

// TestSignTxClearsSidecarSessionsOnFrostKeySignError documents the immediate
// cleanup path for abandoned XMR signing rounds. Without this call, the signer
// can keep the selected output reserved and Bifrost retries degrade into
// "insufficient xmr balance" even though the output is unlocked.
func TestSignTxClearsSidecarSessionsOnFrostKeySignError(t *testing.T) {
	tssErr := errors.New("frost execution stopped")
	tssServer := &mockFrostTssServer{err: tssErr}
	c, tx, server, clearHits := newFrostSignTxCleanupFixture(t, tssServer, 0)
	defer server.Close()

	_, _, _, signErr := c.signTx(tx, 12345)
	if !errors.Is(signErr, tssErr) {
		t.Fatalf("expected frost execution error, got %T %v", signErr, signErr)
	}
	if got := atomic.LoadInt32(clearHits); got != 1 {
		t.Fatalf("expected clear sessions hit once, got %d", got)
	}
}

func TestSignTxSerializesFrostSessionsBeforeClearingSidecar(t *testing.T) {
	tssServer := newBlockingFrostTssServer()
	c, tx, server, clearHits := newFrostSignTxCleanupFixture(t, (*mockFrostTssServer)(nil), 0)
	defer server.Close()
	c.tssServer = tssServer

	firstDone := make(chan error, 1)
	go func() {
		_, _, _, err := c.signTx(tx, 12345)
		firstDone <- err
	}()

	select {
	case <-tssServer.firstEntered:
	case <-time.After(time.Second):
		t.Fatal("first FROST keysign did not start")
	}

	secondDone := make(chan error, 1)
	go func() {
		secondTx := tx
		secondTx.SigningID = "second-xmr-signing"
		_, _, _, err := c.signTx(secondTx, 12345)
		secondDone <- err
	}()

	select {
	case <-tssServer.secondEntered:
		t.Fatal("second FROST keysign entered while first session was still in flight")
	case <-time.After(50 * time.Millisecond):
	}

	close(tssServer.releaseFirst)

	if err := <-firstDone; err == nil || !strings.Contains(err.Error(), "first frost execution stopped") {
		t.Fatalf("expected first signing failure, got %v", err)
	}
	if got := atomic.LoadInt32(clearHits); got != 1 {
		t.Fatalf("expected first failure to clear sidecar sessions once, got %d", got)
	}

	select {
	case <-tssServer.secondEntered:
	case <-time.After(time.Second):
		t.Fatal("second FROST keysign did not start after first cleanup")
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("expected second signing to stand down cleanly, got %v", err)
	}
}

// TestSignTxClearsSidecarSessionsOnNonSuccessWithBlame keeps signer-blame
// behavior intact while still releasing any XMR inputs reserved by the failed
// sidecar signing session.
func TestSignTxClearsSidecarSessionsOnNonSuccessWithBlame(t *testing.T) {
	tssServer := &mockFrostTssServer{
		resp: tssp.FrostKeySignResponse{
			Status: gotsscommon.Fail,
			Blame: gotssblame.Blame{
				FailReason: "xmr keysign failed",
				BlameNodes: []gotssblame.Node{
					{Pubkey: "thorpub1blamenode"},
				},
			},
		},
	}
	c, tx, server, clearHits := newFrostSignTxCleanupFixture(t, tssServer, http.StatusInternalServerError)
	defer server.Close()

	_, _, _, signErr := c.signTx(tx, 12345)
	var keysignErr bifrosttss.KeysignError
	if !errors.As(signErr, &keysignErr) {
		t.Fatalf("expected keysign error despite clear failure, got %T %v", signErr, signErr)
	}
	if got := atomic.LoadInt32(clearHits); got != 1 {
		t.Fatalf("expected clear sessions hit once, got %d", got)
	}
}

// TestSignTxDoesNotClearSidecarSessionsOnFrostStandDown covers the selected
// stand-down response. No sidecar session is abandoned in this case, so there
// is no reservation to roll back.
func TestSignTxDoesNotClearSidecarSessionsOnFrostStandDown(t *testing.T) {
	tssServer := &mockFrostTssServer{
		resp: tssp.FrostKeySignResponse{Status: gotsscommon.NA},
	}
	c, tx, server, clearHits := newFrostSignTxCleanupFixture(t, tssServer, 0)
	defer server.Close()

	_, _, _, signErr := c.signTx(tx, 12345)
	if signErr != nil {
		t.Fatalf("expected stand-down without error, got %v", signErr)
	}
	if got := atomic.LoadInt32(clearHits); got != 0 {
		t.Fatalf("expected no clear sessions hit, got %d", got)
	}
}

// TestSignTxClearsSidecarSessionsOnEmptyMsgID ensures malformed successful
// FROST responses also release sidecar session state before returning.
func TestSignTxClearsSidecarSessionsOnEmptyMsgID(t *testing.T) {
	tssServer := &mockFrostTssServer{
		resp: tssp.FrostKeySignResponse{Status: gotsscommon.Success},
	}
	c, tx, server, clearHits := newFrostSignTxCleanupFixture(t, tssServer, 0)
	defer server.Close()

	_, _, _, signErr := c.signTx(tx, 12345)
	if signErr == nil || !strings.Contains(signErr.Error(), "empty msg_id") {
		t.Fatalf("expected empty msg_id error, got %T %v", signErr, signErr)
	}
	var keysignErr bifrosttss.KeysignError
	if !errors.As(signErr, &keysignErr) || !keysignErr.IsTerminalRound() {
		t.Fatalf("empty post-share result must retain terminal FROST semantics: %v", signErr)
	}
	if got := atomic.LoadInt32(clearHits); got != 1 {
		t.Fatalf("expected clear sessions hit once, got %d", got)
	}
}

func TestSignTxInvalidEventualityRetainsTerminalShare(t *testing.T) {
	tssServer := &mockFrostTssServer{
		resp: tssp.FrostKeySignResponse{
			Status: gotsscommon.Success,
			MsgID:  strings.Repeat("a", 64),
		},
	}
	c, tx, server, clearHits := newFrostSignTxCleanupFixture(t, tssServer, 0)
	defer server.Close()

	_, _, _, signErr := c.signTx(tx, 12345)
	if signErr == nil || !strings.Contains(signErr.Error(), "invalid FROST eventuality") {
		t.Fatalf("expected invalid eventuality error, got %T %v", signErr, signErr)
	}
	var keysignErr bifrosttss.KeysignError
	if !errors.As(signErr, &keysignErr) || !keysignErr.IsTerminalRound() {
		t.Fatalf("invalid post-share result must retain terminal FROST semantics: %v", signErr)
	}
	if got := atomic.LoadInt32(clearHits); got != 1 {
		t.Fatalf("expected clear sessions hit once, got %d", got)
	}
}

func TestSignTxPassesXMRDustThresholdInAtomicUnits(t *testing.T) {
	t.Parallel()

	vaultPubKey, vaultAddress := testVaultPubKeyAndAddress(t)
	bridge := &keysignFailureBridge{
		vaults: map[string]ttypes.Vault{
			vaultPubKey.String(): {
				PubKey:      vaultPubKey,
				PubKeyFrost: vaultPubKey,
				Status:      ttypes.VaultStatus_ActiveVault,
				StatusSince: 12000,
				Membership:  []string{"member-a"},
			},
		},
	}
	tssServer := &mockFrostTssServer{err: errors.New("stop after request capture")}
	balanceServer := newFrostBalanceServer(t, map[string]sidecar.Balance{
		vaultPubKey.String(): {
			Balance:         10 * moneroAtomicToThorchain,
			UnlockedBalance: 10 * moneroAtomicToThorchain,
		},
	})
	defer balanceServer.Close()

	frostSigner, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, balanceServer.URL)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}

	c := &Client{
		chain:              common.XMRChain,
		localNodePubKey:    common.PubKey("member-a"),
		sidecar:            frostSigner,
		tssServer:          tssServer,
		signerCacheManager: newTestSignerCacheManager(t),
		thorchainBridge:    bridge,
		vaultAddressMap: map[string]common.Address{
			vaultPubKey.String(): vaultAddress,
		},
	}
	tx := stypes.TxOutItem{
		Chain:            common.XMRChain,
		VaultPubKey:      vaultPubKey,
		VaultPubKeyEddsa: vaultPubKey,
		VaultPubKeyFrost: vaultPubKey,
		ToAddress:        testOutboundXMRDestination(),
		Coins: common.Coins{
			common.NewCoin(common.XMRAsset, cosmos.NewUint(1)),
		},
		Memo: "OUT:0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF",
	}

	_, _, _, signErr := c.signTx(tx, 12345)
	if signErr == nil {
		t.Fatalf("expected signTx to stop after request capture")
	}

	expected, err := thorchainAmountToAtomic(common.XMRChain.DustThreshold())
	if err != nil {
		t.Fatalf("convert dust threshold: %v", err)
	}
	if expected == common.XMRChain.DustThreshold().Uint64() {
		t.Fatalf("test is not exercising THORChain-to-atomic unit conversion")
	}
	if tssServer.lastReq.DustThreshold != expected {
		t.Fatalf("expected atomic dust threshold %d, got %d", expected, tssServer.lastReq.DustThreshold)
	}
}

// TestSignTxPassesXMRMaxGasInAtomicUnits proves Bifrost sends the scheduled
// MaxGas to the FROST layer in Monero atomic units, matching the signer API.
func TestSignTxPassesXMRMaxGasInAtomicUnits(t *testing.T) {
	t.Parallel()

	vaultPubKey, vaultAddress := testVaultPubKeyAndAddress(t)
	bridge := &keysignFailureBridge{
		vaults: map[string]ttypes.Vault{
			vaultPubKey.String(): {
				PubKey:      vaultPubKey,
				PubKeyFrost: vaultPubKey,
				Status:      ttypes.VaultStatus_ActiveVault,
				StatusSince: 12000,
				Membership:  []string{"member-a"},
			},
		},
	}
	tssServer := &mockFrostTssServer{err: errors.New("stop after request capture")}
	balanceServer := newFrostBalanceServer(t, map[string]sidecar.Balance{
		vaultPubKey.String(): {
			Balance:         10 * moneroAtomicToThorchain,
			UnlockedBalance: 10 * moneroAtomicToThorchain,
		},
	})
	defer balanceServer.Close()

	frostSigner, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, balanceServer.URL)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}

	c := &Client{
		chain:              common.XMRChain,
		localNodePubKey:    common.PubKey("member-a"),
		sidecar:            frostSigner,
		tssServer:          tssServer,
		signerCacheManager: newTestSignerCacheManager(t),
		thorchainBridge:    bridge,
		vaultAddressMap: map[string]common.Address{
			vaultPubKey.String(): vaultAddress,
		},
	}
	tx := stypes.TxOutItem{
		Chain:            common.XMRChain,
		VaultPubKey:      vaultPubKey,
		VaultPubKeyEddsa: vaultPubKey,
		VaultPubKeyFrost: vaultPubKey,
		ToAddress:        testOutboundXMRDestination(),
		Coins: common.Coins{
			common.NewCoin(common.XMRAsset, cosmos.NewUint(1)),
		},
		MaxGas: common.Gas{
			common.NewCoin(common.XMRAsset, cosmos.NewUint(2)),
		},
		Memo: "OUT:0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF",
	}

	_, _, _, signErr := c.signTx(tx, 12345)
	if signErr == nil {
		t.Fatalf("expected signTx to stop after request capture")
	}

	expected, err := thorchainAmountToAtomic(cosmos.NewUint(2))
	if err != nil {
		t.Fatalf("convert max gas: %v", err)
	}
	if tssServer.lastReq.MaxGasAtomic != expected {
		t.Fatalf("expected MaxGasAtomic=%d, got %d", expected, tssServer.lastReq.MaxGasAtomic)
	}
}

// TestEnforceXMRMaxGasRejectsOverCapFees documents the local compatibility
// guard for signers that return an over-cap fee instead of rejecting it first.
func TestEnforceXMRMaxGasRejectsOverCapFees(t *testing.T) {
	t.Parallel()

	// This helper-level check protects 3.19 Bifrost from accepting an over-cap
	// signed result even when the connected signer does not enforce MaxGas
	// during transaction construction.
	if err := enforceXMRMaxGas(100, 100); err != nil {
		t.Fatalf("expected fee equal to cap to pass: %v", err)
	}
	err := enforceXMRMaxGas(101, 100)
	if err == nil {
		t.Fatal("expected over-cap xmr fee to fail")
	}
	if !strings.Contains(err.Error(), tssp.FrostFailReasonXMRMaxGasExceeded) {
		t.Fatalf("expected stable max gas error prefix, got %v", err)
	}
	var keysignErr bifrosttss.KeysignError
	if !errors.As(err, &keysignErr) || !keysignErr.IsTerminalRound() {
		t.Fatalf("over-cap result must preserve terminal FROST share semantics: %v", err)
	}
}

func TestSignTxPostsKeysignFailureWhenSignFails(t *testing.T) {
	t.Parallel()

	vaultPubKey, vaultAddress := testVaultPubKeyAndAddress(t)
	bridge := &keysignFailureBridge{
		vaults: map[string]ttypes.Vault{
			vaultPubKey.String(): {
				PubKey:      vaultPubKey,
				PubKeyFrost: vaultPubKey,
				Status:      ttypes.VaultStatus_ActiveVault,
				StatusSince: 12000,
				Membership:  []string{"member-a"},
			},
		},
	}

	tssServer := &mockFrostTssServer{
		resp: tssp.FrostKeySignResponse{
			Status: gotsscommon.Fail,
			Blame: gotssblame.Blame{
				FailReason: "xmr keysign failed",
				BlameNodes: []gotssblame.Node{
					{Pubkey: "thorpub1blamenode"},
				},
			},
		},
	}
	balanceServer := newFrostBalanceServer(t, map[string]sidecar.Balance{
		vaultPubKey.String(): {
			Balance:         10 * moneroAtomicToThorchain,
			UnlockedBalance: 10 * moneroAtomicToThorchain,
		},
	})
	defer balanceServer.Close()
	frostSigner, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, balanceServer.URL)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}

	c := &Client{
		chain:              common.XMRChain,
		localNodePubKey:    common.PubKey("member-a"),
		sidecar:            frostSigner,
		tssServer:          tssServer,
		signerCacheManager: newTestSignerCacheManager(t),
		thorchainBridge:    bridge,
		vaultAddressMap: map[string]common.Address{
			vaultPubKey.String(): vaultAddress,
		},
	}

	tx := stypes.TxOutItem{
		Chain:            common.XMRChain,
		VaultPubKey:      vaultPubKey,
		VaultPubKeyEddsa: vaultPubKey,
		VaultPubKeyFrost: vaultPubKey,
		ToAddress:        testOutboundXMRDestination(),
		Coins: common.Coins{
			common.NewCoin(common.XMRAsset, cosmos.NewUint(1)),
		},
		Memo: "OUT:0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF",
	}

	_, _, _, signErr := c.SignTx(tx, 12345)
	if signErr == nil {
		t.Fatalf("expected SignTx error")
	}
	var keysignErr bifrosttss.KeysignError
	if !errors.As(signErr, &keysignErr) {
		t.Fatalf("expected keysign error, got: %T %v", signErr, signErr)
	}
	if len(keysignErr.Blame.BlameNodes) != 1 {
		t.Fatalf("expected one blame node on keysign error, got %d", len(keysignErr.Blame.BlameNodes))
	}
	if bridge.calls != 1 {
		t.Fatalf("expected PostKeysignFailure to be called once, got %d", bridge.calls)
	}
}

func TestSignTxKeepsTerminalRoundWithoutPostingEmptyBlame(t *testing.T) {
	t.Parallel()

	vaultPubKey, vaultAddress := testVaultPubKeyAndAddress(t)
	bridge := &keysignFailureBridge{
		vaults: map[string]ttypes.Vault{
			vaultPubKey.String(): {
				PubKey:      vaultPubKey,
				PubKeyFrost: vaultPubKey,
				Status:      ttypes.VaultStatus_ActiveVault,
				StatusSince: 900,
				Membership:  []string{"member-a"},
			},
		},
	}

	tssServer := &mockFrostTssServer{
		resp: tssp.FrostKeySignResponse{
			Status: gotsscommon.Fail,
			Blame: gotssblame.Blame{
				FailReason: "xmr keysign failed",
				Round:      messages.FROSTKEYSIGNSHARE,
			},
		},
	}
	balanceServer := newFrostBalanceServer(t, map[string]sidecar.Balance{
		vaultPubKey.String(): {
			Balance:         10 * moneroAtomicToThorchain,
			UnlockedBalance: 10 * moneroAtomicToThorchain,
		},
	})
	defer balanceServer.Close()
	frostSigner, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, balanceServer.URL)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}

	c := &Client{
		chain:              common.XMRChain,
		localNodePubKey:    common.PubKey("member-a"),
		sidecar:            frostSigner,
		tssServer:          tssServer,
		signerCacheManager: newTestSignerCacheManager(t),
		thorchainBridge:    bridge,
		vaultAddressMap: map[string]common.Address{
			vaultPubKey.String(): vaultAddress,
		},
	}

	tx := stypes.TxOutItem{
		Chain:            common.XMRChain,
		VaultPubKey:      vaultPubKey,
		VaultPubKeyEddsa: vaultPubKey,
		VaultPubKeyFrost: vaultPubKey,
		ToAddress:        testOutboundXMRDestination(),
		Coins: common.Coins{
			common.NewCoin(common.XMRAsset, cosmos.NewUint(1)),
		},
		Memo: "OUT:1111111111111111111111111111111111111111111111111111111111111111",
	}

	_, _, _, signErr := c.SignTx(tx, 999)
	if signErr == nil {
		t.Fatalf("expected SignTx error")
	}
	var keysignErr bifrosttss.KeysignError
	if !errors.As(signErr, &keysignErr) {
		t.Fatalf("expected round-only failure to remain a keysign error, got %T: %v", signErr, signErr)
	}
	if !keysignErr.IsTerminalRound() {
		t.Fatalf("expected FROST share failure to remain terminal")
	}

	if bridge.calls != 0 {
		t.Fatalf("expected PostKeysignFailure not to be called, got %d calls", bridge.calls)
	}
}
