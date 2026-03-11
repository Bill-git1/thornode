package runners

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	ckeys "github.com/cosmos/cosmos-sdk/crypto/keyring"
	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/bifrost/metrics"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient"
	"gitlab.com/thorchain/thornode/v3/cmd"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/config"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

func TestPackage(t *testing.T) { TestingT(t) }

type SolvencyTestSuite struct {
	sp   *DummySolvencyCheckProvider
	m    *metrics.Metrics
	cfg  config.BifrostClientConfiguration
	keys *thorclient.Keys
}

var _ = Suite(&SolvencyTestSuite{})

func (s *SolvencyTestSuite) SetUpSuite(c *C) {
	sp := &DummySolvencyCheckProvider{}
	s.sp = sp

	m, _ := metrics.NewMetrics(config.BifrostMetricsConfiguration{
		Enabled:      false,
		ListenPort:   9090,
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
		Chains:       common.Chains{common.ETHChain},
	})
	s.m = m

	cfg := config.BifrostClientConfiguration{
		ChainID:         "thorchain",
		ChainHost:       "localhost",
		SignerName:      "bob",
		SignerPasswd:    "password",
		ChainHomeFolder: ".",
	}
	registry := codectypes.NewInterfaceRegistry()
	cryptocodec.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)
	kb := ckeys.NewInMemory(cdc)
	_, _, err := kb.NewMnemonic(cfg.SignerName, ckeys.English, cmd.THORChainHDPath, cfg.SignerPasswd, hd.Secp256k1)
	c.Assert(err, IsNil)
	s.cfg = cfg
	s.keys = thorclient.NewKeysWithKeybase(kb, cfg.SignerName, cfg.SignerPasswd)

	c.Assert(err, IsNil)
}

func (s *SolvencyTestSuite) TestSolvencyCheck(c *C) {
	type testCase struct {
		name               string
		thorHeight         int64
		haltHeight         int64
		solvencyHaltHeight int64
		expectCheck        bool
		expectReport       bool
	}

	testCases := []testCase{
		{
			name:               "nothing halted",
			thorHeight:         9,
			haltHeight:         0,
			solvencyHaltHeight: 0,
			expectCheck:        false,
			expectReport:       false,
		},
		{
			name:               "admin halted",
			thorHeight:         9,
			haltHeight:         1,
			solvencyHaltHeight: 0,
			expectCheck:        false,
			expectReport:       false,
		},
		{
			name:               "future chain halt does not trigger reporting",
			thorHeight:         9,
			haltHeight:         10,
			solvencyHaltHeight: 0,
			expectCheck:        false,
			expectReport:       false,
		},
		{
			name:               "active chain halt triggers reporting",
			thorHeight:         10,
			haltHeight:         10,
			solvencyHaltHeight: 0,
			expectCheck:        true,
			expectReport:       true,
		},
		{
			name:               "future solvency halt does not trigger reporting",
			thorHeight:         9,
			haltHeight:         0,
			solvencyHaltHeight: 10,
			expectCheck:        false,
			expectReport:       false,
		},
		{
			name:               "active solvency halt triggers reporting",
			thorHeight:         10,
			haltHeight:         0,
			solvencyHaltHeight: 10,
			expectCheck:        true,
			expectReport:       true,
		},
	}

	for _, tc := range testCases {
		c.Log(tc.name)

		mimirMap := map[string]int64{
			"HaltETHChain":         tc.haltHeight,
			"SolvencyHaltETHChain": tc.solvencyHaltHeight,
		}
		thorHeight := tc.thorHeight

		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c.Logf("================>:%s", r.RequestURI)
			switch {
			case strings.HasPrefix(r.RequestURI, thorclient.MimirEndpoint):
				parts := strings.Split(r.RequestURI, "/key/")
				mimirKey := parts[1]
				mimirValue := int64(0)
				if val, found := mimirMap[mimirKey]; found {
					mimirValue = val
				}
				if _, err := w.Write([]byte(strconv.FormatInt(mimirValue, 10))); err != nil {
					c.Error(err)
				}
			case strings.HasPrefix(r.RequestURI, thorclient.LastBlockEndpoint):
				if _, err := w.Write([]byte(fmt.Sprintf(`[{"chain":"ETH","last_observed_in":0,"last_signed_out":0,"thorchain":%d}]`, thorHeight))); err != nil {
					c.Error(err)
				}
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		})

		server := httptest.NewServer(h)
		bridge, err := thorclient.NewThorchainBridge(config.BifrostClientConfiguration{
			ChainID:         "thorchain",
			ChainHost:       server.Listener.Addr().String(),
			ChainRPC:        server.Listener.Addr().String(),
			SignerName:      "bob",
			SignerPasswd:    "password",
			ChainHomeFolder: ".",
		}, s.m, s.keys)
		c.Assert(err, IsNil)

		s.sp.ResetChecks()
		stopchan := make(chan struct{})
		wg := &sync.WaitGroup{}
		wg.Add(1)
		go SolvencyCheckRunner(common.ETHChain, s.sp, bridge, stopchan, wg, 10*time.Millisecond)
		time.Sleep(50 * time.Millisecond)
		close(stopchan)
		wg.Wait()
		server.Close()

		c.Assert(s.sp.ShouldReportSolvencyRan, Equals, tc.expectCheck)
		c.Assert(s.sp.ReportSolvencyRun, Equals, tc.expectReport)
	}
}

// Mock SolvencyCheckProvider
type DummySolvencyCheckProvider struct {
	ShouldReportSolvencyRan bool
	ReportSolvencyRun       bool
}

func (d *DummySolvencyCheckProvider) ResetChecks() {
	d.ShouldReportSolvencyRan = false
	d.ReportSolvencyRun = false
}

func (d *DummySolvencyCheckProvider) GetHeight() (int64, error) {
	return 0, nil
}

func (d *DummySolvencyCheckProvider) ShouldReportSolvency(height int64) bool {
	d.ShouldReportSolvencyRan = true
	return true
}

func (d *DummySolvencyCheckProvider) ReportSolvency(height int64) error {
	d.ReportSolvencyRun = true
	return nil
}

func (s *SolvencyTestSuite) TestIsVaultSolvent(c *C) {
	vault := types.Vault{
		BlockHeight: 1,
		PubKey:      types.GetRandomPubKey(),
		Coins: common.NewCoins(
			common.NewCoin(common.ETHAsset, cosmos.NewUint(102400000000)),
		),
		Type:   types.VaultType_AsgardVault,
		Status: types.VaultStatus_ActiveVault,
	}
	acct := common.Account{
		Sequence:      0,
		AccountNumber: 0,
		Coins:         common.NewCoins(common.NewCoin(common.ETHAsset, cosmos.NewUint(102400000000))),
	}
	c.Assert(IsVaultSolvent(acct, vault, cosmos.NewUint(0)), Equals, true)
	acct = common.Account{
		Sequence:      0,
		AccountNumber: 0,
		Coins:         common.NewCoins(common.NewCoin(common.ETHAsset, cosmos.NewUint(102305000000))),
	}
	c.Assert(IsVaultSolvent(acct, vault, cosmos.NewUint(80000*120)), Equals, true)
	acct = common.Account{
		Sequence:      0,
		AccountNumber: 0,
		Coins:         common.NewCoins(common.NewCoin(common.ETHAsset, cosmos.NewUint(102205000000))),
	}
	c.Assert(IsVaultSolvent(acct, vault, cosmos.NewUint(80000*120)), Equals, false)
}
