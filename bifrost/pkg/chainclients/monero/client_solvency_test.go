package monero

import (
	"testing"

	"github.com/rs/zerolog/log"
	"go.uber.org/atomic"

	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/monero/sidecar"
	stypes "gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/config"
	ttypes "gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

func TestShouldReportSolvency(t *testing.T) {
	c := &Client{
		cfg:                     config.BifrostChainConfiguration{SolvencyBlocks: 10},
		scanner:                 &Scanner{lastFee: atomic.NewUint64(3)},
		lastSolvencyCheckHeight: atomic.NewInt64(0),
	}

	if c.ShouldReportSolvency(9) {
		t.Fatal("should not report on non-solvency block")
	}
	if !c.ShouldReportSolvency(10) {
		t.Fatal("should report on solvency block")
	}

	c.lastSolvencyCheckHeight.Store(10)
	if c.ShouldReportSolvency(10) {
		t.Fatal("should not report at same height twice")
	}

	c.scanner.lastFee.Store(0)
	if c.ShouldReportSolvency(20) {
		t.Fatal("should not report when fee is unknown")
	}
}

func TestShouldReportSolvencyRequiresInitializedState(t *testing.T) {
	c := &Client{
		cfg: config.BifrostChainConfiguration{SolvencyBlocks: 10},
	}
	if c.ShouldReportSolvency(10) {
		t.Fatal("should not report when scanner and solvency state are not initialized")
	}
}

func TestReportSolvencyAutoUnhaltUsesCheckedVaultCount(t *testing.T) {
	t.Parallel()

	vaultPubKey, vaultAddress := testVaultPubKeyAndAddress(t)
	balanceServer := newFrostBalanceServer(t, map[string]sidecar.Balance{
		vaultPubKey.String(): {
			Balance:          100 * moneroAtomicToThorchain,
			UnlockedBalance:  100 * moneroAtomicToThorchain,
			SpendableBalance: uint64Ptr(100 * moneroAtomicToThorchain),
		},
	})
	defer balanceServer.Close()

	frostSigner, err := sidecar.NewClientWithEndpoint(
		config.BifrostChainConfiguration{},
		balanceServer.URL,
	)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}

	queue := make(chan stypes.Solvency, 2)
	c := &Client{
		logger:                  log.Logger,
		cfg:                     config.BifrostChainConfiguration{SolvencyBlocks: 10},
		chain:                   common.XMRChain,
		thorchainBridge:         &solvencyBridge{asgards: ttypes.Vaults{{PubKey: vaultPubKey, PubKeyEddsa: vaultPubKey, PubKeyFrost: vaultPubKey, Coins: common.NewCoins(common.NewCoin(common.XMRAsset, cosmos.NewUint(100)))}, {PubKey: common.EmptyPubKey, PubKeyEddsa: common.EmptyPubKey, PubKeyFrost: common.EmptyPubKey}}},
		scanner:                 &Scanner{lastFee: atomic.NewUint64(10)},
		sidecar:                 frostSigner,
		globalSolvencyQueue:     queue,
		vaultAddressMap:         map[string]common.Address{vaultPubKey.String(): vaultAddress},
		lastSolvencyCheckHeight: atomic.NewInt64(0),
	}

	if err := c.ReportSolvency(10); err != nil {
		t.Fatalf("report solvency: %v", err)
	}
	if len(queue) != 1 {
		t.Fatalf("expected one solvent auto-unhalt message, got %d", len(queue))
	}
	msg := <-queue
	if !msg.PubKey.Equals(vaultPubKey) {
		t.Fatalf("expected solvency message for checked vault %s, got %s", vaultPubKey, msg.PubKey)
	}
}
