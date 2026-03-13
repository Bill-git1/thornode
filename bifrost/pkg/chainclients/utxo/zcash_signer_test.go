package utxo

import (
	"encoding/hex"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcec"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	cKeys "github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/storage"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/shared/mock"
	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/bifrost/metrics"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/shared/utxo"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient"
	stypes "gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
	"gitlab.com/thorchain/thornode/v3/cmd"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/config"
	"gitlab.com/thorchain/thornode/v3/x/thorchain"
	types2 "gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

type ZcashSignerSuite struct {
	client   *Client
	chainRpc *httptest.Server
	thorApi  *httptest.Server
	bridge   thorclient.ThorchainBridge
	cfg      config.BifrostChainConfiguration
	m        *metrics.Metrics
	db       *leveldb.DB
	keys     *thorclient.Keys
}

var _ = Suite(&ZcashSignerSuite{})

func zecFixtureVaultPubKey(c *C) common.PubKey {
	raw := "thorpub1addwnpepqt7qug8vk9r3saw8n4r803ydj2g3dqwx0mvq5akhnze86fc536xcy2cr8a2"
	if common.CurrentChainNetwork == common.MockNet {
		raw = "tthorpub1zcjduepqrcthx0ke3r2z39rp42xrr777af7qfcs6wcxtxck6tj9j0ap8cl0q0msnrn"
	}

	pubKey, err := common.NewPubKey(raw)
	c.Assert(err, IsNil)
	return pubKey
}

func (s *ZcashSignerSuite) SetUpSuite(c *C) {
	types2.SetupConfigForTest()
	registry := codectypes.NewInterfaceRegistry()
	cryptocodec.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)
	kb := cKeys.NewInMemory(cdc)
	_, _, err := kb.NewMnemonic(bob, cKeys.English, cmd.THORChainHDPath, password, hd.Secp256k1)
	c.Assert(err, IsNil)
	s.keys = thorclient.NewKeysWithKeybase(kb, bob, password)
}

func (s *ZcashSignerSuite) SetUpTest(c *C) {
	s.m = GetMetricForTest(c, common.ZECChain)
	s.cfg = config.BifrostChainConfiguration{
		ChainID:     "ZEC",
		UserName:    bob,
		Password:    password,
		DisableTLS:  true,
		HTTPostMode: true,
		BlockScanner: config.BifrostBlockScannerConfiguration{
			StartBlockHeight: 1, // avoids querying thorchain for block height
		},
	}

	ns := strconv.Itoa(time.Now().Nanosecond())
	thordir := filepath.Join(os.TempDir(), ns, ".thorcli")
	cfg := config.BifrostClientConfiguration{
		ChainID:         "thorchain",
		ChainHost:       "localhost",
		SignerName:      bob,
		SignerPasswd:    password,
		ChainHomeFolder: thordir,
	}

	s.chainRpc = mock.NewChainRpc(common.ZECChain)
	s.thorApi = mock.NewThornodeApi()

	var err error
	s.cfg.RPCHost = s.chainRpc.Listener.Addr().String()
	cfg.ChainHost = s.thorApi.Listener.Addr().String()
	s.bridge, err = thorclient.NewThorchainBridge(cfg, s.m, s.keys)
	c.Assert(err, IsNil)
	s.client, err = NewClient(s.keys, s.cfg, nil, s.bridge, s.m)
	c.Assert(err, IsNil)
	memStorage := storage.NewMemStorage()
	db, err := leveldb.Open(memStorage, nil)
	c.Assert(err, IsNil)
	s.db = db
	s.client.temporalStorage, err = utxo.NewTemporalStorage(db, 0)
	c.Assert(err, IsNil)
	c.Assert(s.client, NotNil)
}

func (s *ZcashSignerSuite) TearDownTest(c *C) {
	s.chainRpc.Close()
	s.thorApi.Close()
	c.Assert(s.db.Close(), IsNil)
}

// TestSignTx tests transaction signing with various error cases.
// Tests: wrong chain, invalid pubkey, invalid address, and insufficient UTXOs.
func (s *ZcashSignerSuite) TestSignTx(c *C) {
	testCases := []struct {
		Name        string
		TxOutItem   stypes.TxOutItem
		ExpectError bool
		ExpectNil   bool
		Reason      string
	}{
		{
			Name: "Wrong chain returns error",
			TxOutItem: stypes.TxOutItem{
				Chain:       common.ETHChain, // Wrong chain
				ToAddress:   types2.GetRandomETHAddress(),
				VaultPubKey: types2.GetRandomPubKey(),
				Coins: common.Coins{
					common.NewCoin(common.ZECAsset, cosmos.NewUint(10000000000)),
				},
				MaxGas: common.Gas{
					common.NewCoin(common.ZECAsset, cosmos.NewUint(1001)),
				},
			},
			ExpectError: true,
			ExpectNil:   true,
			Reason:      "ETH chain tx should not be signed by ZEC client",
		},
		{
			Name: "Invalid pubkey returns error",
			TxOutItem: stypes.TxOutItem{
				Chain:       common.ZECChain,
				ToAddress:   "t1RMxNfN8Q7iwZw7UJB4CqzatxsBmGeYMe2",
				VaultPubKey: common.PubKey("invalidpubkey"), // Invalid
				Coins: common.Coins{
					common.NewCoin(common.ZECAsset, cosmos.NewUint(10000000000)),
				},
				MaxGas: common.Gas{
					common.NewCoin(common.ZECAsset, cosmos.NewUint(1001)),
				},
			},
			ExpectError: true,
			ExpectNil:   true,
			Reason:      "Invalid pubkey cannot derive signing key",
		},
		{
			Name: "Invalid to address returns error",
			TxOutItem: stypes.TxOutItem{
				Chain:       common.ZECChain,
				ToAddress:   "invalidaddress", // Invalid ZEC address
				VaultPubKey: types2.GetRandomPubKey(),
				Coins: common.Coins{
					common.NewCoin(common.ZECAsset, cosmos.NewUint(10000000000)),
				},
				MaxGas: common.Gas{
					common.NewCoin(common.ZECAsset, cosmos.NewUint(1001)),
				},
			},
			ExpectError: true,
			ExpectNil:   true,
			Reason:      "Invalid destination address cannot create output script",
		},
	}

	for i, tc := range testCases {
		result, _, _, err := s.client.SignTx(tc.TxOutItem, int64(i+1))
		if tc.ExpectError {
			c.Assert(err, NotNil, Commentf("Test case: %s - %s", tc.Name, tc.Reason))
		} else {
			c.Assert(err, IsNil, Commentf("Test case: %s - %s", tc.Name, tc.Reason))
		}
		if tc.ExpectNil {
			c.Assert(result, IsNil, Commentf("Test case: %s - %s", tc.Name, tc.Reason))
		}
	}
}

// TestSignTxInsufficientUTXOs tests that signing fails when there are no UTXOs available.
// This verifies the UTXO selection logic properly reports insufficient funds.
func (s *ZcashSignerSuite) TestSignTxInsufficientUTXOs(c *C) {
	addr, err := types2.GetRandomPubKey().GetAddress(common.ZECChain)
	c.Assert(err, IsNil)
	inHash := thorchain.GetRandomTxHash()
	memo := "OUT:" + inHash.String() // Memo must be parsable

	txOutItem := stypes.TxOutItem{
		Chain:       common.ZECChain,
		ToAddress:   addr,
		VaultPubKey: "tthorpub1addwnpepqw2k68efthm08f0f5akhjs6fk5j2pze4wkwt4fmnymf9yd463puruhh0lyz",
		Coins: common.Coins{
			common.NewCoin(common.ZECAsset, cosmos.NewUint(10)),
		},
		MaxGas: common.Gas{
			common.NewCoin(common.ZECAsset, cosmos.NewUint(1000)),
		},
		InHash:  inHash,
		OutHash: "",
		Memo:    memo,
	}

	// Add a block meta with customer transaction
	txHash := "500468e8f504eaeee3c53f826dac132dbff2abf824b5361d10d712a199917d65"
	blockMeta := utxo.NewBlockMeta("0000000000d2c231aea395dfd756cbc15adf86ef95b8229aabb2609f12d35555",
		100,
		"0000000000873c222527135b3817b3be88bb0569d8a58fb7860260c22ce823bb")
	blockMeta.AddCustomerTransaction(txHash)
	c.Assert(s.client.temporalStorage.SaveBlockMeta(blockMeta.Height, blockMeta), IsNil)

	// Set up private key for signing - but listunspent will return 0 UTXOs
	// for this address because the fixture doesn't match the derived vault address
	priKeyBuf, err := hex.DecodeString("b404c5ec58116b5f0fe13464a92e46626fc5db130e418cbce98df86ffe9317c5")
	c.Assert(err, IsNil)
	pkey, _ := btcec.PrivKeyFromBytes(btcec.S256(), priKeyBuf)
	c.Assert(pkey, NotNil)
	c.Assert(pkey.PubKey(), NotNil)
	s.client.nodePrivKey = pkey
	s.client.nodePubKey, err = bech32AccountPubKey(pkey)
	c.Assert(err, IsNil)
	txOutItem.VaultPubKey = s.client.nodePubKey

	// Signing should fail due to insufficient UTXOs
	buf, _, _, err := s.client.SignTx(txOutItem, 1)
	c.Assert(err, NotNil, Commentf("Expected error due to insufficient UTXOs"))
	c.Assert(buf, IsNil)
}

func (s *ZcashSignerSuite) TestVinsUnspentRejectsSpentCachedZECInput(c *C) {
	const txID = "500468e8f504eaeee3c53f826dac132dbff2abf824b5361d10d712a199917d65"
	hash, err := chainhash.NewHashFromStr(txID)
	c.Assert(err, IsNil)

	tx := stypes.TxOutItem{
		Chain:       common.ZECChain,
		VaultPubKey: zecFixtureVaultPubKey(c),
	}
	vin := wire.NewTxIn(wire.NewOutPoint(hash, 0), nil, nil)

	unspent, err := s.client.vinsUnspent(tx, []*wire.TxIn{vin})
	c.Assert(err, IsNil)
	c.Assert(unspent, Equals, true)

	c.Assert(s.client.temporalStorage.SetSpentUtxos([]string{formatUtxoKey(txID, 0)}, 123), IsNil)

	unspent, err = s.client.vinsUnspent(tx, []*wire.TxIn{vin})
	c.Assert(err, IsNil)
	c.Assert(unspent, Equals, false)
}

// TestBroadcastTx tests transaction broadcasting behavior.
// ZEC chain sends raw bytes directly to RPC without local validation (unlike BTC/LTC/DOGE/BCH).
// This tests that: 1) empty payload fails, 2) non-empty payload is sent to RPC.
func (s *ZcashSignerSuite) TestBroadcastTx(c *C) {
	txOutItem := stypes.TxOutItem{
		Chain:       common.ZECChain,
		ToAddress:   "t1RMxNfN8Q7iwZw7UJB4CqzatxsBmGeYMe2",
		VaultPubKey: types2.GetRandomPubKey(),
		Coins: common.Coins{
			common.NewCoin(common.ZECAsset, cosmos.NewUint(10)),
		},
		MaxGas: common.Gas{
			common.NewCoin(common.ZECAsset, cosmos.NewUint(1)),
		},
	}

	// Empty payload should return error
	emptyInput := []byte{}
	_, err := s.client.BroadcastTx(txOutItem, emptyInput)
	c.Assert(err, NotNil, Commentf("Empty payload should fail"))
	c.Assert(strings.Contains(err.Error(), "empty"), Equals, true,
		Commentf("Error should mention empty payload: %v", err))

	// Non-empty payload sends to RPC (mock returns success)
	// ZEC doesn't validate tx format locally, just forwards to RPC
	somePayload := []byte("some payload bytes")
	txid, err := s.client.BroadcastTx(txOutItem, somePayload)
	c.Assert(err, IsNil, Commentf("Non-empty payload should be forwarded to RPC"))
	c.Assert(txid, Not(Equals), "", Commentf("Should return txid from RPC"))
}

// TestIsSelfTransaction tests detection of transactions originated by this node.
// Transactions stored in block meta as "self" should be recognized.
func (s *ZcashSignerSuite) TestIsSelfTransaction(c *C) {
	testHash := "500468e8f504eaeee3c53f826dac132dbff2abf824b5361d10d712a199917d65"

	// Initially, transaction should not be recognized as self
	c.Check(s.client.isSelfTransaction(testHash), Equals, false,
		Commentf("Unknown transaction should not be self"))

	// Add to block meta as self transaction
	bm := utxo.NewBlockMeta("", 1024, "")
	bm.AddSelfTransaction(testHash)
	c.Assert(s.client.temporalStorage.SaveBlockMeta(1024, bm), IsNil)

	// Now it should be recognized
	c.Check(s.client.isSelfTransaction(testHash), Equals, true,
		Commentf("Transaction in self meta should be recognized"))
}

func (s *ZcashSignerSuite) TestGetGasCoinZEC(c *C) {
	testCases := []struct {
		Vin         int
		Memo        string
		Info        string
		ExpectedGas uint64
	}{
		{
			Info:        "empty memo counts as 1 logical action, > 1 vin",
			Vin:         1,
			Memo:        "",
			ExpectedGas: 15_000,
		},
		{
			Info:        "short memo counts as 1 logical action == 3 vin",
			Vin:         3,
			Memo:        "TEST",
			ExpectedGas: 15_000,
		},
		{
			Info:        "more logical actions from vin than memo",
			Vin:         6,
			Memo:        "TEST",
			ExpectedGas: 30_000,
		},
		{
			Info:        "memo counts as 7 logical actions (12+196/34 byte)",
			Vin:         4,
			Memo:        "OUT:2180B871F2DEA2546E1403DBFE9C26B062ABAFFD979CF3A65F2B4D2230105CF12180B871F2DEA2546E1403DBFE9C26B062ABAFFD979CF3A65F2B4D2230105CF12180B871F2DEA2546E1403DBFE9C26B062ABAFFD979CF3A65F2B4D2230105CF1",
			ExpectedGas: 45_000,
		},
		{
			Info:        "memo counts as 3 logical actions (12+68/34 byte)",
			Vin:         4,
			Memo:        "OUT:2180B871F2DEA2546E1403DBFE9C26B062ABAFFD979CF3A65F2B4D2230105CF1",
			ExpectedGas: 25_000,
		},
	}

	for _, tc := range testCases {
		tx := wire.NewMsgTx(wire.TxVersion)
		for i := 0; i < tc.Vin; i++ {
			tx.AddTxIn(&wire.TxIn{})
		}

		gas := s.client.getGasCoinZEC(tx, tc.Memo)
		c.Assert(gas.Amount.Uint64(), Equals, tc.ExpectedGas, Commentf("vin=%d, memo=%d => %s, got: %d", len(tx.TxIn), len(tc.Memo), tc.Info, gas.Amount.Uint64()))
	}
}
