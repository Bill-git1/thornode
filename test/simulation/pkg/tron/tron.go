package tron

import (
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"cosmossdk.io/math"
	"github.com/ethereum/go-ethereum/crypto"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/shared/evm"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/tron/api"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient"
	"gitlab.com/thorchain/thornode/v3/common"

	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"

	. "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// Tron
////////////////////////////////////////////////////////////////////////////////////////

type Client struct {
	chain   common.Chain
	api     *api.TronApi
	keys    *thorclient.Keys
	privKey *ecdsa.PrivateKey
	address common.Address
}

var _ LiteChainClient = &Client{}

func NewConstructor(host string) LiteChainClientConstructor {
	return func(chain common.Chain, keys *thorclient.Keys) (LiteChainClient, error) {
		return NewClient(chain, host, keys)
	}
}

func NewClient(
	chain common.Chain,
	host string,
	keys *thorclient.Keys,
) (LiteChainClient, error) {
	// extract the private key
	privKey, err := keys.GetPrivateKey()
	if err != nil {
		return nil, fmt.Errorf("fail to get private key: %w", err)
	}

	// derive the public key
	pk, err := cryptocodec.ToCmtPubKeyInterface(privKey.PubKey())
	if err != nil {
		return nil, fmt.Errorf("fail to get tm pub key: %w", err)
	}
	pubKey, err := common.NewPubKeyFromCrypto(pk)
	if err != nil {
		return nil, fmt.Errorf("fail to get pub key: %w", err)
	}

	// get pubkey address for the chain
	address, err := pubKey.GetAddress(chain)
	if err != nil {
		return nil, fmt.Errorf("fail to get address from pubkey(%s): %w", pk, err)
	}

	client := Client{
		chain:   chain,
		api:     api.NewTronApi(host, time.Second*2),
		keys:    keys,
		address: address,
	}

	priv, err := keys.GetPrivateKey()
	if err != nil {
		return nil, err
	}

	client.privKey, err = evm.GetPrivateKey(priv)
	if err != nil {
		return nil, err
	}

	return &client, nil
}

func (c *Client) GetAccount(pk *common.PubKey) (*common.Account, error) {
	address := c.address

	if pk != nil {
		var err error
		address, err = pk.GetAddress(c.chain)
		if err != nil {
			return nil, fmt.Errorf("fail to get address from pubkey(%s): %w", pk, err)
		}
	}

	balance, err := c.api.GetBalance(address.String())
	if err != nil {
		return nil, err
	}

	amount := math.NewUint(balance).Mul(math.NewUint(100)) // 1e6 -> 1e8
	coins := []common.Coin{
		common.NewCoin(common.TRXAsset, amount),
	}

	return &common.Account{
		Coins: coins,
	}, nil
}

func (c *Client) SignContractTx(_ SimContractTx) ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}

func (c *Client) SignTx(tx SimTx) ([]byte, error) {
	tronTx, err := c.api.CreateTransaction(
		c.address.String(),
		tx.ToAddress.String(),
		tx.Coin.Amount.Quo(math.NewUint(100)).Uint64(),
		tx.Memo,
	)
	if err != nil {
		return nil, err
	}

	hash, err := hex.DecodeString(tronTx.TxId)
	if err != nil {
		return nil, err
	}

	signature, err := crypto.Sign(hash, c.privKey)
	if err != nil {
		return nil, err
	}

	tronTx.Signature = append(tronTx.Signature, hex.EncodeToString(signature))

	txBytes, err := json.Marshal(tronTx)
	if err != nil {
		return nil, err
	}

	return txBytes, nil
}

func (c *Client) BroadcastTx(signed []byte) (string, error) {
	response, err := c.api.BroadcastTransaction(signed)
	if err != nil {
		return "", err
	}

	return response.TxId, nil
}
