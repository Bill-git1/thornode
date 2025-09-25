package features

import (
	"fmt"

	"github.com/rs/zerolog/log"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	openapi "gitlab.com/thorchain/thornode/v3/openapi/gen"
	acommon "gitlab.com/thorchain/thornode/v3/test/simulation/actors/common"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/thornode"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"

	. "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// InactiveVaultRefunds
////////////////////////////////////////////////////////////////////////////////////////

func InactiveVaultRefunds() *Actor {
	a := NewActor("Feature-InactiveVaultRefunds")

	for _, asset := range acommon.SimChains {
		a.Children[NewInactiveVaultRefundActor(asset.GetGasAsset())] = true
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

func NewInactiveVaultRefundActor(asset common.Asset) *Actor {
	a := &InactiveVaultRefundActor{
		Actor: *NewActor(fmt.Sprintf("InactiveVaultRefund-%s", asset)),
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

	url := fmt.Sprintf("%s/thorchain/inbound_addresses?height=1", thornode.BaseURL()) // genesis vault
	var inboundAddresses []openapi.InboundAddress
	err := thornode.Get(url, &inboundAddresses)
	if err != nil {
		log.Error().Err(err).Msg("failed to get inbound addresses")
		return OpResult{
			Continue: false,
		}
	}

	// find address for chain
	var inboundAddr common.Address
	var inboundPubkey string
	for _, inboundAddress := range inboundAddresses {
		if *inboundAddress.Chain == string(a.asset.Chain) {
			inboundAddr = common.Address(*inboundAddress.Address)
			inboundPubkey = *inboundAddress.PubKey
			break
		}
	}

	vault, err := thornode.GetVault(inboundPubkey)
	if err != nil {
		log.Error().Err(err).Msg("failed to get vault")
		return OpResult{
			Continue: false,
		}
	}
	if vault.Status == types.VaultStatus_ActiveVault.String() {
		log.Error().Msgf("vault is active: %s", vault.Status)
		return OpResult{
			Continue: false,
		}
	}

	// create tx out
	tx := SimTx{
		Chain:     a.asset.Chain,
		ToAddress: inboundAddr,
		Coin:      common.NewCoin(a.asset, a.amount),
		Memo:      memo,
	}

	// sign transaction
	signed, err := client.SignTx(tx)
	if err != nil {
		log.Error().Err(err).Msg("failed to sign tx")
		return OpResult{
			Continue: false,
		}
	}

	// broadcast transaction
	txid, err := client.BroadcastTx(signed)
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
