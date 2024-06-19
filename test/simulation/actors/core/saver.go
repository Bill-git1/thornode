package core

import (
	"fmt"

	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/common/cosmos"
	"gitlab.com/thorchain/thornode/constants"
	"gitlab.com/thorchain/thornode/test/simulation/pkg/thornode"
	"golang.org/x/exp/rand"

	. "gitlab.com/thorchain/thornode/test/simulation/actors/common"
	. "gitlab.com/thorchain/thornode/test/simulation/pkg/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// SaverActor
////////////////////////////////////////////////////////////////////////////////////////

type SaverActor struct {
	Actor

	asset         common.Asset
	account       *User
	saverAddress  common.Address
	depositAmount cosmos.Uint
	poolDepthBps  uint64

	// expected range for received amount, including outbound fee
	minExpected cosmos.Uint
	maxExpected cosmos.Uint
}

func NewSaverActor(asset common.Asset, poolDepthBps uint64) *Actor {
	a := &SaverActor{
		Actor:        *NewActor(fmt.Sprintf("Saver-%s", asset)),
		asset:        asset,
		poolDepthBps: poolDepthBps,
	}

	// lock a user that has L1 balance
	a.Ops = append(a.Ops, a.acquireUser)

	// generate deposit quote
	a.Ops = append(a.Ops, a.getQuote)

	// deposit L1 saver
	if asset.Chain.IsEVM() && !asset.IsGasAsset() {
		a.Ops = append(a.Ops, a.depositL1Token)
	} else {
		a.Ops = append(a.Ops, a.depositL1)
	}

	// ensure the saver is created and release the account
	a.Ops = append(a.Ops, a.verifySaver)

	return &a.Actor
}

////////////////////////////////////////////////////////////////////////////////////////
// Ops
////////////////////////////////////////////////////////////////////////////////////////

func (a *SaverActor) acquireUser(config *OpConfig) OpResult {
	// determine the asset amount
	pool, err := thornode.GetPool(a.asset)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get pool")
		return OpResult{
			Continue: false,
		}
	}
	assetAmount := cosmos.NewUintFromString(pool.BalanceAsset).
		MulUint64(a.poolDepthBps).
		QuoUint64(constants.MaxBasisPts)
	a.depositAmount = assetAmount

	for _, user := range config.Users {
		a.SetLogger(a.Log().With().Str("user", user.Name()).Logger())

		// skip users already being used
		if !user.Acquire() {
			continue
		}

		// skip users that with insufficient L1 balance
		l1Acct, err := user.ChainClients[a.asset.Chain].GetAccount(nil)
		if err != nil {
			a.Log().Error().Err(err).Msg("failed to get L1 account")
			user.Release()
			continue
		}
		if l1Acct.Coins.GetCoin(a.asset).Amount.LT(assetAmount) {
			a.Log().Error().Msg("user has insufficient L1 balance")
			user.Release()
			continue
		}

		// get l1 address to store in state context
		l1Address, err := user.PubKey().GetAddress(a.asset.Chain)
		if err != nil {
			a.Log().Error().Err(err).Msg("failed to get L1 address")
			user.Release()
			continue
		}

		// set acquired account and amounts in state context
		a.Log().Info().Stringer("l1Address", l1Address).Msg("acquired user")
		a.saverAddress = l1Address
		a.account = user

		break
	}

	// remain pending if no user is available
	return OpResult{
		Continue: a.account != nil,
	}
}

func (a *SaverActor) getQuote(_ *OpConfig) OpResult {
	quote, err := thornode.GetSaverDepositQuote(a.asset, a.depositAmount)
	if err != nil {
		a.Log().Error().Err(err).Str("amount", a.depositAmount.String()).Msg("failed to get deposit quote")
		return OpResult{
			Continue: false,
		}
	}

	// store expected range to fail if received amount is outside 5% tolerance
	quoteOut := cosmos.NewUintFromString(quote.ExpectedAmountDeposit)
	tolerance := quoteOut.QuoUint64(20)
	if quote.Fees.Outbound != nil {
		outboundFee := cosmos.NewUintFromString(*quote.Fees.Outbound)
		quoteOut = quoteOut.Add(outboundFee)
	}
	a.minExpected = quoteOut.Sub(tolerance)
	a.maxExpected = quoteOut.Add(tolerance)

	return OpResult{
		Continue: true,
	}
}

func (a *SaverActor) depositL1Token(config *OpConfig) OpResult {
	memo := fmt.Sprintf("+:%s", a.asset.GetSyntheticAsset())
	client := a.account.ChainClients[a.asset.Chain]
	txid, err := DepositL1Token(a.Log(), client, a.asset, memo, a.depositAmount)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to deposit L1 token")
		return OpResult{
			Continue: false,
		}
	}

	a.Log().Info().Str("txid", txid).Msg("broadcasted token saver deposit")
	return OpResult{
		Continue: true,
	}
}

func (a *SaverActor) depositL1(config *OpConfig) OpResult {
	// send random half as memoless savers
	memo := ""
	if rand.Intn(2) == 0 {
		memo = fmt.Sprintf("+:%s", a.asset.GetSyntheticAsset())
	}

	client := a.account.ChainClients[a.asset.Chain]
	txid, err := DepositL1(a.Log(), client, a.asset, memo, a.depositAmount)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to deposit L1")
		return OpResult{
			Continue: false,
		}
	}

	a.Log().Info().Str("txid", txid).Msg("broadcasted saver deposit")
	return OpResult{
		Continue: true,
	}
}

func (a *SaverActor) verifySaver(config *OpConfig) OpResult {
	savers, err := thornode.GetSavers(a.asset)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get savers")
		return OpResult{
			Continue: false,
		}
	}

	for _, saver := range savers {
		if saver.AssetAddress != a.saverAddress.String() {
			continue
		}
		res := OpResult{
			Finish: true,
		}

		deposit := cosmos.NewUintFromString(saver.AssetDepositValue)
		a.Log().Info().
			Stringer("deposit", deposit).
			Stringer("minExpected", a.minExpected).
			Stringer("maxExpected", a.maxExpected).
			Msg("deposit complete")

		// verify the amounts, only slip in swap to synth lost
		if deposit.LT(a.minExpected) || deposit.GT(a.maxExpected) {
			err = fmt.Errorf("out amount plus gas outside tolerance")
			res.Error = err
			a.Log().Error().Err(res.Error).Msg("failed saver deposit")
		}

		a.account.Release() // release the user
		return res
	}

	// remain pending if no saver is available
	return OpResult{
		Continue: false,
	}
}
