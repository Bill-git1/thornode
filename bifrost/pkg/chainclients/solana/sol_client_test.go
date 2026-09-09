package solana

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	cKeys "github.com/cosmos/cosmos-sdk/crypto/keyring"
	cosmosed25519 "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	bech32 "github.com/cosmos/cosmos-sdk/types/bech32/legacybech32" // nolint SA1019 deprecated
	"github.com/mr-tron/base58"
	"github.com/stretchr/testify/require"
	"gitlab.com/thorchain/thornode/v3/bifrost/blockscanner"
	"gitlab.com/thorchain/thornode/v3/bifrost/metrics"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/shared/signercache"
	"gitlab.com/thorchain/thornode/v3/bifrost/pubkeymanager"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient"
	stypes "gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
	"gitlab.com/thorchain/thornode/v3/bifrost/tss"
	"gitlab.com/thorchain/thornode/v3/cmd"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/common/crypto/ed25519"
	"gitlab.com/thorchain/thornode/v3/config"
	types2 "gitlab.com/thorchain/thornode/v3/x/thorchain/types"
	. "gopkg.in/check.v1"
)

func TestPackage(t *testing.T) { TestingT(t) }

type SolTestSuite struct {
	thordir  string
	thorKeys *thorclient.Keys
	bridge   thorclient.ThorchainBridge
	m        *metrics.Metrics
	server   *httptest.Server
}

var _ = Suite(&SolTestSuite{})

var m *metrics.Metrics

func GetMetricForTest(c *C) *metrics.Metrics {
	if m == nil {
		var err error
		m, err = metrics.NewMetrics(config.BifrostMetricsConfiguration{
			Enabled:      false,
			ListenPort:   9000,
			ReadTimeout:  time.Second,
			WriteTimeout: time.Second,
			Chains:       common.Chains{common.SOLChain},
		})
		c.Assert(m, NotNil)
		c.Assert(err, IsNil)
	}
	return m
}

func (s *SolTestSuite) SetUpSuite(c *C) {
	s.m = GetMetricForTest(c)
	c.Assert(s.m, NotNil)

	ccfg := cosmos.GetConfig()
	ccfg.SetBech32PrefixForAccount("tthor", "tthorpub")
	ccfg.SetBech32PrefixForValidator("tthorv", "tthorvpub")
	ccfg.SetBech32PrefixForConsensusNode("tthorc", "tthorcpub")

	cfg := config.BifrostClientConfiguration{
		ChainID:         "thorchain",
		SignerName:      "bob-secp256k1",
		SignerPasswd:    "password",
		ChainHomeFolder: s.thordir,
	}

	registry := codectypes.NewInterfaceRegistry()
	cdc := codec.NewProtoCodec(registry)
	cryptocodec.RegisterInterfaces(cdc.InterfaceRegistry())
	kb := cKeys.NewInMemory(cdc, func(options *cKeys.Options) {
		options.SupportedAlgos = append(options.SupportedAlgos, ed25519.Ed25519)
	})
	edName := ed25519.SignerNameEDDSA(cfg.SignerName)
	r, mnemonic, err := kb.NewMnemonic(edName, cKeys.English, "", cfg.SignerPasswd, ed25519.Ed25519)
	c.Assert(err, IsNil)

	_, err = kb.NewAccount(cfg.SignerName, mnemonic, cfg.SignerPasswd, cmd.THORChainHDPath, hd.Secp256k1)
	c.Assert(err, IsNil)

	record, err := kb.Key(edName)
	c.Assert(err, IsNil)

	localRecord := record.GetLocal()
	c.Assert(localRecord, NotNil)

	privKey := localRecord.PrivKey
	c.Assert(privKey, NotNil)

	cPrivKey := new(cosmosed25519.PrivKey)
	err = cdc.UnpackAny(privKey, &cPrivKey)
	c.Assert(err, IsNil)

	cPubKey := new(cosmosed25519.PubKey)
	err = cdc.UnpackAny(r.PubKey, &cPubKey)
	c.Assert(err, IsNil)

	c.Assert(bytes.Equal(cPrivKey.PubKey().Bytes(), cPubKey.Bytes()), Equals, true)

	pubKey, err := bech32.MarshalPubKey(bech32.AccPK, cPubKey) // nolint:staticcheck
	c.Assert(err, IsNil)

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		// This handler is hit CONCURRENTLY: NewSOLClient starts the block scanner
		// in a goroutine that polls the SOL RPC while a test drives the sign path.
		// gocheck's *C is not safe for concurrent use, so never call c.Assert/
		// c.Fatal here — use local errors and ignore write failures.
		switch req.RequestURI {
		case thorclient.ChainVersionEndpoint:
			_, _ = rw.Write([]byte(`{"current":"` + types2.GetCurrentVersion().String() + `"}`))
		case thorclient.PubKeysEndpoint:
			rw.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(rw, `{"asgard":[{"pub_key_eddsa":%q}]}`, pubKey)
		default:
			body, _ := io.ReadAll(req.Body)
			var rpcRequest struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			_ = json.Unmarshal(body, &rpcRequest)
			rw.Header().Set("Content-Type", "application/json")

			// SOL consensus-blockhash + participation-gate methods (getSlot,
			// getBlocks, getBlockTime, getBlock, isBlockhashValid).
			if res, ok := consensusBlockhashRPCResponse(rpcRequest.Method, rpcRequest.Params); ok {
				_, _ = fmt.Fprintf(rw, `{"jsonrpc":"2.0","id":1,"result":%s}`, res)
				return
			}
			switch rpcRequest.Method {
			case "block": // CometBFT block for the real bridge's GetBlockTimestamp
				_, _ = rw.Write([]byte(cometBFTBlockResponse(rpcRequest.ID)))
			case "getLatestBlockhash":
				_, _ = rw.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"context":{"slot":279644744},"value":{"blockhash":"2Q1p2F8BDaLRkiJURvxTXAFHffC1uVsTXFkWsE7eawHE","lastValidBlockHeight":258824587}}}`))
			case "getTransaction":
				if bytes.Contains(body, []byte("somesig")) {
					_, _ = rw.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"slot":12345,"meta":{},"transaction":{}}}`))
				} else {
					_, _ = rw.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
				}
			case "getSignatureStatuses":
				if bytes.Contains(body, []byte("pendingsig")) {
					_, _ = rw.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"value":[{"slot":12345,"confirmations":1,"err":null,"confirmationStatus":"processed"}]}}`))
				} else {
					_, _ = rw.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"value":[null]}}`))
				}
			}
		}
	}))

	s.server = server

	cfg.ChainHost = server.Listener.Addr().String()
	// Point the bridge's CometBFT client at the same mock so GetBlockTimestamp
	// (used by the consensus-blockhash sign path) resolves against the "block"
	// handler above.
	cfg.ChainRPC = server.Listener.Addr().String()

	ns := strconv.Itoa(time.Now().Nanosecond())
	c.Assert(os.Setenv("NET", "stagenet"), IsNil)

	s.thordir = filepath.Join(os.TempDir(), ns, ".thorcli")

	s.thorKeys = thorclient.NewKeysWithKeybase(kb, cfg.SignerName, cfg.SignerPasswd)

	s.bridge, err = thorclient.NewThorchainBridge(cfg, s.m, s.thorKeys)
	c.Assert(err, IsNil)
}

// TestGetGasEstimateForSolvency verifies SOL keeps the legacy 3x MaxGasLimit
// breathing room using the scanner's lamport fee rate, converted to THORChain units.
func (s *SolTestSuite) TestGetGasEstimateForSolvency(c *C) {
	client := &SOLClient{
		cfg: config.BifrostChainConfiguration{
			ChainID: common.SOLChain,
			BlockScanner: config.BifrostBlockScannerConfiguration{
				MaxGasLimit: 5_000,
			},
		},
		solScanner: &SOLScanner{},
	}
	client.solScanner.lastFeeRate.Store(2_000)

	got := client.GetGasEstimateForSolvency()
	c.Assert(got.Equal(cosmos.NewUint(3_000_000)), Equals, true)
}

func (s *SolTestSuite) TestCreateAndSerializeTx(c *C) {
	pubkeyMgr, err := pubkeymanager.NewPubKeyManager(s.bridge, s.m)
	c.Assert(err, IsNil)
	poolMgr := thorclient.NewPoolMgr(s.bridge)
	e, err := NewSOLClient(s.thorKeys,
		config.BifrostChainConfiguration{
			RPCHost: "http://" + s.server.Listener.Addr().String(),
			BlockScanner: config.BifrostBlockScannerConfiguration{
				StartBlockHeight:   1, // avoids querying thorchain for block height
				HTTPRequestTimeout: time.Second,
				MaxGasLimit:        15000,
			},
		}, nil, s.bridge, s.m, pubkeyMgr, poolMgr)
	c.Assert(err, IsNil)
	c.Assert(e, NotNil)

	// Create TX
	txOutItem := stypes.TxOutItem{
		Chain:            common.SOLChain,
		ToAddress:        "D9A6eE2pZ6oSiGb8BPkag4gvAeEhHvc3eEYPBoLSMshG",
		VaultPubKey:      "tthorpub1addwnpepqfshsq2y6ejy2ysxmq4gj8n8mzuzyulk9wh4n946jv5w2vpwdn2yuyp6sp4",
		VaultPubKeyEddsa: "tthorpub1zcjduepq829qzzhllcktltnfdudyupw5qjtyyjzcmv9wx2jvpah4d6nxtl2s7vwca8",
		Coins: common.Coins{
			common.NewCoin(common.SOLAsset, cosmos.NewUint(11047250)),
		},
		MaxGas: common.Gas{
			common.NewCoin(common.SOLAsset, cosmos.NewUint(15000)),
		},
		GasRate: 1,
		Memo:    "OUT:4D91ADAFA69765E7805B5FF2F3A0BA1DBE69E37A1CFCD20C48B99C528AA3EE87",
	}
	tx, err := e.CreateTx(txOutItem, txOutItem.GetMemo(), "asdfasdf")
	c.Assert(err, IsNil)
	c.Assert(tx, NotNil)

	// Serialize TX
	rawTx, err := tx.Serialize()
	c.Assert(err, IsNil)
	c.Assert(len(rawTx), Equals, 293)
}

func (s *SolTestSuite) TestSignSOLTxDoesNotReSignWhenCachedTxIsStillPending(c *C) {
	pubkeyMgr, err := pubkeymanager.NewPubKeyManager(s.bridge, s.m)
	c.Assert(err, IsNil)
	poolMgr := thorclient.NewPoolMgr(s.bridge)
	e, err := NewSOLClient(s.thorKeys,
		config.BifrostChainConfiguration{
			RPCHost: "http://" + s.server.Listener.Addr().String(),
			BlockScanner: config.BifrostBlockScannerConfiguration{
				ChainID:            common.SOLChain,
				StartBlockHeight:   1,
				HTTPRequestTimeout: time.Second,
				MaxGasLimit:        80000,
			},
		}, nil, s.bridge, s.m, pubkeyMgr, poolMgr)
	c.Assert(err, IsNil)
	c.Assert(pubkeyMgr.Start(), IsNil)
	defer func() { c.Assert(pubkeyMgr.Stop(), IsNil) }()
	e.solScanner.globalNetworkFeeQueue = make(chan common.NetworkFee, 1)
	pubkeys := pubkeyMgr.GetPubKeys()

	txOut := stypes.TxOutItem{
		Chain:            common.SOLChain,
		ToAddress:        "D9A6eE2pZ6oSiGb8BPkag4gvAeEhHvc3eEYPBoLSMshG",
		VaultPubKey:      pubkeys[0],
		VaultPubKeyEddsa: pubkeys[0],
		Coins: common.Coins{
			common.NewCoin(common.SOLAsset, cosmos.NewUint(11047250)),
		},
		MaxGas: common.Gas{
			common.NewCoin(common.SOLAsset, cosmos.NewUint(15000)),
		},
		GasRate: 1,
		Memo:    "OUT:4D91ADAFA69765E7805B5FF2F3A0BA1DBE69E37A1CFCD20C48B99C528AA3EE87",
	}

	c.Assert(e.signerCacheManager.SetSigned(txOut.CacheHash(), txOut.CacheVault(common.SOLChain), "pendingsig"), IsNil)

	result, _, _, err := e.SignTx(txOut, 1)
	c.Assert(err, IsNil)
	c.Assert(result, IsNil)
	c.Assert(e.signerCacheManager.HasSigned(txOut.CacheHash()), Equals, true)
}

func TestTxSigFromRawTx(t *testing.T) {
	sig := make([]byte, 64)
	for i := range sig {
		sig[i] = byte(i)
	}
	expectedSig := base58.Encode(sig)

	// valid: 1-byte count prefix + 64-byte sig + message bytes
	validTx := append([]byte{0x01}, sig...)
	validTx = append(validTx, []byte("message")...)

	tests := []struct {
		name    string
		rawTx   []byte
		wantSig string
		wantOk  bool
	}{
		{"nil input", nil, "", false},
		{"empty input", []byte{}, "", false},
		{"too short - only prefix", []byte{0x01}, "", false},
		{"too short - 64 bytes (missing 1)", append([]byte{0x01}, make([]byte, 63)...), "", false},
		{"exactly 65 bytes", append([]byte{0x01}, sig...), expectedSig, true},
		{"valid tx with message", validTx, expectedSig, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotSig, gotOk := txSigFromRawTx(tc.rawTx)
			require.Equal(t, tc.wantOk, gotOk)
			require.Equal(t, tc.wantSig, gotSig)
		})
	}
}

// solKeysignFailureCall captures a PostKeysignFailure invocation for assertions.
type solKeysignFailureCall struct {
	blame  types2.Blame
	height int64
	memo   string
	coins  common.Coins
	pubkey common.PubKey
}

// mockSOLThorchainBridge records keysign-failure posts emitted by the signer.
type mockSOLThorchainBridge struct {
	thorclient.ThorchainBridge
	postCalls []solKeysignFailureCall
	postErr   error
}

// testConsensusBlockTimeUnix is the fixed THORChain block time the mock bridge
// reports, so the deterministic-blockhash mock RPC (see makeDeterministicSolRPC)
// can craft getBlockTime values that bracket target = t - margin.
const testConsensusBlockTimeUnix = int64(1788116000)

// PostKeysignFailure records the forwarded blame so the Solana signer tests can
// assert whether Thornode reporting happened.
// GetBlockTimestamp stub so SignTx's consensusBlockHash path derives a deterministic
// blockhash from a known consensus time instead of the per-node scanner.
func (m *mockSOLThorchainBridge) GetBlockTimestamp(int64) (time.Time, error) {
	return time.Unix(testConsensusBlockTimeUnix, 0).UTC(), nil
}

func (m *mockSOLThorchainBridge) PostKeysignFailure(blame types2.Blame, height int64, memo string, coins common.Coins, pubkey common.PubKey) (common.TxID, error) {
	m.postCalls = append(m.postCalls, solKeysignFailureCall{
		blame:  blame,
		height: height,
		memo:   memo,
		coins:  coins,
		pubkey: pubkey,
	})
	return common.TxID("ABCD"), m.postErr
}

// mockSOLThorchainKeyManager injects remote-sign failures into the Solana signer.
type mockSOLThorchainKeyManager struct {
	tss.ThorchainKeyManager
	err error
}

// RemoteSign returns the configured TSS error to exercise SignTx failure paths.
func (m *mockSOLThorchainKeyManager) RemoteSign(_ []byte, _ common.SigningAlgo, _ string, _ int64) ([]byte, []byte, error) {
	return nil, nil, m.err
}

// newTestSignerCacheManager creates a CacheManager backed by a temp LevelDB for tests.
func newTestSignerCacheManager(c *C) *signercache.CacheManager {
	storage, err := blockscanner.NewBlockScannerStorageSolana(c.MkDir(), config.LevelDBOptions{})
	c.Assert(err, IsNil)
	mgr, err := signercache.NewSignerCacheManager(storage.GetInternalDb())
	c.Assert(err, IsNil)
	return mgr
}

// TestSignSOLTx_PostKeysignFailureOnEdDSAFinalRound verifies that blamed EdDSA
// terminal-round failures are reported back to Thornode.
func (s *SolTestSuite) TestSignSOLTx_PostKeysignFailureOnEdDSAFinalRound(c *C) {
	bridge := &mockSOLThorchainBridge{}
	vaultPubKey := types2.GetRandomEd25519PubKey()
	toPubKey := types2.GetRandomEd25519PubKey()
	toAddr, err := toPubKey.GetAddress(common.SOLChain)
	c.Assert(err, IsNil)

	blame := types2.Blame{
		FailReason: "error Tss Timeout",
		Round:      "thorchain.tsslib.eddsa.signing.SignRound3Message",
		BlameNodes: []types2.Node{
			{Pubkey: types2.GetRandomEd25519PubKey().String()},
		},
	}
	srv, rc := newDeterministicSOLRPC(c)
	defer srv.Close()
	client := &SOLClient{
		bridge:             bridge,
		localPubKey:        "not-local",
		rpcClient:          rc,
		solScanner:         &SOLScanner{},
		tssKeyManager:      &mockSOLThorchainKeyManager{err: tss.NewKeysignError(blame)},
		signerCacheManager: newTestSignerCacheManager(c),
	}

	tx := stypes.TxOutItem{
		Chain:            common.SOLChain,
		ToAddress:        toAddr,
		VaultPubKeyEddsa: vaultPubKey,
		Coins: common.Coins{
			common.NewCoin(common.SOLAsset, cosmos.NewUint(11047250)),
		},
		MaxGas: common.Gas{
			common.NewCoin(common.SOLAsset, cosmos.NewUint(15000)),
		},
		GasRate: 1,
		Memo:    "OUT:4D91ADAFA69765E7805B5FF2F3A0BA1DBE69E37A1CFCD20C48B99C528AA3EE87",
	}

	result, checkpoint, obs, err := client.SignTx(tx, 123)
	c.Assert(err, NotNil)
	c.Assert(result, NotNil)
	c.Assert(checkpoint, IsNil)
	c.Assert(obs, IsNil)
	c.Assert(errors.As(err, &tss.KeysignError{}), Equals, true)
	c.Assert(bridge.postCalls, HasLen, 1)
	c.Check(bridge.postCalls[0].height, Equals, int64(123))
	c.Check(bridge.postCalls[0].memo, Equals, tx.Memo)
	c.Check(bridge.postCalls[0].coins.String(), Equals, tx.Coins.String())
	c.Check(bridge.postCalls[0].pubkey, Equals, tx.VaultPubKeyEddsa)
	c.Check(bridge.postCalls[0].blame.Round, Equals, blame.Round)
}

// TestSignSOLTx_PostKeysignFailureUsesOriginalMemo verifies memoless Solana
// outbounds report the canonical memo used for signing failure IDs.
func (s *SolTestSuite) TestSignSOLTx_PostKeysignFailureUsesOriginalMemo(c *C) {
	bridge := &mockSOLThorchainBridge{}
	vaultPubKey := types2.GetRandomEd25519PubKey()
	toPubKey := types2.GetRandomEd25519PubKey()
	toAddr, err := toPubKey.GetAddress(common.SOLChain)
	c.Assert(err, IsNil)

	blame := types2.Blame{
		FailReason: "error Tss Timeout",
		Round:      "thorchain.tsslib.eddsa.signing.SignRound3Message",
		BlameNodes: []types2.Node{
			{Pubkey: types2.GetRandomEd25519PubKey().String()},
		},
	}
	srv, rc := newDeterministicSOLRPC(c)
	defer srv.Close()
	client := &SOLClient{
		bridge:             bridge,
		localPubKey:        "not-local",
		rpcClient:          rc,
		solScanner:         &SOLScanner{},
		tssKeyManager:      &mockSOLThorchainKeyManager{err: tss.NewKeysignError(blame)},
		signerCacheManager: newTestSignerCacheManager(c),
	}

	tx := stypes.TxOutItem{
		Chain:            common.SOLChain,
		ToAddress:        toAddr,
		VaultPubKeyEddsa: vaultPubKey,
		Coins: common.Coins{
			common.NewCoin(common.SOLAsset, cosmos.NewUint(11047250)),
		},
		MaxGas: common.Gas{
			common.NewCoin(common.SOLAsset, cosmos.NewUint(15000)),
		},
		GasRate:      1,
		Memo:         "",
		OriginalMemo: "OUT:4D91ADAFA69765E7805B5FF2F3A0BA1DBE69E37A1CFCD20C48B99C528AA3EE87",
	}

	result, checkpoint, obs, err := client.SignTx(tx, 123)
	c.Assert(err, NotNil)
	c.Assert(result, NotNil)
	c.Assert(checkpoint, IsNil)
	c.Assert(obs, IsNil)
	c.Assert(bridge.postCalls, HasLen, 1)
	c.Check(bridge.postCalls[0].memo, Equals, tx.GetMemo())
}

// TestSignSOLTx_DoesNotPostKeysignFailureWithoutBlameNodes keeps the existing
// behavior for generic TSS failures where no culprit set is available.
func (s *SolTestSuite) TestSignSOLTx_DoesNotPostKeysignFailureWithoutBlameNodes(c *C) {
	bridge := &mockSOLThorchainBridge{}
	vaultPubKey := types2.GetRandomEd25519PubKey()
	toPubKey := types2.GetRandomEd25519PubKey()
	toAddr, err := toPubKey.GetAddress(common.SOLChain)
	c.Assert(err, IsNil)

	srv, rc := newDeterministicSOLRPC(c)
	defer srv.Close()
	client := &SOLClient{
		bridge:             bridge,
		localPubKey:        "not-local",
		rpcClient:          rc,
		solScanner:         &SOLScanner{},
		tssKeyManager:      &mockSOLThorchainKeyManager{err: tss.NewKeysignError(types2.Blame{FailReason: "timeout", Round: "thorchain.tsslib.eddsa.signing.SignRound3Message"})},
		signerCacheManager: newTestSignerCacheManager(c),
	}

	tx := stypes.TxOutItem{
		Chain:            common.SOLChain,
		ToAddress:        toAddr,
		VaultPubKeyEddsa: vaultPubKey,
		Coins: common.Coins{
			common.NewCoin(common.SOLAsset, cosmos.NewUint(11047250)),
		},
		MaxGas: common.Gas{
			common.NewCoin(common.SOLAsset, cosmos.NewUint(15000)),
		},
		GasRate: 1,
		Memo:    "OUT:4D91ADAFA69765E7805B5FF2F3A0BA1DBE69E37A1CFCD20C48B99C528AA3EE87",
	}

	result, checkpoint, obs, err := client.SignTx(tx, 123)
	c.Assert(err, NotNil)
	c.Assert(result, NotNil)
	c.Assert(checkpoint, IsNil)
	c.Assert(obs, IsNil)
	c.Assert(bridge.postCalls, HasLen, 0)
}

func TestSolAddrKeyringRoundTrip(t *testing.T) {
	testCases := []struct {
		name         string
		mnemonic     string
		passphrase   string
		solanaPath   string
		expectedPriv string
		expectedPub  string
	}{
		{
			name:         "Default path with empty passphrase",
			mnemonic:     "exotic bulk marriage joy soft fine escape rate nurse else candy radar toy ribbon eight claw cattle whip traffic garbage brain wisdom extra enrich",
			passphrase:   "",
			solanaPath:   "",
			expectedPriv: "d94e229d9b2b0990f80ecf7db3e30fe5fa71bb1332d585fa8baeee55254696ba05e65b7cc845586df97c9493753bab2378b75db3b171d152cfbf9cbd77679f93",
			expectedPub:  "Q2mXbp2hcsCRnzZV4jAitvG6qZGW3NJYmzGGsmRkdkE",
		},
		{
			name:         "Custom path with passphrase",
			mnemonic:     "symbol orchard junior satoshi auto police solid behind ankle bargain kangaroo cave",
			passphrase:   "test-passphrase",
			solanaPath:   "m/44'/501'/0'/0'",
			expectedPriv: "6b7c0ce1a5afd12c7ef637c56f34846d616cc61489dff556533eacd728dc844ea97aef1fea003477242c8351bbb002b496f0565f48ee06f1b86ee98664d87b74",
			expectedPub:  "CQadDdsqrZjxj3t46zY2UbXyF8ZV1CAvmFp6Bs9tq5HM",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			registry := codectypes.NewInterfaceRegistry()
			cdc := codec.NewProtoCodec(registry)
			cryptocodec.RegisterInterfaces(cdc.InterfaceRegistry())
			kb := cKeys.NewInMemory(cdc, func(options *cKeys.Options) {
				options.SupportedAlgos = append(options.SupportedAlgos, ed25519.Ed25519)
			})

			// create a new account
			accountName := "test-account"
			edName := ed25519.SignerNameEDDSA(accountName)
			_, err := kb.NewAccount(edName, tc.mnemonic, tc.passphrase, tc.solanaPath, ed25519.Ed25519)
			require.NoError(t, err)

			// get the account
			keys := thorclient.NewKeysWithKeybase(kb, accountName, tc.passphrase)
			priv, err := keys.GetPrivateKeyEDDSA()
			require.NoError(t, err)

			priv2, err := ed25519.GetPrivateKeyFromMnemonic(tc.mnemonic, tc.passphrase, tc.solanaPath)
			require.NoError(t, err)

			expectedPrivBz, err := hex.DecodeString(tc.expectedPriv)
			require.NoError(t, err)
			expectedPubBz, err := base58.Decode(tc.expectedPub)
			require.NoError(t, err)

			require.Equal(t, expectedPrivBz, priv2)
			require.Equal(t, expectedPrivBz, priv.Bytes())
			require.Equal(t, priv.PubKey().Bytes(), expectedPubBz)
		})
	}
}

// solCacheRPCServer mocks a Solana RPC server for cache-precheck tests.
// method-by-method behavior is parameterized so each test can choose whether
// getTransaction reports the tx as confirmed, missing, or an RPC error.
type solCacheRPCServer struct {
	getTxCalls         int
	getTxReturnsSlot   uint64 // > 0 → confirmed on chain
	getTxReturnsErr    bool   // true → respond with a JSON-RPC error body
	getTxFailedOnChain bool   // true (with slot > 0) → landed but meta.err set (failed execution)
}

func newSolCacheRPCServer(_ *C, s *solCacheRPCServer) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		// Hit concurrently by the background scanner — never touch *C here.
		body, _ := io.ReadAll(req.Body)
		var rpcRequest struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(body, &rpcRequest)
		rw.Header().Set("Content-Type", "application/json")
		// Serve the consensus-blockhash sign path (getSlot/getBlocks/getBlockTime/
		// getBlock/isBlockhashValid) so the re-sign cache tests can reach signing.
		if res, ok := consensusBlockhashRPCResponse(rpcRequest.Method, rpcRequest.Params); ok {
			_, _ = fmt.Fprintf(rw, `{"jsonrpc":"2.0","id":1,"result":%s}`, res)
			return
		}
		switch rpcRequest.Method {
		case "getTransaction":
			s.getTxCalls++
			switch {
			case s.getTxReturnsErr:
				_, _ = rw.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"rpc error"}}`))
			case s.getTxReturnsSlot > 0:
				meta := "{}"
				if s.getTxFailedOnChain {
					// landed but failed execution — mirrors the mainnet #3089 evidence
					meta = `{"err":{"InstructionError":[1,{"Custom":1}]}}`
				}
				_, _ = fmt.Fprintf(rw, `{"jsonrpc":"2.0","id":1,"result":{"slot":%d,"meta":%s,"transaction":{}}}`, s.getTxReturnsSlot, meta)
			default:
				// result:null → tx not on chain
				_, _ = rw.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
			}
		case "getLatestBlockhash":
			_, _ = rw.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"context":{"slot":279644744},"value":{"blockhash":"2Q1p2F8BDaLRkiJURvxTXAFHffC1uVsTXFkWsE7eawHE","lastValidBlockHeight":258824587}}}`))
		default:
			_, _ = rw.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
		}
	}))
}

// newSolCacheTestClient builds a SOLClient wired to a mock RPC server for cache tests.
// Returns the client, the first vault pubkey, and the pubkey manager that callers
// must Stop when done.
func (s *SolTestSuite) newSolCacheTestClient(c *C, server *httptest.Server) (*SOLClient, common.PubKey, *pubkeymanager.PubKeyManager) {
	pubkeyMgr, err := pubkeymanager.NewPubKeyManager(s.bridge, s.m)
	c.Assert(err, IsNil)
	poolMgr := thorclient.NewPoolMgr(s.bridge)
	e, err := NewSOLClient(s.thorKeys,
		config.BifrostChainConfiguration{
			RPCHost: "http://" + server.Listener.Addr().String(),
			BlockScanner: config.BifrostBlockScannerConfiguration{
				ChainID:            common.SOLChain,
				StartBlockHeight:   1,
				HTTPRequestTimeout: time.Second,
				MaxGasLimit:        80000,
			},
		}, nil, s.bridge, s.m, pubkeyMgr, poolMgr)
	c.Assert(err, IsNil)
	c.Assert(pubkeyMgr.Start(), IsNil)
	e.solScanner.globalNetworkFeeQueue = make(chan common.NetworkFee, 1)
	pubkeys := pubkeyMgr.GetPubKeys()
	return e, pubkeys[0], pubkeyMgr
}

func newSolCacheTestTxOut(pub common.PubKey) stypes.TxOutItem {
	return stypes.TxOutItem{
		Chain:            common.SOLChain,
		ToAddress:        "D9A6eE2pZ6oSiGb8BPkag4gvAeEhHvc3eEYPBoLSMshG",
		VaultPubKey:      pub,
		VaultPubKeyEddsa: pub,
		Coins: common.Coins{
			common.NewCoin(common.SOLAsset, cosmos.NewUint(11047250)),
		},
		MaxGas: common.Gas{
			common.NewCoin(common.SOLAsset, cosmos.NewUint(15000)),
		},
		GasRate: 1,
		Memo:    "OUT:4D91ADAFA69765E7805B5FF2F3A0BA1DBE69E37A1CFCD20C48B99C528AA3EE87",
	}
}

// TestSignTxUnexpiredCacheRespectedNoRPC verifies the pre-expiry fast path: while
// the cache entry is still within its expiry window the recorded broadcast may
// still land on Solana (its blockhash has not necessarily expired yet), so SignTx
// must respect the cache unconditionally — no RPC call, no re-sign.
func (s *SolTestSuite) TestSignTxUnexpiredCacheRespectedNoRPC(c *C) {
	rpc := &solCacheRPCServer{} // if called, would return not-on-chain
	server := newSolCacheRPCServer(c, rpc)
	defer server.Close()

	e, pub, pubkeyMgr := s.newSolCacheTestClient(c, server)
	defer func() { c.Assert(pubkeyMgr.Stop(), IsNil) }()

	txOut := newSolCacheTestTxOut(pub)
	expiry := time.Now().Add(time.Hour).UnixMilli()
	c.Assert(e.signerCacheManager.SetSignedWithExpiry(
		txOut.CacheHash(), txOut.CacheVault(common.SOLChain), "cached-sig", expiry,
	), IsNil)

	result, _, _, err := e.SignTx(txOut, 1)
	c.Assert(err, IsNil)
	c.Assert(result, IsNil)                                                   // suppressed
	c.Assert(rpc.getTxCalls, Equals, 0)                                       // no RPC lookup on fast path
	c.Assert(e.signerCacheManager.HasSigned(txOut.CacheHash()), Equals, true) // preserved
}

// TestSignTxExpiredEntryOnChainProtectsAgainstDoubleSpend verifies the critical
// property that motivates the two-gate invariant: when thorchain observation
// consensus lags behind actual Solana inclusion, thornode may reschedule an
// outbound whose broadcast has already landed. The cache entry will be past its
// expiry by the time the reschedule arrives, but because the recorded sig IS on
// chain, SignTx must NOT re-sign — otherwise a second broadcast lands alongside
// the first and we double-spend. The entry must also remain in storage.
func (s *SolTestSuite) TestSignTxExpiredEntryOnChainProtectsAgainstDoubleSpend(c *C) {
	rpc := &solCacheRPCServer{getTxReturnsSlot: 42} // RPC confirms on chain
	server := newSolCacheRPCServer(c, rpc)
	defer server.Close()

	e, pub, pubkeyMgr := s.newSolCacheTestClient(c, server)
	defer func() { c.Assert(pubkeyMgr.Stop(), IsNil) }()

	txOut := newSolCacheTestTxOut(pub)
	expired := time.Now().Add(-time.Minute).UnixMilli()
	c.Assert(e.signerCacheManager.SetSignedWithExpiry(
		txOut.CacheHash(), txOut.CacheVault(common.SOLChain), "slow-obs-sig", expired,
	), IsNil)

	result, _, _, err := e.SignTx(txOut, 1)
	c.Assert(err, IsNil)
	c.Assert(result, IsNil)             // did NOT re-sign
	c.Assert(rpc.getTxCalls, Equals, 1) // RPC was consulted because expired
	c.Assert(e.signerCacheManager.HasSigned(txOut.CacheHash()), Equals, true)
}

// TestSignTxExpiredEntryRPCErrorRespectsCache verifies the fail-closed behavior:
// when the cache is past expiry but the RPC returns an error, SignTx must respect
// the cache — we cannot confirm absence, so re-signing could double-spend.
func (s *SolTestSuite) TestSignTxExpiredEntryRPCErrorRespectsCache(c *C) {
	rpc := &solCacheRPCServer{getTxReturnsErr: true}
	server := newSolCacheRPCServer(c, rpc)
	defer server.Close()

	e, pub, pubkeyMgr := s.newSolCacheTestClient(c, server)
	defer func() { c.Assert(pubkeyMgr.Stop(), IsNil) }()

	txOut := newSolCacheTestTxOut(pub)
	expired := time.Now().Add(-time.Minute).UnixMilli()
	c.Assert(e.signerCacheManager.SetSignedWithExpiry(
		txOut.CacheHash(), txOut.CacheVault(common.SOLChain), "cached-sig", expired,
	), IsNil)

	result, _, _, err := e.SignTx(txOut, 1)
	c.Assert(err, IsNil)
	c.Assert(result, IsNil)                                                   // respected cache
	c.Assert(rpc.getTxCalls, Equals, 1)                                       // RPC attempted
	c.Assert(e.signerCacheManager.HasSigned(txOut.CacheHash()), Equals, true) // preserved
}

// TestSignTxLegacyCacheEntryRespected verifies backward compatibility: a legacy
// SetSigned entry (no expiry, no recorded tx hash) still suppresses re-signing
// without any RPC lookup.
func (s *SolTestSuite) TestSignTxLegacyCacheEntryRespected(c *C) {
	rpc := &solCacheRPCServer{}
	server := newSolCacheRPCServer(c, rpc)
	defer server.Close()

	e, pub, pubkeyMgr := s.newSolCacheTestClient(c, server)
	defer func() { c.Assert(pubkeyMgr.Stop(), IsNil) }()

	txOut := newSolCacheTestTxOut(pub)
	// Legacy SetSigned: permanent flag with no tx hash recorded.
	c.Assert(e.signerCacheManager.SetSigned(
		txOut.CacheHash(), txOut.CacheVault(common.SOLChain), "legacy-sig",
	), IsNil)

	result, _, _, err := e.SignTx(txOut, 1)
	c.Assert(err, IsNil)
	c.Assert(result, IsNil) // suppressed by cache without RPC call
	c.Assert(rpc.getTxCalls, Equals, 0)
}

// The tests below exercise the successful-signing / re-sign / broadcast paths
// through the deterministic consensus-blockhash lookup. s.server (and the
// per-test servers, via consensusBlockhashRPCResponse) canned-serve
// getSlot/getBlocks/getBlockTime/getBlock/isBlockhashValid so SignTx can derive
// a blockhash and sign; the real bridge's GetBlockTimestamp resolves against the
// "block" handler on s.server. The determinism property itself (cross-node hash
// agreement) is validated in the simulation framework, not in a single-process
// unit test.

func (s *SolTestSuite) TestSignSOLTx(c *C) {
	pubkeyMgr, err := pubkeymanager.NewPubKeyManager(s.bridge, s.m)
	c.Assert(err, IsNil)
	poolMgr := thorclient.NewPoolMgr(s.bridge)
	e, err := NewSOLClient(s.thorKeys,
		config.BifrostChainConfiguration{
			RPCHost: "http://" + s.server.Listener.Addr().String(),
			BlockScanner: config.BifrostBlockScannerConfiguration{
				ChainID:            common.SOLChain,
				StartBlockHeight:   1, // avoids querying thorchain for block height
				HTTPRequestTimeout: time.Second,
				MaxGasLimit:        80000,
			},
		}, nil, s.bridge, s.m, pubkeyMgr, poolMgr)
	c.Assert(err, IsNil)
	c.Assert(e, NotNil)
	c.Assert(pubkeyMgr.Start(), IsNil)
	defer func() { c.Assert(pubkeyMgr.Stop(), IsNil) }()
	e.solScanner.globalNetworkFeeQueue = make(chan common.NetworkFee, 1)
	pubkeys := pubkeyMgr.GetPubKeys()

	// Not SOL chain
	result, _, obs, err := e.SignTx(stypes.TxOutItem{
		Chain:       common.BTCChain,
		ToAddress:   "D9A6eE2pZ6oSiGb8BPkag4gvAeEhHvc3eEYPBoLSMshG",
		VaultPubKey: "",
	}, 1)
	c.Assert(err, NotNil)
	c.Assert(result, IsNil)
	c.Assert(obs, IsNil)

	// Valid outbound
	result, _, obs, err = e.SignTx(stypes.TxOutItem{
		Chain:            common.SOLChain,
		ToAddress:        "D9A6eE2pZ6oSiGb8BPkag4gvAeEhHvc3eEYPBoLSMshG",
		VaultPubKey:      pubkeys[0],
		VaultPubKeyEddsa: pubkeys[0],
		Coins: common.Coins{
			common.NewCoin(common.SOLAsset, cosmos.NewUint(11047250)),
		},
		MaxGas: common.Gas{
			common.NewCoin(common.SOLAsset, cosmos.NewUint(15000)),
		},
		GasRate: 1,
		Memo:    "OUT:4D91ADAFA69765E7805B5FF2F3A0BA1DBE69E37A1CFCD20C48B99C528AA3EE87",
	}, 1)

	c.Assert(err, IsNil)
	c.Assert(result, NotNil)
	c.Assert(obs, NotNil)
	c.Assert(obs.Gas, HasLen, 1)
	c.Assert(obs.Gas[0].Amount.Uint64(), Equals, uint64(500))
	c.Assert(obs.Gas[0].Decimals, Equals, int64(9))

	// Already signed — signer cache should suppress re-signing
	txOut := stypes.TxOutItem{
		Chain:            common.SOLChain,
		ToAddress:        "D9A6eE2pZ6oSiGb8BPkag4gvAeEhHvc3eEYPBoLSMshG",
		VaultPubKey:      pubkeys[0],
		VaultPubKeyEddsa: pubkeys[0],
		Coins: common.Coins{
			common.NewCoin(common.SOLAsset, cosmos.NewUint(11047250)),
		},
		MaxGas: common.Gas{
			common.NewCoin(common.SOLAsset, cosmos.NewUint(15000)),
		},
		GasRate: 1,
		Memo:    "OUT:4D91ADAFA69765E7805B5FF2F3A0BA1DBE69E37A1CFCD20C48B99C528AA3EE87",
	}
	c.Assert(e.signerCacheManager.SetSigned(txOut.CacheHash(), txOut.CacheVault(common.SOLChain), "somesig"), IsNil)
	result, _, _, err = e.SignTx(txOut, 1)
	c.Assert(err, IsNil)
	c.Assert(result, IsNil) // suppressed by signer cache
}

func (s *SolTestSuite) TestSignSOLTxReSignsWhenCachedTxMissingFromChain(c *C) {
	pubkeyMgr, err := pubkeymanager.NewPubKeyManager(s.bridge, s.m)
	c.Assert(err, IsNil)
	poolMgr := thorclient.NewPoolMgr(s.bridge)
	e, err := NewSOLClient(s.thorKeys,
		config.BifrostChainConfiguration{
			RPCHost: "http://" + s.server.Listener.Addr().String(),
			BlockScanner: config.BifrostBlockScannerConfiguration{
				ChainID:            common.SOLChain,
				StartBlockHeight:   1,
				HTTPRequestTimeout: time.Second,
				MaxGasLimit:        80000,
			},
		}, nil, s.bridge, s.m, pubkeyMgr, poolMgr)
	c.Assert(err, IsNil)
	c.Assert(pubkeyMgr.Start(), IsNil)
	defer func() { c.Assert(pubkeyMgr.Stop(), IsNil) }()
	e.solScanner.globalNetworkFeeQueue = make(chan common.NetworkFee, 1)
	pubkeys := pubkeyMgr.GetPubKeys()

	txOut := stypes.TxOutItem{
		Chain:            common.SOLChain,
		ToAddress:        "D9A6eE2pZ6oSiGb8BPkag4gvAeEhHvc3eEYPBoLSMshG",
		VaultPubKey:      pubkeys[0],
		VaultPubKeyEddsa: pubkeys[0],
		Coins: common.Coins{
			common.NewCoin(common.SOLAsset, cosmos.NewUint(11047250)),
		},
		MaxGas: common.Gas{
			common.NewCoin(common.SOLAsset, cosmos.NewUint(15000)),
		},
		GasRate: 1,
		Memo:    "OUT:4D91ADAFA69765E7805B5FF2F3A0BA1DBE69E37A1CFCD20C48B99C528AA3EE87",
	}

	expired := time.Now().Add(-time.Minute).UnixMilli()
	c.Assert(e.signerCacheManager.SetSignedWithExpiry(txOut.CacheHash(), txOut.CacheVault(common.SOLChain), "missingsig", expired), IsNil)

	result, _, _, err := e.SignTx(txOut, 1)
	c.Assert(err, IsNil)
	c.Assert(result, NotNil)
	c.Assert(e.signerCacheManager.HasSigned(txOut.CacheHash()), Equals, false)
}

func (s *SolTestSuite) TestBroadcastTxLandedDespiteError(c *C) {
	// Build a test server that rejects sendTransaction but confirms the tx on-chain
	// via getTransaction — simulating a network timeout after the tx was accepted.
	// It also serves the consensus-blockhash lookup so SignTx can derive a hash.
	const landedSlot = uint64(12345)
	broadcastServer := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		var rpcRequest struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(body, &rpcRequest)
		rw.Header().Set("Content-Type", "application/json")
		if res, ok := consensusBlockhashRPCResponse(rpcRequest.Method, rpcRequest.Params); ok {
			_, _ = fmt.Fprintf(rw, `{"jsonrpc":"2.0","id":1,"result":%s}`, res)
			return
		}
		switch rpcRequest.Method {
		case "sendTransaction":
			// Simulate RPC error (e.g. node behind, response dropped)
			_, _ = rw.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32005,"message":"Node is behind by 150 slots"}}`))
		case "getTransaction":
			_, _ = fmt.Fprintf(rw, `{"jsonrpc":"2.0","id":1,"result":{"slot":%d,"meta":{},"transaction":{}}}`, landedSlot)
		case "getLatestBlockhash":
			_, _ = rw.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"context":{"slot":279644744},"value":{"blockhash":"2Q1p2F8BDaLRkiJURvxTXAFHffC1uVsTXFkWsE7eawHE","lastValidBlockHeight":258824587}}}`))
		default:
			_, _ = rw.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
		}
	}))
	defer broadcastServer.Close()

	pubkeyMgr, err := pubkeymanager.NewPubKeyManager(s.bridge, s.m)
	c.Assert(err, IsNil)
	poolMgr := thorclient.NewPoolMgr(s.bridge)
	e, err := NewSOLClient(s.thorKeys,
		config.BifrostChainConfiguration{
			RPCHost: "http://" + broadcastServer.Listener.Addr().String(),
			BlockScanner: config.BifrostBlockScannerConfiguration{
				ChainID:            common.SOLChain,
				StartBlockHeight:   1,
				HTTPRequestTimeout: time.Second,
				MaxGasLimit:        80000,
			},
		}, nil, s.bridge, s.m, pubkeyMgr, poolMgr)
	c.Assert(err, IsNil)
	c.Assert(pubkeyMgr.Start(), IsNil)
	defer func() { c.Assert(pubkeyMgr.Stop(), IsNil) }()
	e.solScanner.globalNetworkFeeQueue = make(chan common.NetworkFee, 1)
	pubkeys := pubkeyMgr.GetPubKeys()

	txOut := stypes.TxOutItem{
		Chain:            common.SOLChain,
		ToAddress:        "D9A6eE2pZ6oSiGb8BPkag4gvAeEhHvc3eEYPBoLSMshG",
		VaultPubKey:      pubkeys[0],
		VaultPubKeyEddsa: pubkeys[0],
		Coins: common.Coins{
			common.NewCoin(common.SOLAsset, cosmos.NewUint(11047250)),
		},
		MaxGas: common.Gas{
			common.NewCoin(common.SOLAsset, cosmos.NewUint(15000)),
		},
		GasRate: 1,
		Memo:    "OUT:4D91ADAFA69765E7805B5FF2F3A0BA1DBE69E37A1CFCD20C48B99C528AA3EE87",
	}

	// Override the broadcast lookup delay so the test doesn't sleep.
	broadcastLookupDelay = 0

	rawTx, _, _, err := e.SignTx(txOut, 1)
	c.Assert(err, IsNil)
	c.Assert(rawTx, NotNil)

	// Derive expected sig from raw bytes before broadcast
	expectedSig, ok := txSigFromRawTx(rawTx)
	c.Assert(ok, Equals, true)

	// Cache should be empty before broadcast
	c.Assert(e.signerCacheManager.HasSigned(txOut.CacheHash()), Equals, false)

	// BroadcastTx should detect the tx on-chain despite the RPC error
	txSig, err := e.BroadcastTx(txOut, rawTx)
	c.Assert(err, IsNil)
	c.Assert(txSig, Equals, expectedSig)

	// Cache must now be written so a future reschedule won't re-sign
	c.Assert(e.signerCacheManager.HasSigned(txOut.CacheHash()), Equals, true)
}

func (s *SolTestSuite) TestSignSOLTx_ObservationUsesOriginalMemo(c *C) {
	pubkeyMgr, err := pubkeymanager.NewPubKeyManager(s.bridge, s.m)
	c.Assert(err, IsNil)
	poolMgr := thorclient.NewPoolMgr(s.bridge)
	client, err := NewSOLClient(s.thorKeys,
		config.BifrostChainConfiguration{
			RPCHost: "http://" + s.server.Listener.Addr().String(),
			BlockScanner: config.BifrostBlockScannerConfiguration{
				ChainID:            common.SOLChain,
				StartBlockHeight:   1,
				HTTPRequestTimeout: time.Second,
				MaxGasLimit:        80000,
			},
		}, nil, s.bridge, s.m, pubkeyMgr, poolMgr)
	c.Assert(err, IsNil)
	c.Assert(pubkeyMgr.Start(), IsNil)
	defer func() { c.Assert(pubkeyMgr.Stop(), IsNil) }()
	client.solScanner.globalNetworkFeeQueue = make(chan common.NetworkFee, 1)

	pubkeys := pubkeyMgr.GetPubKeys()
	originalMemo := "OUT:4D91ADAFA69765E7805B5FF2F3A0BA1DBE69E37A1CFCD20C48B99C528AA3EE87"
	_, _, obs, err := client.SignTx(stypes.TxOutItem{
		Chain:            common.SOLChain,
		ToAddress:        "D9A6eE2pZ6oSiGb8BPkag4gvAeEhHvc3eEYPBoLSMshG",
		VaultPubKey:      pubkeys[0],
		VaultPubKeyEddsa: pubkeys[0],
		Coins: common.Coins{
			common.NewCoin(common.SOLAsset, cosmos.NewUint(11047250)),
		},
		MaxGas: common.Gas{
			common.NewCoin(common.SOLAsset, cosmos.NewUint(15000)),
		},
		GasRate:      1,
		Memo:         "",
		OriginalMemo: originalMemo,
	}, 123)
	c.Assert(err, IsNil)
	c.Assert(obs, NotNil)
	c.Check(obs.Memo, Equals, originalMemo)
}

func (s *SolTestSuite) TestSignTxExpiredEntryLandedButFailedReSigns(c *C) {
	rpc := &solCacheRPCServer{getTxReturnsSlot: 42, getTxFailedOnChain: true} // landed but failed
	server := newSolCacheRPCServer(c, rpc)
	defer server.Close()

	e, pub, pubkeyMgr := s.newSolCacheTestClient(c, server)
	defer func() { c.Assert(pubkeyMgr.Stop(), IsNil) }()

	txOut := newSolCacheTestTxOut(pub)
	expired := time.Now().Add(-time.Minute).UnixMilli()
	c.Assert(e.signerCacheManager.SetSignedWithExpiry(
		txOut.CacheHash(), txOut.CacheVault(common.SOLChain), "failed-sig", expired,
	), IsNil)

	result, _, _, err := e.SignTx(txOut, 1)
	c.Assert(err, IsNil)
	c.Assert(result, NotNil)                                                   // re-signed
	c.Assert(rpc.getTxCalls, Equals, 1)                                        // RPC verified first
	c.Assert(e.signerCacheManager.HasSigned(txOut.CacheHash()), Equals, false) // cleared
}

func (s *SolTestSuite) TestSignTxExpiredEntryNotOnChainReSigns(c *C) {
	rpc := &solCacheRPCServer{} // result:null → not on chain
	server := newSolCacheRPCServer(c, rpc)
	defer server.Close()

	e, pub, pubkeyMgr := s.newSolCacheTestClient(c, server)
	defer func() { c.Assert(pubkeyMgr.Stop(), IsNil) }()

	txOut := newSolCacheTestTxOut(pub)
	expired := time.Now().Add(-time.Minute).UnixMilli()
	c.Assert(e.signerCacheManager.SetSignedWithExpiry(
		txOut.CacheHash(), txOut.CacheVault(common.SOLChain), "dropped-sig", expired,
	), IsNil)

	result, _, _, err := e.SignTx(txOut, 1)
	c.Assert(err, IsNil)
	c.Assert(result, NotNil)                                                   // re-signed
	c.Assert(rpc.getTxCalls, Equals, 1)                                        // RPC verified first
	c.Assert(e.signerCacheManager.HasSigned(txOut.CacheHash()), Equals, false) // cleared
}

func (s *SolTestSuite) TestBroadcastTxWritesExpiringCacheEntry(c *C) {
	// A minimal test server that accepts any broadcast and reports success. It
	// also serves the consensus-blockhash lookup so SignTx can derive a hash.
	broadcastServer := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		var rpcRequest struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(body, &rpcRequest)
		rw.Header().Set("Content-Type", "application/json")
		if res, ok := consensusBlockhashRPCResponse(rpcRequest.Method, rpcRequest.Params); ok {
			_, _ = fmt.Fprintf(rw, `{"jsonrpc":"2.0","id":1,"result":%s}`, res)
			return
		}
		switch rpcRequest.Method {
		case "sendTransaction":
			_, _ = rw.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"broadcast-sig"}`))
		case "getLatestBlockhash":
			_, _ = rw.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"context":{"slot":279644744},"value":{"blockhash":"2Q1p2F8BDaLRkiJURvxTXAFHffC1uVsTXFkWsE7eawHE","lastValidBlockHeight":258824587}}}`))
		default:
			_, _ = rw.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
		}
	}))
	defer broadcastServer.Close()

	e, pub, pubkeyMgr := s.newSolCacheTestClient(c, broadcastServer)
	defer func() { c.Assert(pubkeyMgr.Stop(), IsNil) }()

	txOut := newSolCacheTestTxOut(pub)
	rawTx, _, _, err := e.SignTx(txOut, 1)
	c.Assert(err, IsNil)
	c.Assert(rawTx, NotNil)

	txSig, err := e.BroadcastTx(txOut, rawTx)
	c.Assert(err, IsNil)
	c.Assert(txSig, Equals, "broadcast-sig")

	// Cache should now treat this item as signed and expose the forward index.
	c.Assert(e.signerCacheManager.HasSigned(txOut.CacheHash()), Equals, true)
	sig, ok := e.signerCacheManager.GetSignedTxHash(txOut.CacheHash())
	c.Assert(ok, Equals, true)
	c.Assert(sig, Equals, "broadcast-sig")
}

// TestSignTxAbstainInvalidBlockhashDrops covers the PERMANENT abstain branch: when the
// derived consensus blockhash is no longer valid at the processed tip, it can never
// become valid again at this fixed height, so SignTx returns the (nil,nil,nil,nil)
// no-op — the signer clears the item and THORChain reschedules at a fresh height.
// Returning an error here would instead re-claim the vault's single signing slot every
// pass and starve the vault's other SOL outbounds.
func (s *SolTestSuite) TestSignTxAbstainInvalidBlockhashDrops(c *C) {
	bridge := &mockSOLThorchainBridge{}
	vaultPubKey := types2.GetRandomEd25519PubKey()
	toAddr, err := types2.GetRandomEd25519PubKey().GetAddress(common.SOLChain)
	c.Assert(err, IsNil)

	srv, rc := newSOLRPCBlockhashInvalid(c)
	defer srv.Close()
	client := &SOLClient{
		bridge:             bridge,
		localPubKey:        "not-local",
		rpcClient:          rc,
		solScanner:         &SOLScanner{},
		tssKeyManager:      &mockSOLThorchainKeyManager{},
		signerCacheManager: newTestSignerCacheManager(c),
	}

	tx := stypes.TxOutItem{
		Chain:            common.SOLChain,
		ToAddress:        toAddr,
		VaultPubKeyEddsa: vaultPubKey,
		Coins:            common.Coins{common.NewCoin(common.SOLAsset, cosmos.NewUint(11047250))},
		MaxGas:           common.Gas{common.NewCoin(common.SOLAsset, cosmos.NewUint(15000))},
		GasRate:          1,
		Memo:             "OUT:4D91ADAFA69765E7805B5FF2F3A0BA1DBE69E37A1CFCD20C48B99C528AA3EE87",
	}

	result, checkpoint, obs, err := client.SignTx(tx, 123)
	c.Assert(err, IsNil) // no error -> signer clears the item (drop for reschedule)
	c.Assert(result, IsNil)
	c.Assert(checkpoint, IsNil)
	c.Assert(obs, IsNil)
	c.Assert(bridge.postCalls, HasLen, 0) // abstain is pre-keysign: no blame posted
}

// TestSignTxAbstainBlockhashValidErrorRetains covers the TRANSIENT abstain branch: when
// IsBlockhashValid errors (RPC blip), SignTx fails CLOSED and returns an ERROR so the
// signer RETAINS the outbound and retries, rather than the no-op that would drop it.
func (s *SolTestSuite) TestSignTxAbstainBlockhashValidErrorRetains(c *C) {
	bridge := &mockSOLThorchainBridge{}
	vaultPubKey := types2.GetRandomEd25519PubKey()
	toAddr, err := types2.GetRandomEd25519PubKey().GetAddress(common.SOLChain)
	c.Assert(err, IsNil)

	srv, rc := newSOLRPCBlockhashValidError(c)
	defer srv.Close()
	client := &SOLClient{
		bridge:             bridge,
		localPubKey:        "not-local",
		rpcClient:          rc,
		solScanner:         &SOLScanner{},
		tssKeyManager:      &mockSOLThorchainKeyManager{},
		signerCacheManager: newTestSignerCacheManager(c),
	}

	tx := stypes.TxOutItem{
		Chain:            common.SOLChain,
		ToAddress:        toAddr,
		VaultPubKeyEddsa: vaultPubKey,
		Coins:            common.Coins{common.NewCoin(common.SOLAsset, cosmos.NewUint(11047250))},
		MaxGas:           common.Gas{common.NewCoin(common.SOLAsset, cosmos.NewUint(15000))},
		GasRate:          1,
		Memo:             "OUT:4D91ADAFA69765E7805B5FF2F3A0BA1DBE69E37A1CFCD20C48B99C528AA3EE87",
	}

	result, _, _, err := client.SignTx(tx, 123)
	c.Assert(err, NotNil) // fail closed -> error -> signer retains + retries
	c.Assert(result, IsNil)
}

// TestSignTxAbstainLaggingBackendRetains covers the minContextSlot guard (d3): a
// backend whose bank is BELOW the anchor slot answers value:false, but that is a
// lagging node, not a genuinely expired hash. The guard turns it into an error so
// SignTx RETAINS + retries instead of dropping a possibly-still-valid outbound.
func (s *SolTestSuite) TestSignTxAbstainLaggingBackendRetains(c *C) {
	bridge := &mockSOLThorchainBridge{}
	vaultPubKey := types2.GetRandomEd25519PubKey()
	toAddr, err := types2.GetRandomEd25519PubKey().GetAddress(common.SOLChain)
	c.Assert(err, IsNil)

	srv, rc := newSOLRPCBlockhashLagging(c)
	defer srv.Close()
	client := &SOLClient{
		bridge:             bridge,
		localPubKey:        "not-local",
		rpcClient:          rc,
		solScanner:         &SOLScanner{},
		tssKeyManager:      &mockSOLThorchainKeyManager{},
		signerCacheManager: newTestSignerCacheManager(c),
	}

	tx := stypes.TxOutItem{
		Chain:            common.SOLChain,
		ToAddress:        toAddr,
		VaultPubKeyEddsa: vaultPubKey,
		Coins:            common.Coins{common.NewCoin(common.SOLAsset, cosmos.NewUint(11047250))},
		MaxGas:           common.Gas{common.NewCoin(common.SOLAsset, cosmos.NewUint(15000))},
		GasRate:          1,
		Memo:             "OUT:4D91ADAFA69765E7805B5FF2F3A0BA1DBE69E37A1CFCD20C48B99C528AA3EE87",
	}

	result, _, _, err := client.SignTx(tx, 123)
	c.Assert(err, NotNil) // lagging backend -> error -> retain (NOT drop)
	c.Assert(result, IsNil)
}
