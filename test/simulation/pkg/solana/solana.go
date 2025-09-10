package solana

import (
	"encoding/base64"
	"fmt"
	"time"

	btypes "github.com/blocto/solana-go-sdk/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/solana/rpc"
	stypes "gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/solana/types"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"

	. "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// Solana
////////////////////////////////////////////////////////////////////////////////////////

type Client struct {
	chain common.Chain
	rpc   *rpc.SolRPC

	keys    *thorclient.Keys
	signer  btypes.Account
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
	pkStruct, err := keys.GetPrivateKeyEDDSA()
	if err != nil {
		return nil, fmt.Errorf("fail to get private key: %w", err)
	}

	account, err := btypes.AccountFromBytes(pkStruct.Bytes())
	if err != nil {
		return nil, fmt.Errorf("fail to get account: %w", err)
	}

	pk, err := cryptocodec.ToCmtPubKeyInterface(pkStruct.PubKey())
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

	rpc := rpc.NewSolRPC(host, time.Second*30)

	return &Client{
		chain:   chain,
		rpc:     rpc,
		keys:    keys,
		pubKey:  pubKey,
		address: address,
		signer:  account,
	}, nil
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

	lamportsBalance, err := c.rpc.GetBalance(address.String(), "finalized", 0)
	if err != nil {
		return nil, fmt.Errorf("fail to get balance: %w", err)
	}

	// get amount
	amount := cosmos.NewUintFromBigInt(lamportsBalance)
	amount = amount.Quo(cosmos.NewUint(10)) // 1e9 -> 1e8

	coins := common.Coins{
		common.NewCoin(common.SOLAsset, amount),
	}

	return &common.Account{
		Coins: coins,
	}, nil
}

func (c *Client) SignTx(tx SimTx) ([]byte, error) {
	// get latest blockhash
	blockhash, err := c.rpc.GetLatestBlockhash()
	if err != nil {
		return nil, fmt.Errorf("fail to get latest blockhash: %w", err)
	}

	// public keys
	fromPubKey := stypes.MustPublicKeyFromString(c.address.String())
	toPubKey := stypes.MustPublicKeyFromString(tx.ToAddress.String())

	// create transaction
	transaction := stypes.NewTransferTransaction(fromPubKey, toPubKey, tx.Coin.Amount.Uint64(), tx.Memo, blockhash)
	rawMessage, err := transaction.Message.Serialize()
	if err != nil {
		return nil, fmt.Errorf("fail to serialize transaction: %w", err)
	}

	// Sign rawTx
	signature, err := c.sign(rawMessage)
	if err != nil {
		return nil, fmt.Errorf("fail to sign transaction: %w", err)
	}
	err = transaction.AddSignature(signature)
	if err != nil {
		return nil, fmt.Errorf("fail to add signature: %w", err)
	}
	signedTx, err := transaction.Serialize()
	if err != nil {
		return nil, fmt.Errorf("fail to serialize signed transaction: %w", err)
	}

	return signedTx, nil
}

func (c *Client) sign(msg []byte) ([]byte, error) {
	return c.signer.Sign(msg), nil
}

func (c *Client) BroadcastTx(signed []byte) (string, error) {
	base64Tx := base64.StdEncoding.EncodeToString(signed)
	txSig, err := c.rpc.BroadcastTx(base64Tx)
	if err != nil {
		return "", fmt.Errorf("failed to broadcast tx: %w", err)
	}

	return txSig, nil
}
