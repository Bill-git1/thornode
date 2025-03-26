package xrp

import (
	"encoding/hex"
	"fmt"

	sdkmath "cosmossdk.io/math"

	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/xrp/keymanager"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient"
	"gitlab.com/thorchain/thornode/v3/common"

	. "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/types"

	"github.com/Peersyst/xrpl-go/binary-codec"
	"github.com/Peersyst/xrpl-go/xrpl/queries/account"
	qcommon "github.com/Peersyst/xrpl-go/xrpl/queries/common"
	"github.com/Peersyst/xrpl-go/xrpl/rpc"
	transactions "github.com/Peersyst/xrpl-go/xrpl/transaction"
	txtypes "github.com/Peersyst/xrpl-go/xrpl/transaction/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// XRP
////////////////////////////////////////////////////////////////////////////////////////

type Client struct {
	chain     common.Chain
	keys      *thorclient.Keys
	localKm   *keymanager.KeyManager
	rpcClient *rpc.Client
}

var _ LiteChainClient = &Client{}

func NewConstructor(host string) LiteChainClientConstructor {
	return func(chain common.Chain, keys *thorclient.Keys) (LiteChainClient, error) {
		return NewClient(chain, host, keys)
	}
}

func NewClient(chain common.Chain, host string, keys *thorclient.Keys) (LiteChainClient, error) {
	// extract the private key
	privKey, err := keys.GetPrivateKey()
	if err != nil {
		return nil, fmt.Errorf("fail to get private key: %w", err)
	}
	localKm, err := keymanager.NewKeyManager(privKey, keymanager.SECP256K1)
	if err != nil {
		return nil, fmt.Errorf("fail to create key manager: %w", err)
	}
	rpcConfig, err := rpc.NewClientConfig(host)
	if err != nil {
		fmt.Println("ERROR ERROR ERROR 5")
		return nil, fmt.Errorf("unable to create rpc config for client, %w", err)
	}
	rpcClient := rpc.NewClient(rpcConfig)
	rpcClient.NetworkID = 1234

	return &Client{
		chain:     chain,
		keys:      keys,
		localKm:   localKm,
		rpcClient: rpcClient,
	}, nil
}

func (c *Client) GetAccount(pk *common.PubKey) (*common.Account, error) {
	aiReq := account.InfoRequest{
		Account:     txtypes.Address(c.localKm.AccountID),
		LedgerIndex: qcommon.Current, // Query current/non-closed/non-validated ledger
	}
	aiResp, err := c.rpcClient.GetAccountInfo(&aiReq)
	if err != nil {
		return nil, err
	}

	balance := sdkmath.NewUint(aiResp.AccountData.Balance.Uint64()).MulUint64(100) // 1e6 -> 1e8
	coins := common.NewCoins(common.NewCoin(common.XRPAsset, balance))

	return &common.Account{
		Sequence:      int64(aiResp.AccountData.Sequence),
		AccountNumber: 0,
		Coins:         coins,
	}, nil
}

func (c *Client) SignTx(tx SimTx) ([]byte, error) {
	// get account
	account, err := c.GetAccount(nil)
	if err != nil {
		return nil, fmt.Errorf("fail to get account: %w", err)
	}

	// create message
	payment := transactions.Payment{
		BaseTx: transactions.BaseTx{
			Account: txtypes.Address(c.localKm.AccountID),
			// NetworkID:     c.chainID, // Currently doesn't do anything, but the client with populate
			SigningPubKey: c.localKm.PublicKeyHex,
			Sequence:      uint32(account.Sequence),
			Fee:           txtypes.XRPCurrencyAmount(10),
		},
		Amount:      txtypes.XRPCurrencyAmount(tx.Coin.Amount.QuoUint64(100).Uint64()), // 1e8 -> 1e6
		Destination: txtypes.Address(tx.ToAddress),
	}
	if tx.Memo != "" {
		payment.BaseTx.Memos = []txtypes.MemoWrapper{
			{
				Memo: txtypes.Memo{
					MemoData: hex.EncodeToString([]byte(tx.Memo)),
				},
			},
		}
	}

	// sign transaction
	flatTx := payment.Flatten()
	if err := c.rpcClient.Autofill(&flatTx); err != nil {
		return nil, err
	}
	encodedTx, err := binarycodec.EncodeForSigning(flatTx)
	if err != nil {
		return nil, err
	}
	signBytes, err := hex.DecodeString(encodedTx)
	if err != nil {
		return nil, err
	}
	derSignature, err := c.localKm.Sign(signBytes)
	if err != nil {
		return nil, fmt.Errorf("unable to sign using localKeyManager: %w", err)
	}
	flatTx["TxnSignature"] = hex.EncodeToString(derSignature) // use flatTx so we don't need to call autofill again

	// Ensure the signature is valid
	verified, err := c.localKm.Keys.Verify(signBytes, derSignature)
	if err != nil {
		return nil, fmt.Errorf("error verifying signature: %w", err)
	}
	if !verified {
		return nil, fmt.Errorf("unable to verify signature with secpPubKey")
	}

	txHex, err := binarycodec.Encode(flatTx)
	if err != nil {
		return nil, err
	}

	return hex.DecodeString(txHex)
}

func (c *Client) BroadcastTx(signed []byte) (string, error) {
	txBlob := hex.EncodeToString(signed)
	resp, err := c.rpcClient.SubmitAndWait(txBlob, true)
	if err != nil {
		return "", err
	}

	return resp.Hash.String(), nil
}
