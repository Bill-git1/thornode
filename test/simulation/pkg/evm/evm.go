package evm

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"strings"

	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	ecommon "github.com/ethereum/go-ethereum/common"
	etypes "github.com/ethereum/go-ethereum/core/types"
	ecrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"gitlab.com/thorchain/thornode/bifrost/pkg/chainclients/shared/evm"

	"gitlab.com/thorchain/thornode/bifrost/thorclient"
	"gitlab.com/thorchain/thornode/common"

	. "gitlab.com/thorchain/thornode/test/simulation/pkg/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// Helpers
////////////////////////////////////////////////////////////////////////////////////////

func ctx() context.Context {
	return context.Background()
}

////////////////////////////////////////////////////////////////////////////////////////
// EVM
////////////////////////////////////////////////////////////////////////////////////////

type Client struct {
	chain common.Chain
	rpc   *ethclient.Client

	keys    *thorclient.Keys
	privKey *ecdsa.PrivateKey
	signer  etypes.EIP155Signer
	pubKey  common.PubKey
	address common.Address
}

var _ LiteChainClient = &Client{}

func NewConstructor(host string) LiteChainClientConstructor {
	return func(chain common.Chain, keys *thorclient.Keys) (LiteChainClient, error) {
		return NewClient(chain, host, keys)
	}
}

func NewClient(chain common.Chain, host string, keys *thorclient.Keys) (LiteChainClient, error) {
	// extract the private key
	privateKey, err := keys.GetPrivateKey()
	if err != nil {
		return nil, fmt.Errorf("fail to get private key: %w", err)
	}
	privKey, err := evm.GetPrivateKey(privateKey)
	if err != nil {
		return nil, err
	}

	// derive the public key
	pk, err := cryptocodec.ToTmPubKeyInterface(privateKey.PubKey())
	if err != nil {
		return nil, fmt.Errorf("failed to get tm pub key: %w", err)
	}
	pubKey, err := common.NewPubKeyFromCrypto(pk)
	if err != nil {
		return nil, fmt.Errorf("fail to create pubkey: %w", err)
	}

	// get pubkey address for the chain
	address, err := pubKey.GetAddress(chain)
	if err != nil {
		return nil, fmt.Errorf("fail to get address from pubkey(%s): %w", pk, err)
	}

	// dial the rpc host
	rpc, err := ethclient.Dial(host)
	if err != nil {
		return nil, fmt.Errorf("fail to dial ETH rpc host(%s): %w", host, err)
	}

	// get the chain id
	chainID, err := rpc.ChainID(ctx())
	if err != nil {
		return nil, fmt.Errorf("fail to get chain id: %w", err)
	}

	// create the signer
	signer := etypes.NewEIP155Signer(chainID)

	return &Client{
		chain:   chain,
		rpc:     rpc,
		keys:    keys,
		privKey: privKey,
		signer:  signer,
		pubKey:  pubKey,
		address: address,
	}, nil
}

func (c *Client) GetAccount(pk *common.PubKey) (*common.Account, error) {
	// get nonce
	nonce, err := c.rpc.PendingNonceAt(ctx(), ecommon.HexToAddress(c.address.String()))
	if err != nil {
		return nil, fmt.Errorf("fail to get account nonce: %w", err)
	}

	// get balance
	balance, err := c.rpc.BalanceAt(ctx(), ecommon.HexToAddress(c.address.String()), nil)
	if err != nil {
		return nil, fmt.Errorf("fail to get account balance: %w", err)
	}

	// get amount
	amount := sdk.NewUintFromBigInt(balance)
	amount = amount.Quo(sdk.NewUint(1e10)) // 1e18 -> 1e8

	// create account
	return &common.Account{
		Sequence: int64(nonce),
		Coins: common.Coins{
			common.NewCoin(c.chain.GetGasAsset(), amount),
		},
	}, nil
}

func (c *Client) SignTx(tx SimTx) ([]byte, error) {
	// get nonce
	nonce, err := c.rpc.PendingNonceAt(ctx(), ecommon.HexToAddress(c.address.String()))
	if err != nil {
		return nil, fmt.Errorf("fail to get account nonce: %w", err)
	}

	// get gas price
	gasPrice, err := c.rpc.SuggestGasPrice(ctx())
	if err != nil {
		return nil, fmt.Errorf("fail to get gas price: %w", err)
	}

	// to address to evm address
	toAddress := ecommon.HexToAddress(tx.ToAddress.String())

	// create signable tx
	signable := etypes.NewTx(&etypes.LegacyTx{
		Nonce:    nonce,
		To:       &toAddress,
		Data:     []byte(tx.Memo),
		GasPrice: gasPrice,
		Gas:      21000 + 3000,                                   // standard transfer + memo
		Value:    tx.Coin.Amount.Mul(sdk.NewUint(1e10)).BigInt(), // 1e8 -> 1e18,
	})

	// sign the tx
	hash := c.signer.Hash(signable)
	sig, err := ecrypto.Sign(hash[:], c.privKey)
	if err != nil {
		return nil, fmt.Errorf("fail to sign tx: %w", err)
	}

	// apply the signature
	newTx, err := signable.WithSignature(c.signer, sig)
	if err != nil {
		return nil, fmt.Errorf("fail to apply signature to tx: %w", err)
	}

	// marshal and return
	enc, err := newTx.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("fail to marshal tx to json: %w", err)
	}

	return enc, nil
}

func (c *Client) BroadcastTx(signed []byte) (string, error) {
	tx := &etypes.Transaction{}
	if err := tx.UnmarshalJSON(signed); err != nil {
		return "", err
	}
	txid := tx.Hash().String()

	// remove 0x prefix
	txid = strings.TrimPrefix(txid, "0x")

	// send the transaction
	return txid, c.rpc.SendTransaction(ctx(), tx)
}
