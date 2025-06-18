package types

import (
	"strings"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/rs/zerolog/log"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient"
	"gitlab.com/thorchain/thornode/v3/cmd"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/common/crypto/ed25519"
	"gitlab.com/thorchain/thornode/v3/config"
)

////////////////////////////////////////////////////////////////////////////////////////
// Account
////////////////////////////////////////////////////////////////////////////////////////

// User holds a set of chain clients configured with a given private key.
type User struct {
	// Thorchain is the thorchain client for the account.
	Thorchain thorclient.ThorchainBridge

	// ChainClients is a map of chain to the corresponding client for the account.
	ChainClients map[common.Chain]LiteChainClient

	lock        chan struct{}
	pubkey      common.PubKey
	eddsaPubkey common.PubKey
	mnemonic    string
}

// NewUser returns a new client using the private key from the given mnemonic.
func NewUser(mnemonic string, constructors map[common.Chain]LiteChainClientConstructor) *User {
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

	// EdDSA pubkey
	edPriv, err := ed25519.Ed25519.Derive()(mnemonic, "", ed25519.HDPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to derive ed25519 private key")
	}
	edPrivKey := ed25519.Ed25519.Generate()(edPriv)
	edS, err := cosmos.Bech32ifyPubKey(cosmos.Bech32PubKeyTypeAccPub, edPrivKey.PubKey())
	if err != nil {
		log.Fatal().Err(err).Msg("failed to bech32ify eddsa pubkey")
	}
	edPubkey := common.PubKey(edS)

	// add key to keyring
	registry := codectypes.NewInterfaceRegistry()
	cryptocodec.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)
	kr := keyring.NewInMemory(cdc, func(options *keyring.Options) {
		options.SupportedAlgos = keyring.SigningAlgoList{hd.Secp256k1, ed25519.Ed25519}
	})
	name := strings.Split(mnemonic, " ")[0]
	_, err = kr.NewAccount(name, mnemonic, "", cmd.THORChainHDPath, hd.Secp256k1)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to add account to keyring")
	}

	// Add Ed25519 key to keyring
	_, err = kr.NewAccount(ed25519.SignerNameEDDSA(name), mnemonic, "", ed25519.HDPath, ed25519.Ed25519)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to add account to keyring")
	}

	// create thorclient.Keys for chain client construction
	keys := thorclient.NewKeysWithKeybase(kr, name, "")

	// bifrost config for chain client construction
	cfg := config.GetBifrost()

	// create chain clients
	chainClients := make(map[common.Chain]LiteChainClient)
	for chain := range constructors {
		pKeys := keys
		chainClients[chain], err = constructors[chain](chain, pKeys)
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

	return &User{
		ChainClients: chainClients,
		Thorchain:    thorchain,
		lock:         make(chan struct{}, 1),
		pubkey:       pubkey,
		eddsaPubkey:  edPubkey,
		mnemonic:     mnemonic,
	}
}

// Name returns the name of the account.
func (u *User) Name() string {
	return strings.Split(u.mnemonic, " ")[0]
}

// Acquire will attempt to acquire the lock. If the lock is already acquired, it will
// return false. If true is returned, the caller has locked and must release when done.
func (u *User) Acquire() bool {
	select {
	case u.lock <- struct{}{}:
		return true
	default:
		return false
	}
}

// IsLocked will return true if the lock is already acquired.
func (u *User) IsLocked() bool {
	select {
	case u.lock <- struct{}{}:
		<-u.lock
		return false
	default:
		return true
	}
}

// Release will release the lock.
func (u *User) Release() {
	<-u.lock
}

// PubKey returns the public key of the client.
func (u *User) PubKey(chain common.Chain) common.PubKey {
	if chain.GetSigningAlgo() == common.SigningAlgoEd25519 {
		return u.eddsaPubkey
	}
	return u.pubkey
}

// Address returns the address of the client for the given chain.
func (u *User) Address(chain common.Chain) common.Address {
	if chain.GetSigningAlgo() == common.SigningAlgoEd25519 {
		address, err := u.eddsaPubkey.GetAddress(chain)
		if err != nil {
			log.Fatal().Err(err).Stringer("chain", chain).Msg("failed to get address")
		}
		return address
	}
	address, err := u.pubkey.GetAddress(chain)
	if err != nil {
		log.Fatal().Err(err).Stringer("chain", chain).Msg("failed to get address")
	}
	return address
}
