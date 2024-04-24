package types

import (
	"strings"

	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/rs/zerolog/log"
	"gitlab.com/thorchain/thornode/bifrost/thorclient"
	"gitlab.com/thorchain/thornode/cmd"
	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/common/cosmos"
	"gitlab.com/thorchain/thornode/config"
)

////////////////////////////////////////////////////////////////////////////////////////
// Account
////////////////////////////////////////////////////////////////////////////////////////

// Account holds a set of chain clients configured with a given private key.
type Account struct {
	// Thorchain is the thorchain client for the account.
	Thorchain thorclient.ThorchainBridge

	// ChainClients is a map of chain to the corresponding client for the account.
	ChainClients map[common.Chain]LiteChainClient

	lock     chan struct{}
	pubkey   common.PubKey
	mnemonic string
}

// NewAccount returns a new client using the private key from the given mnemonic.
func NewAccount(mnemonic string, constructors map[common.Chain]LiteChainClientConstructor) *Account {
	// create pubkey for mnemonic
	derivedPriv, err := hd.Secp256k1.Derive()(mnemonic, "", cmd.THORChainHDPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to derive private key")
	}
	privKey := hd.Secp256k1.Generate()(derivedPriv)
	s, err := cosmos.Bech32ifyPubKey(cosmos.Bech32PubKeyTypeAccPub, privKey.PubKey())
	if err != nil {
		log.Fatal().Err(err).Msg("failed to bech32ify pubkey")
	}
	pubkey := common.PubKey(s)

	// add key to keyring
	kr := keyring.NewInMemory()
	name := strings.Split(mnemonic, " ")[0]
	_, err = kr.NewAccount(name, mnemonic, "", cmd.THORChainHDPath, hd.Secp256k1)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to add account to keyring")
	}

	// create thorclient.Keys for chain client construction
	keys := thorclient.NewKeysWithKeybase(kr, name, "")

	// bifrost config for chain client construction
	cfg := config.GetBifrost()

	// create chain clients
	chainClients := make(map[common.Chain]LiteChainClient)
	for _, chain := range common.AllChains {
		// skip thorchain and deprecated chains
		switch chain {
		case common.THORChain, common.BNBChain, common.TERRAChain:
			continue
		}

		chainClients[chain], err = constructors[chain](chain, keys)
		if err != nil {
			log.Fatal().Err(err).Stringer("chain", chain).Msg("failed to create chain client")
		}
	}

	// create thorchain bridge
	thorchainCfg := cfg.Thorchain
	thorchainCfg.SignerName = name
	thorchain, err := thorclient.NewThorchainBridge(thorchainCfg, nil, keys)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create thorchain client")
	}

	return &Account{
		ChainClients: chainClients,
		Thorchain:    thorchain,
		lock:         make(chan struct{}, 1),
		pubkey:       pubkey,
		mnemonic:     mnemonic,
	}
}

// Name returns the name of the account.
func (a *Account) Name() string {
	return strings.Split(a.mnemonic, " ")[0]
}

// Acquire will attempt to acquire the lock. If the lock is already acquired, it will
// return false. If true is returned, the caller has locked and must release when done.
func (a *Account) Acquire() bool {
	select {
	case a.lock <- struct{}{}:
		return true
	default:
		return false
	}
}

// Release will release the lock.
func (a *Account) Release() {
	<-a.lock
}

// PubKey returns the public key of the client.
func (a *Account) PubKey() common.PubKey {
	return a.pubkey
}
