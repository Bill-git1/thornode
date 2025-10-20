package solana

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
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
	"gitlab.com/thorchain/thornode/v3/bifrost/metrics"
	"gitlab.com/thorchain/thornode/v3/bifrost/pubkeymanager"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient"
	stypes "gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
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
		switch req.RequestURI {
		case thorclient.ChainVersionEndpoint:
			_, writeErr := rw.Write([]byte(`{"current":"` + types2.GetCurrentVersion().String() + `"}`))
			c.Assert(writeErr, IsNil)
		case thorclient.PubKeysEndpoint:
			content := fmt.Sprintf(`{
  "asgard": [
    {
      "pub_key_eddsa": "%s"
    }
  ]
}`, pubKey)
			rw.Header().Set("Content-Type", "application/json")
			if _, err = rw.Write([]byte(content)); err != nil {
				c.Fatal(err)
			}
			// httpTestHandler(c, rw, "../../../../test/fixtures/endpoints/vaults/pubKeys.json")
		default:
			body, readErr := io.ReadAll(req.Body)
			c.Assert(readErr, IsNil)
			type RPCRequest struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      interface{}     `json:"id"`
				Method  string          `json:"method"`
				Params  json.RawMessage `json:"params"`
			}
			var rpcRequest RPCRequest
			err = json.Unmarshal(body, &rpcRequest)
			c.Assert(err, IsNil)
			if rpcRequest.Method == "getBlock" {
				httpTestHandler(c, rw, "./test/getBlock.json")
			}
			if rpcRequest.Method == "getLatestBlockhash" {
				// fmt.Println("GET LATEST BLOCK HASH", rpcRequest)
				httpTestHandler(c, rw, "./test/getLatestBlockHash.json")
			}
		}
	}))

	s.server = server

	cfg.ChainHost = server.Listener.Addr().String()

	ns := strconv.Itoa(time.Now().Nanosecond())
	c.Assert(os.Setenv("NET", "stagenet"), IsNil)

	s.thordir = filepath.Join(os.TempDir(), ns, ".thorcli")

	s.thorKeys = thorclient.NewKeysWithKeybase(kb, cfg.SignerName, cfg.SignerPasswd)

	s.bridge, err = thorclient.NewThorchainBridge(cfg, s.m, s.thorKeys)
	c.Assert(err, IsNil)
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

	e.solScanner.recentBlockHash = "asdfasdf"

	// Create TX
	tx, err := e.CreateTx(stypes.TxOutItem{
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
	})
	c.Assert(err, IsNil)
	c.Assert(tx, NotNil)

	// Serialize TX
	rawTx, err := tx.Serialize()
	c.Assert(err, IsNil)
	c.Assert(len(rawTx), Equals, 293)
}

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

	e.solScanner.recentBlockHash = "asdfasdf"

	// Valid outbound
	result, _, _, err = e.SignTx(stypes.TxOutItem{
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
	// c.Assert(obs, NotNil)
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
