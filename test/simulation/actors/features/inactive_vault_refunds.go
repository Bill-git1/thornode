package features

import (
	"fmt"
	rand "math/rand/v2"

	"github.com/rs/zerolog/log"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	acommon "gitlab.com/thorchain/thornode/v3/test/simulation/actors/common"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/thornode"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"

	. "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// InactiveVaultRefunds
////////////////////////////////////////////////////////////////////////////////////////

func InactiveVaultRefunds(rng *rand.Rand) *Actor {
	a := NewActor("Feature-InactiveVaultRefunds", rng)

	for _, chain := range acommon.SimChains {
		if chain.Equals(common.SOLChain) { // skip SOL as inactive vaults default disabled
			continue
		}
		a.Children[NewInactiveVaultRefundActor(chain.GetGasAsset(), rng)] = true
	}

	return a
}

////////////////////////////////////////////////////////////////////////////////////////
// InactiveVaultRefundActor
////////////////////////////////////////////////////////////////////////////////////////

type InactiveVaultRefundActor struct {
	Actor

	account     *User
	asset       common.Asset
	amount      cosmos.Uint
	inboundTxid string
}

func NewInactiveVaultRefundActor(asset common.Asset, rng *rand.Rand) *Actor {
	a := &InactiveVaultRefundActor{
		Actor: *NewActor(fmt.Sprintf("InactiveVaultRefund-%s", asset), rng),
		asset: asset,
	}

	a.SetLogger(a.Log().With().Str("asset", asset.String()).Logger())

	// lock a user that has sufficient L1 balance
	a.Ops = append(a.Ops, a.acquireUser)

	// send inbound to genesis vault
	a.Ops = append(a.Ops, a.sendInactiveVaultInbound)

	// verify refund outbound
	a.Ops = append(a.Ops, a.verifyRefund)

	return &a.Actor
}

////////////////////////////////////////////////////////////////////////////////////////
// Ops
////////////////////////////////////////////////////////////////////////////////////////

func (a *InactiveVaultRefundActor) acquireUser(config *OpConfig) OpResult {
	// determine the asset amount
	pool, err := thornode.GetPool(a.asset)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get pool")
		return OpResult{
			Continue: false,
		}
	}

	// amount is 1% of the pool asset depth
	a.amount = cosmos.NewUintFromString(pool.BalanceAsset).QuoUint64(100)

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
		if l1Acct.Coins.GetCoin(a.asset).Amount.LTE(a.amount) {
			a.Log().Error().Msg("user has insufficient L1 balance")
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

		// set acquired account and amounts in state context
		a.Log().Info().Stringer("l1Address", l1Address).Msg("acquired user")
		a.account = user

		break
	}

	// remain pending if no user is available
	return OpResult{
		Continue: a.account != nil,
	}
}

func (a *InactiveVaultRefundActor) sendInactiveVaultInbound(config *OpConfig) OpResult {
	memo := "TEST-INACTIVE-VAULT-REFUND"
	client := a.account.ChainClients[a.asset.Chain]

	inboundAddr, inboundPubkey, err := a.findInactiveVaultAddress()
	if err != nil {
		log.Error().Err(err).Msg("failed to get inactive vault")
		return OpResult{
			Continue: false,
		}
	}
	if inboundAddr.IsEmpty() || inboundPubkey == "" {
		a.Log().Info().Msg("no inactive vault available for chain, skipping")
		a.account.Release()
		return OpResult{
			Finish: true,
		}
	}

	// create tx out
	tx := SimTx{
		Chain:     a.asset.Chain,
		ToAddress: inboundAddr,
		Coin:      common.NewCoin(a.asset, a.amount),
		Memo:      memo,
	}

	txid, err := SignAndBroadcastTx(client, tx)
	if err != nil {
		log.Error().Err(err).Msg("failed to broadcast tx")
		return OpResult{
			Continue: false,
		}
	}
	a.Log().Info().Str("txid", txid).Msg("sent inbound to inactive vault")

	a.inboundTxid = txid
	return OpResult{
		Continue: true,
	}
}

func (a *InactiveVaultRefundActor) findInactiveVaultAddress() (common.Address, string, error) {
	vaults, err := thornode.GetVaults()
	if err != nil {
		return "", "", err
	}

	for _, vault := range vaults {
		if vault.Status != types.VaultStatus_InactiveVault.String() {
			continue
		}
		for _, address := range vault.Addresses {
			if address.Chain == a.asset.Chain.String() {
				return common.Address(address.Address), vault.GetPubKey(), nil
			}
		}
	}

	return "", "", nil
}

func (a *InactiveVaultRefundActor) verifyRefund(config *OpConfig) OpResult {
	// get swap stages
	stages, err := thornode.GetTxStages(a.inboundTxid)
	if err != nil {
		a.Log().Warn().Err(err).Msg("failed to get tx stages")
		return OpResult{
			Continue: false,
		}
	}

	// wait for outbound to be marked complete
	if stages.OutboundSigned == nil || !stages.OutboundSigned.Completed {
		return OpResult{
			Continue: false,
		}
	}

	// get tx details
	details, err := thornode.GetTxDetails(a.inboundTxid)
	if err != nil {
		a.Log().Warn().Err(err).Msg("failed to get tx details")
		return OpResult{
			Continue: false,
		}
	}

	// verify exactly one out transaction
	if len(details.OutTxs) != 1 {
		return OpResult{
			Error:  fmt.Errorf("expected exactly one out transaction"),
			Finish: true,
		}
	}

	// verify exactly one action
	if len(details.Actions) != 1 {
		return OpResult{
			Error:  fmt.Errorf("expected exactly one action"),
			Finish: true,
		}
	}

	// release user
	a.account.Release()

	return OpResult{
		Finish: true,
	}
}
