package features

import (
	"fmt"
	"sync/atomic"

	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/common/cosmos"
	"gitlab.com/thorchain/thornode/constants"
	"gitlab.com/thorchain/thornode/test/simulation/pkg/thornode"
	"gitlab.com/thorchain/thornode/x/thorchain/types"

	. "gitlab.com/thorchain/thornode/test/simulation/actors/common"
	. "gitlab.com/thorchain/thornode/test/simulation/pkg/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// SaverEject
////////////////////////////////////////////////////////////////////////////////////////

func SaverEject() *Actor {
	a := NewActor("Savers")

	// test saver eject actor for btc and eth
	pendingDeposits := &atomic.Int64{}
	pendingDeposits.Store(2)
	a.Children[NewSaverEjectActor(common.BTCAsset, pendingDeposits)] = true
	a.Children[NewSaverEjectActor(common.ETHAsset, pendingDeposits)] = true

	return a
}

////////////////////////////////////////////////////////////////////////////////////////
// SaverEjectActor
////////////////////////////////////////////////////////////////////////////////////////

type SaverEjectActor struct {
	Actor

	asset         common.Asset
	account       *User
	saverAddress  common.Address
	depositAmount cosmos.Uint
	poolDepthBps  uint64

	pendingDeposits *atomic.Int64 // ensure deposits complete before mimirs to eject
}

func NewSaverEjectActor(asset common.Asset, pendingDeposits *atomic.Int64) *Actor {
	a := &SaverEjectActor{
		Actor:           *NewActor(fmt.Sprintf("Feature-Saver-Eject-%s", asset)),
		asset:           asset,
		poolDepthBps:    2000,
		pendingDeposits: pendingDeposits,
	}

	// reset mimirs for test
	a.Ops = append(a.Ops, a.resetMimirs)

	// lock a user that has L1 and RUNE balance
	a.Ops = append(a.Ops, a.acquireUser)

	// deposit l1 balance
	a.Ops = append(a.Ops, a.depositL1)

	// ensure the saver deposit is successful
	a.Ops = append(a.Ops, a.verifySaverDeposit)

	// set mimirs for max synths and trigger eject
	a.Ops = append(a.Ops, a.setMimirs)

	// ensure the saver is ejected and release the account
	a.Ops = append(a.Ops, a.verifySaverEject)

	return &a.Actor
}

////////////////////////////////////////////////////////////////////////////////////////
// Ops
////////////////////////////////////////////////////////////////////////////////////////

func (a *SaverEjectActor) acquireUser(config *OpConfig) OpResult {
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
		if l1Acct.Coins.GetCoin(a.asset).Amount.LTE(assetAmount) {
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

func (a *SaverEjectActor) depositL1(config *OpConfig) OpResult {
	memo := fmt.Sprintf("+:%s", a.asset.GetSyntheticAsset())
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

func (a *SaverEjectActor) verifySaverDeposit(config *OpConfig) OpResult {
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
		a.pendingDeposits.Add(-1) // signal the saver deposit is complete
		return OpResult{
			Continue: true,
		}
	}

	// remain pending if no saver is available
	return OpResult{
		Continue: false,
	}
}

func (a *SaverEjectActor) resetMimirs(config *OpConfig) OpResult {
	if !a.setMimir(config, "MaxSynthPerPoolDepth", -1) || !a.setMimir(config, "SaversEjectInterval", -1) {
		return OpResult{
			Continue: false,
		}
	}
	return OpResult{
		Continue: true,
	}
}

func (a *SaverEjectActor) setMimirs(config *OpConfig) OpResult {
	// wait for the saver deposits to complete
	if a.pendingDeposits.Load() > 0 {
		return OpResult{
			Continue: false,
		}
	}

	if !a.setMimir(config, "MaxSynthPerPoolDepth", 500) || !a.setMimir(config, "SaversEjectInterval", 1) {
		return OpResult{
			Continue: false,
		}
	}
	return OpResult{
		Continue: true,
	}
}

func (a *SaverEjectActor) setMimir(config *OpConfig, key string, value int64) bool {
	// wait to acquire the admin user
	if !config.AdminUser.Acquire() {
		return false
	}
	defer config.AdminUser.Release()

	// get mimir
	mimirs, err := thornode.GetMimirs()
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get mimirs")
		return false
	}

	// skip if already set
	if mimir, ok := mimirs[key]; (ok && mimir == value) || (!ok && value == -1) {
		return true
	}

	// set mimirs to trigger eject
	accAddr, err := config.AdminUser.PubKey().GetThorAddress()
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get thor address")
		return false
	}
	mimir := types.NewMsgMimir(key, value, accAddr)
	txid, err := config.AdminUser.Thorchain.Broadcast(mimir)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to broadcast mimir")
		return false
	}

	a.Log().Info().
		Str("key", key).
		Int64("value", value).
		Str("txid", txid.String()).
		Msg("broadcasted mimir")

	return true
}

func (a *SaverEjectActor) verifySaverEject(config *OpConfig) OpResult {
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
		// remain pending the saver is found
		return OpResult{
			Finish: false,
		}
	}

	a.account.Release() // release the user
	return OpResult{
		Continue: true,
	}
}
