package monero

import (
	"errors"
	"testing"

	"github.com/blang/semver"

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

func TestSignTxSkipsKeysignFailurePostWhenBlameNodesEmpty(t *testing.T) {
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

	if bridge.calls != 0 {
		t.Fatalf("expected PostKeysignFailure not to be called, got %d calls", bridge.calls)
	}
}
