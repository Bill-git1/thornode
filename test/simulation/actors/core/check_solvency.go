package core

import (
	"fmt"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/evm"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/thornode"

	. "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// SolvencyCheckActo
////////////////////////////////////////////////////////////////////////////////////////

type SolvencyCheckActor struct {
	Actor
}

func NewSolvencyCheckActor() *Actor {
	a := &DualLPActor{
		Actor: *NewActor("SolvencyCheck"),
	}

	a.Ops = append(a.Ops, a.checkSolvency)

	return &a.Actor
}

////////////////////////////////////////////////////////////////////////////////////////
// Ops
////////////////////////////////////////////////////////////////////////////////////////

func (a *DualLPActor) checkSolvency(config *OpConfig) OpResult {
	// get all vaults
	vaults, err := thornode.GetVaults()
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to get vaults")
		return OpResult{
			Continue: false,
		}
	}
	vaultCoinAmounts := make(map[string]cosmos.Uint)
	for _, vault := range vaults {
		for _, coin := range vault.Coins {
			if _, ok := vaultCoinAmounts[coin.Asset]; !ok {
				vaultCoinAmounts[coin.Asset] = cosmos.ZeroUint()
			}
			vaultCoinAmounts[coin.Asset] = vaultCoinAmounts[coin.Asset].Add(cosmos.NewUintFromString(coin.Amount))
		}
	}

	// assert only one vault
	if len(vaults) != 1 {
		a.Log().Error().Msg("expected only one vault")
		return OpResult{
			Continue: false,
		}
	}
	pubkey, err := common.NewPubKey(*vaults[0].PubKey)
	if err != nil {
		a.Log().Error().Err(err).Msg("failed to parse vault pubkey")
		return OpResult{
			Continue: false,
		}
	}

	// find a user to lookup L1 balances
	var user *User
	for _, user = range config.Users {
		a.SetLogger(a.Log().With().Str("user", user.Name()).Logger())

		// skip users already being used
		if user.Acquire() {
			break
		}
	}
	if user == nil {
		a.Log().Error().Msg("no user available")
		return OpResult{
			Continue: false,
		}
	}

	// get all L1 balances
	l1CoinAmounts := make(map[string]cosmos.Uint)
	for assetStr := range vaultCoinAmounts {
		asset, err := common.NewAsset(assetStr)
		if err != nil {
			a.Log().Fatal().Err(err).Msg("failed to parse asset")
		}

		if asset.IsGasAsset() {
			l1Acct, err := user.ChainClients[asset.Chain].GetAccount(&pubkey)
			if err != nil {
				a.Log().Error().Err(err).Msg("failed to get L1 account")
				return OpResult{
					Continue: false,
				}
			}
			l1CoinAmounts[assetStr] = l1Acct.Coins.GetCoin(asset).Amount
		} else if asset.Chain.IsEVM() {
			// for EVM chains, we need to get the balance from the contract
			_, routerAddr, err := thornode.GetInboundAddress(asset.Chain)
			if err != nil {
				a.Log().Error().Err(err).Msg("failed to get router address")
				return OpResult{
					Continue: false,
				}
			}
			vaultAddr, err := pubkey.GetAddress(asset.Chain)
			if err != nil {
				a.Log().Error().Err(err).Msg("failed to get vault address")
				return OpResult{
					Continue: false,
				}
			}
			balance, err := user.ChainClients[asset.Chain].(*evm.Client).GetVaultAllowance(*routerAddr, vaultAddr, asset)
			if err != nil {
				a.Log().Error().Err(err).Msg("failed to get vault allowance")
				return OpResult{
					Continue: false,
				}
			}
			l1CoinAmounts[assetStr] = balance
		} else {
			a.Log().Error().Msgf("unsupported asset %s", asset)
			return OpResult{
				Continue: false,
				Error:    fmt.Errorf("unsupported asset %s", asset),
			}
		}
	}

	// verify vault and on chain amount are equal for all assets
	vaultL1Mismatch := false
	for asset, vaultAmount := range vaultCoinAmounts {
		l1Amount, ok := l1CoinAmounts[asset]
		if !ok {
			a.Log().Error().Str("asset", asset).Msg("L1 amount not found for asset")
		}
		if !vaultAmount.Equal(l1Amount) {
			a.Log().Error().
				Str("asset", asset).
				Str("vault_amount", vaultAmount.String()).
				Str("l1_amount", l1Amount.String()).
				Msg("vault and L1 amounts do not match")
			vaultL1Mismatch = true
		}
	}
	if vaultL1Mismatch {
		return OpResult{
			Continue: false,
			Error:    fmt.Errorf("vault and L1 amounts do not match"),
			Finish:   true,
		}
	}

	return OpResult{
		Continue: true,
	}
}
