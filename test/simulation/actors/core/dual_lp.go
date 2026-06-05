package core

import (
	"fmt"
	rand "math/rand/v2"
	"time"

	openapi "gitlab.com/thorchain/thornode/v3/openapi/gen"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/thornode"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"

	. "gitlab.com/thorchain/thornode/v3/test/simulation/actors/common"
	. "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// DualLPActor
////////////////////////////////////////////////////////////////////////////////////////

type DualLPActor struct {
	Actor

	asset            common.Asset
	account          *User
	thorAddress      common.Address
	l1Address        common.Address
	runeAmount       cosmos.Uint
	l1Amount         cosmos.Uint
	minLastAddHeight int64
}

func NewDualLPActor(asset common.Asset, rng *rand.Rand) *Actor {
	a := &DualLPActor{
		Actor: *NewActor(fmt.Sprintf("DualLP-%s", asset), rng),
		asset: asset,
	}
	a.Timeout = dualLPActorTimeout(asset)

	// lock a user that has L1 and RUNE balance
	a.Ops = append(a.Ops, a.acquireUser)

	// deposit 10% of the user RUNE balance
	a.Ops = append(a.Ops, a.depositRune)

	// deposit 10% of the user L1 balance to match
	if asset.Chain.IsEVM() && !asset.IsGasAsset() {
		a.Ops = append(a.Ops, a.depositL1Token)
	} else {
		a.Ops = append(a.Ops, a.depositL1)
	}

	// ensure the lp is created and release the account
	a.Ops = append(a.Ops, a.verifyLP)

	return &a.Actor
}

func dualLPActorTimeout(asset common.Asset) time.Duration {
	if asset.Chain.Equals(common.XMRChain) || asset.Chain.Equals(common.TRONChain) {
		return 8 * time.Minute
	}
	return 2 * time.Minute
}

////////////////////////////////////////////////////////////////////////////////////////
// Ops
////////////////////////////////////////////////////////////////////////////////////////

func (a *DualLPActor) acquireUser(config *OpConfig) OpResult {
	userMaxRune := cosmos.NewUint(0)
	existingLPs, err := thornode.GetLiquidityProviders(a.asset)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get liquidity providers")
		return OpResult{
			Continue: false,
		}
	}

	for _, user := range config.Users {
		a.SetLogger(a.Log().With().Str("user", user.Name()).Logger())

		// skip users already being used
		if !user.Acquire() {
			continue
		}

		// skip users that don't have RUNE balance
		thorAddress, err := user.PubKey(common.THORChain).GetAddress(common.THORChain)
		if err != nil {
			a.Log().Error().Err(err).Msg("failed to get thor address")
			user.Release()
			continue
		}
		thorBalances, err := thornode.GetBalances(thorAddress)
		if err != nil {
			a.Log().Error().Err(err).Msg("failed to get thorchain balances")
			user.Release()
			continue
		}
		if thorBalances.GetCoin(common.RuneAsset()).Amount.IsZero() {
			a.Log().Error().Msg("user has no RUNE balance")
			user.Release()
			continue
		}

		// skip users that don't have L1 balance
		l1Acct, err := user.ChainClients[a.asset.Chain].GetAccount(nil)
		if err != nil {
			a.Log().Error().Err(err).Msg("failed to get L1 account")
			user.Release()
			continue
		}
		if l1Acct.Coins.GetCoin(a.asset).Amount.IsZero() {
			a.Log().Error().Msg("user has no L1 balance")
			user.Release()
			continue
		}

		// get l1 address to store in state context
		l1Address, err := user.PubKey(a.asset.Chain).GetAddress(a.asset.Chain)
		if err != nil {
			a.Log().Error().Err(err).Msg("failed to get L1 address")
			user.Release()
			continue
		}
		if a.userHasLPPosition(existingLPs, thorAddress, l1Address) {
			a.Log().Info().
				Stringer("address", thorAddress).
				Stringer("l1Address", l1Address).
				Msg("user already has LP position, skipping")
			user.Release()
			continue
		}

		// find the user with the most RUNE balance
		if thorBalances.GetCoin(common.RuneAsset()).Amount.LTE(userMaxRune) {
			user.Release()
			continue
		}
		userMaxRune = thorBalances.GetCoin(common.RuneAsset()).Amount

		// release the previous candidate before overwriting
		if a.account != nil {
			a.account.Release()
		}

		// set acquired account and amounts in state context
		a.Log().Info().
			Stringer("address", thorAddress).
			Stringer("l1Address", l1Address).
			Msg("acquired user")
		a.thorAddress = thorAddress
		a.l1Address = l1Address
		a.runeAmount = thorBalances.GetCoin(common.RuneAsset()).Amount.QuoUint64(4)
		a.l1Amount = l1Acct.Coins.GetCoin(a.asset).Amount.QuoUint64(4)
		a.account = user
	}

	if a.account != nil {
		a.SetLogger(a.Log().With().Str("user", a.account.Name()).Logger())
		block, err := thornode.GetBlock(0)
		if err != nil {
			a.Log().Warn().Err(err).Msg("failed to get current block height")
		} else {
			a.minLastAddHeight = block.Header.Height
		}
	}

	// remain pending if no user is available
	return OpResult{
		Continue: a.account != nil,
	}
}

func (a *DualLPActor) userHasLPPosition(lps []openapi.LiquidityProvider, thorAddress, l1Address common.Address) bool {
	for _, lp := range lps {
		if lp.RuneAddress == nil || lp.AssetAddress == nil {
			continue
		}
		if common.Address(*lp.RuneAddress).Equals(thorAddress) &&
			common.Address(*lp.AssetAddress).Equals(l1Address) {
			return true
		}
	}
	return false
}

func (a *DualLPActor) depositL1Token(config *OpConfig) OpResult {
	memo := fmt.Sprintf("+:%s:%s", a.asset, a.thorAddress)
	client := a.account.ChainClients[a.asset.Chain]
	txid, err := DepositL1Token(a.Log(), client, a.asset, memo, a.l1Amount)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to deposit L1 token")
		return OpResult{
			Continue: false,
		}
	}

	a.Log().Info().Str("txid", txid).Msg("broadcasted token add liquidity tx")
	return OpResult{
		Continue: true,
	}
}

func (a *DualLPActor) depositL1(config *OpConfig) OpResult {
	memo := fmt.Sprintf("+:%s:%s", a.asset, a.thorAddress)
	client := a.account.ChainClients[a.asset.Chain]
	txid, err := DepositL1(a.Log(), client, a.asset, memo, a.l1Amount)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to deposit L1")
		return OpResult{
			Continue: false,
		}
	}

	a.Log().Info().Str("txid", txid).Msg("broadcasted L1 add liquidity tx")
	return OpResult{
		Continue: true,
	}
}

func (a *DualLPActor) depositRune(config *OpConfig) OpResult {
	memo := fmt.Sprintf("+:%s:%s", a.asset, a.l1Address)
	accAddr, err := a.account.PubKey(common.THORChain).GetThorAddress()
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get thor address")
		return OpResult{
			Continue: false,
		}
	}
	deposit := types.NewMsgDeposit(
		common.NewCoins(common.NewCoin(common.RuneAsset(), a.runeAmount)),
		memo,
		accAddr,
	)
	txid, err := a.account.Thorchain.Broadcast(deposit)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to broadcast tx")
		return OpResult{
			Continue: false,
		}
	}

	a.Log().Info().Stringer("txid", txid).Msg("broadcasted RUNE add liquidity tx")
	return OpResult{
		Continue: true,
	}
}

func (a *DualLPActor) verifyLP(config *OpConfig) OpResult {
	lps, err := thornode.GetLiquidityProviders(a.asset)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get liquidity providers")
		return OpResult{
			Continue: false,
		}
	}

	lp := findCompletedLP(lps, a.thorAddress, a.l1Address)
	if lp == nil {
		return OpResult{
			Continue: false,
		}
	}
	if a.minLastAddHeight > 0 {
		if lp.LastAddHeight == nil {
			a.Log().Info().Msg("waiting for LP last_add_height")
			return OpResult{
				Continue: false,
			}
		}
		if *lp.LastAddHeight <= a.minLastAddHeight {
			a.Log().Info().
				Int64("last_add_height", *lp.LastAddHeight).
				Int64("min_last_add_height", a.minLastAddHeight).
				Msg("waiting for LP position to update")
			return OpResult{
				Continue: false,
			}
		}
	}

	a.Log().Info().
		Str("units", lp.Units).
		Str("rune_deposit_value", lp.RuneDepositValue).
		Str("asset_deposit_value", lp.AssetDepositValue).
		Msg("verified liquidity provider")
	a.account.Release()
	return OpResult{
		Finish: true,
	}
}
