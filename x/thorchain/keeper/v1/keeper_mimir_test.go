package keeperv1

import (
	"gitlab.com/thorchain/thornode/v3/constants"

	. "gopkg.in/check.v1"
)

type KeeperMimirSuite struct{}

var _ = Suite(&KeeperMimirSuite{})

func (s *KeeperMimirSuite) TestMimir(c *C) {
	ctx, k := setupKeeperForTest(c)

	k.SetMimir(ctx, "foo", 14)

	val, err := k.GetMimir(ctx, "foo")
	c.Assert(err, IsNil)
	c.Assert(val, Equals, int64(14))

	val, err = k.GetMimir(ctx, "bogus")
	c.Assert(err, IsNil)
	c.Check(val, Equals, int64(-1))
}

func (s *KeeperMimirSuite) TestIsOperationalMimirPOLReserveBlackList(c *C) {
	_, k := setupKeeperForTest(c)

	// Per-pool POL reserve blacklist must be operational so operators can
	// quickly disable a misbehaving pool without full governance consensus.
	c.Check(k.IsOperationalMimir("POLReserveBlacklist-BTC-BTC"), Equals, true)
	c.Check(
		k.IsOperationalMimir("POLReserveBlacklist-ETH-USDC-0XA0B86991C6218B36C1D19D4A2E9EB0CE3606EB48"),
		Equals, true,
	)

	// The partial match is case-insensitive (IsOperationalMimir uppercases the
	// key before comparing), so operator-set keys in any case still register.
	c.Check(k.IsOperationalMimir("POLRESERVEBLACKLIST-BTC-BTC"), Equals, true)
	c.Check(k.IsOperationalMimir("polreserveblacklist-btc-btc"), Equals, true)

	// Whitelist is NOT operational — adding new assets to POL Reserve is an
	// economic decision requiring full admin mimir consensus.
	c.Check(k.IsOperationalMimir("POLReserveWhitelist-BTC-BTC"), Equals, false)
	c.Check(k.IsOperationalMimir("polreservewhitelist-btc-btc"), Equals, false)

	// Similarly-prefixed but unrelated keys must NOT be flagged operational.
	c.Check(k.IsOperationalMimir("POLReserveFoo-BTC-BTC"), Equals, false)
	c.Check(k.IsOperationalMimir("POLReserveDeposit-BTC-BTC"), Equals, false)
}

func (s *KeeperMimirSuite) TestIsOperationalMimirCompromisedVault(c *C) {
	_, k := setupKeeperForTest(c)

	c.Check(k.IsOperationalMimir("CompromisedVault-tthorpub1addwnpepq0"), Equals, true)
	c.Check(k.IsOperationalMimir("COMPROMISEDVAULT-TTHORPUB1ADDWNPEPQ0"), Equals, true)
	c.Check(k.IsOperationalMimir("compromisedvault-tthorpub1addwnpepq0"), Equals, true)
	c.Check(k.IsOperationalMimir("CompromisedVault"), Equals, false)
}

func (s *KeeperMimirSuite) TestIsOperationalMimirOverSolvencyToTreasuryBps(c *C) {
	_, k := setupKeeperForTest(c)

	c.Check(k.IsOperationalMimir("OverSolvencyToTreasuryBps-BTC-BTC"), Equals, true)
	c.Check(
		k.IsOperationalMimir("OverSolvencyToTreasuryBps-ETH-USDC-0XA0B86991C6218B36C1D19D4A2E9EB0CE3606EB48"),
		Equals, true,
	)
	c.Check(k.IsOperationalMimir("OVERSOLVENCYTOTREASURYBPS-ETH-ETH"), Equals, true)
	c.Check(k.IsOperationalMimir("oversolvencytotreasurybps-btc-btc"), Equals, true)
	c.Check(k.IsOperationalMimir("OverSolvencyToTreasuryBps"), Equals, true)
	c.Check(k.IsOperationalMimir("OverSolvencyToTreasuryFoo-BTC-BTC"), Equals, false)
}

func (s *KeeperMimirSuite) TestDisableTSSSignerFilteringIsEconomicMimir(c *C) {
	_, k := setupKeeperForTest(c)

	c.Check(k.IsOperationalMimir(constants.MimirKeyDisableTSSSignerFiltering), Equals, false)
}
