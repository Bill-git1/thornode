package observer

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	cKeys "github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/prometheus/client_golang/prometheus/testutil"
	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/bifrost/metrics"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/evm"
	"gitlab.com/thorchain/thornode/v3/bifrost/pubkeymanager"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
	"gitlab.com/thorchain/thornode/v3/cmd"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/config"
	"gitlab.com/thorchain/thornode/v3/x/thorchain"
	types2 "gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

func TestPackage(t *testing.T) { TestingT(t) }

type ObserverSuite struct {
	metrics  *metrics.Metrics
	thorKeys *thorclient.Keys
	bridge   thorclient.ThorchainBridge
	client   *evm.EVMClient
}

var _ = Suite(&ObserverSuite{})

////////////////////////////////////////////////////////////////////////////////////////
// Mock
////////////////////////////////////////////////////////////////////////////////////////

func (s *ObserverSuite) ResetMockClient(c *C) {
	pubkeyMgr, err := pubkeymanager.NewPubKeyManager(s.bridge, s.metrics)
	c.Assert(err, IsNil)
	poolMgr := thorclient.NewPoolMgr(s.bridge)

	mockEvmRPC := httptest.NewServer(
		http.HandlerFunc(
			func(rw http.ResponseWriter, req *http.Request) {
				var body []byte
				body, err = io.ReadAll(req.Body)
				c.Assert(err, IsNil)
				type RPCRequest struct {
					JSONRPC string          `json:"jsonrpc"`
					ID      interface{}     `json:"id"`
					Method  string          `json:"method"`
					Params  json.RawMessage `json:"params"`
				}
				var rpcRequest RPCRequest
				err = json.Unmarshal(body, &rpcRequest)
				c.Assert(err, IsNil)
				if rpcRequest.Method == "eth_chainId" {
					_, err = rw.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0xf"}`))
					c.Assert(err, IsNil)
				}
				if rpcRequest.Method == "eth_blockNumber" {
					_, err = rw.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x7"}`))
					c.Assert(err, IsNil)
				}
			}))

	httpRequestTimeout, _ := time.ParseDuration("5s")

	s.client, err = evm.NewEVMClient(
		s.thorKeys,
		config.BifrostChainConfiguration{
			RPCHost: mockEvmRPC.URL,
			BlockScanner: config.BifrostBlockScannerConfiguration{
				HTTPRequestTimeout:  httpRequestTimeout,
				BlockScanProcessors: 1,
				ChainID:             common.AVAXChain,
				MaxHTTPRequestRetry: 10,
				EnforceBlockHeight:  true,
				GasCacheBlocks:      100,
			},
		},
		nil,
		s.bridge,
		s.metrics,
		pubkeyMgr,
		poolMgr,
	)

	c.Assert(err, IsNil)
	c.Assert(s.client, NotNil)
}

////////////////////////////////////////////////////////////////////////////////////////
// Setup
////////////////////////////////////////////////////////////////////////////////////////

func (s *ObserverSuite) SetUpSuite(c *C) {
	types2.SetupConfigForTest()

	server := httptest.NewServer(
		http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			switch {
			case strings.HasPrefix(req.RequestURI, thorclient.MimirEndpoint):
				buf, err := os.ReadFile("../../test/fixtures/endpoints/mimir/mimir.json")
				c.Assert(err, IsNil)
				_, err = rw.Write(buf)
				c.Assert(err, IsNil)
			case strings.HasPrefix(req.RequestURI, "/thorchain/lastblock"):
				// NOTE: weird pattern in GetBlockHeight uses first thorchain height.
				_, err := rw.Write([]byte(`[
          {
            "chain": "NOOP",
            "lastobservedin": 0,
            "lastsignedout": 0,
            "thorchain": 0
          }
        ]`))
				c.Assert(err, IsNil)
			case strings.HasPrefix(req.RequestURI, "/"):
				_, err := rw.Write([]byte(`{
          "jsonrpc": "2.0",
          "id": 0,
          "result": {
            "height": "1",
            "hash": "E7FDA9DE4D0AD37D823813CB5BC0D6E69AB0D41BB666B65B965D12D24A3AE83C",
            "logs": [
              {
                "success": "true",
                "log": ""
              }
            ]
          }
        }`))
				c.Assert(err, IsNil)
			default:
				c.Errorf("invalid server query: %s", req.RequestURI)
			}
		}))

	var err error
	s.metrics, err = metrics.NewMetrics(config.BifrostMetricsConfiguration{
		Enabled:      false,
		ListenPort:   9000,
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
		Chains:       common.Chains{common.AVAXChain},
	})
	c.Assert(s.metrics, NotNil)
	c.Assert(err, IsNil)

	cfg := config.BifrostClientConfiguration{
		ChainID:      "thorchain",
		ChainHost:    server.Listener.Addr().String(),
		ChainRPC:     server.Listener.Addr().String(),
		SignerName:   "bob",
		SignerPasswd: "password",
	}

	registry := codectypes.NewInterfaceRegistry()
	cryptocodec.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)
	kb := cKeys.NewInMemory(cdc)
	_, _, err = kb.NewMnemonic(cfg.SignerName, cKeys.English, cmd.THORChainHDPath, cfg.SignerPasswd, hd.Secp256k1)
	c.Assert(err, IsNil)
	s.thorKeys = thorclient.NewKeysWithKeybase(kb, cfg.SignerName, cfg.SignerPasswd)

	c.Assert(s.thorKeys, NotNil)
	s.bridge, err = thorclient.NewThorchainBridge(cfg, s.metrics, s.thorKeys)
	c.Assert(s.bridge, NotNil)
	c.Assert(err, IsNil)
	priv, err := s.thorKeys.GetPrivateKey()
	c.Assert(err, IsNil)
	tmp, err := cryptocodec.ToCmtPubKeyInterface(priv.PubKey())
	c.Assert(err, IsNil)
	_, err = common.NewPubKeyFromCrypto(tmp)
	c.Assert(err, IsNil)

	s.ResetMockClient(c)
}

////////////////////////////////////////////////////////////////////////////////////////
// Setup
////////////////////////////////////////////////////////////////////////////////////////

func (s *ObserverSuite) TestProcess(c *C) {
	pubkeyMgr, err := pubkeymanager.NewPubKeyManager(s.bridge, s.metrics)
	c.Assert(err, IsNil)
	obs, err := NewObserver(
		pubkeyMgr,
		map[common.Chain]chainclients.ChainClient{common.AVAXChain: s.client},
		s.bridge,
		s.metrics,
		"",
		metrics.NewTssKeysignMetricMgr(),
	)
	c.Assert(obs, NotNil)
	c.Assert(err, IsNil)
	err = obs.Start()
	c.Assert(err, IsNil)
	time.Sleep(time.Second * 2)
	metric, err := s.metrics.GetCounterVec(metrics.ObserverError).GetMetricWithLabelValues("fail_to_send_to_thorchain", "1")
	c.Assert(err, IsNil)
	c.Check(int(testutil.ToFloat64(metric)), Equals, 0)

	err = obs.Stop()
	c.Assert(err, IsNil)
}

func (s *ObserverSuite) TestErrataTx(c *C) {
	pubkeyMgr, err := pubkeymanager.NewPubKeyManager(s.bridge, s.metrics)
	c.Assert(err, IsNil)
	obs, err := NewObserver(pubkeyMgr, nil, s.bridge, s.metrics, "", metrics.NewTssKeysignMetricMgr())
	c.Assert(obs, NotNil)
	c.Assert(err, IsNil)
	c.Assert(obs.sendErrataTxToThorchain(25, thorchain.GetRandomTxHash(), common.ETHChain), IsNil)
}

func (s *ObserverSuite) TestGetSaversMemo(c *C) {
	pubkeyMgr, err := pubkeymanager.NewPubKeyManager(s.bridge, s.metrics)
	c.Assert(err, IsNil)
	obs, err := NewObserver(
		pubkeyMgr,
		map[common.Chain]chainclients.ChainClient{},
		s.bridge,
		s.metrics,
		"",
		metrics.NewTssKeysignMetricMgr(),
	)
	c.Assert(obs, NotNil)
	c.Assert(err, IsNil)

	usdc, err := common.NewAsset("ETH.USDC-0x9999999999999999999999999999999999999999")
	c.Assert(err, IsNil)

	ethSaversTx := types.TxInItem{
		BlockHeight: 1024,
		Tx:          "tx1",
		Memo:        "",
		Sender:      thorchain.GetRandomETHAddress().String(),
		To:          thorchain.GetRandomETHAddress().String(),
		Coins: common.Coins{
			common.NewCoin(usdc, cosmos.NewUint(1025)),
		},
		Gas:                 nil,
		ObservedVaultPubKey: thorchain.GetRandomPubKey(),
	}

	// memo should be withdraw 1024 basis points
	memo := obs.getSaversMemo(common.ETHChain, ethSaversTx)
	c.Assert(memo, Equals, "-:ETH/USDC-0X9999999999999999999999999999999999999999:1024")

	// memo should be withdraw 1000 basis points
	ethSaversTx.Coins = common.NewCoins(common.NewCoin(common.ETHAsset, cosmos.NewUint(1001)))
	memo = obs.getSaversMemo(common.ETHChain, ethSaversTx)
	c.Assert(memo, Equals, "-:ETH/ETH:1000")

	// memo should be add
	ethSaversTx.Coins = common.NewCoins(common.NewCoin(common.ETHAsset, cosmos.NewUint(20_000)))
	memo = obs.getSaversMemo(common.ETHChain, ethSaversTx)
	c.Assert(memo, Equals, "+:ETH/ETH")

	btcSaversTx := types.TxInItem{
		BlockHeight: 1024,
		Tx:          "tx1",
		Memo:        "",
		Sender:      thorchain.GetRandomBTCAddress().String(),
		To:          thorchain.GetRandomBTCAddress().String(),
		Coins: common.Coins{
			common.NewCoin(common.BTCAsset, cosmos.NewUint(1000)),
		},
		Gas:                 nil,
		ObservedVaultPubKey: thorchain.GetRandomPubKey(),
	}

	// memo should be empty, amount not above dust threshold
	memo = obs.getSaversMemo(common.BTCChain, btcSaversTx)
	c.Assert(memo, Equals, "")

	// memo should still be empty, amount is at the dust threshold, but you can't withdraw 0 basis points
	btcSaversTx.Coins = common.NewCoins(common.NewCoin(common.BTCAsset, cosmos.NewUint(10_000)))
	memo = obs.getSaversMemo(common.BTCChain, btcSaversTx)
	c.Assert(memo, Equals, "")

	// memo should be withdraw 500 basis points
	btcSaversTx.Coins = common.NewCoins(common.NewCoin(common.BTCAsset, cosmos.NewUint(10_500)))
	memo = obs.getSaversMemo(common.BTCChain, btcSaversTx)
	c.Assert(memo, Equals, "-:BTC/BTC:500")

	// memo should be withdraw 10_000 basis points
	btcSaversTx.Coins = common.NewCoins(common.NewCoin(common.BTCAsset, cosmos.NewUint(20_000)))
	memo = obs.getSaversMemo(common.BTCChain, btcSaversTx)
	c.Assert(memo, Equals, "-:BTC/BTC:10000")

	// memo should be add
	btcSaversTx.Coins = common.NewCoins(common.NewCoin(common.BTCAsset, cosmos.NewUint(40_000)))
	memo = obs.getSaversMemo(common.BTCChain, btcSaversTx)
	c.Assert(memo, Equals, "+:BTC/BTC")
}
