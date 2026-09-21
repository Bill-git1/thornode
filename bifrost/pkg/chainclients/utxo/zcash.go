package utxo

import (
	"bytes"
	"errors"
	"fmt"
	"math"

	btcchaincfg "github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	chainclienttypes "gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/shared/types"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/utxo/zecutil"
	stypes "gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
	btctxscript "gitlab.com/thorchain/thornode/v3/bifrost/txscript/txscript"
	"gitlab.com/thorchain/thornode/v3/common"
)

func (c *Client) getChainCfgZEC() *btcchaincfg.Params {
	switch common.CurrentChainNetwork {
	case common.MockNet:
		return &btcchaincfg.Params{Name: "testnet3"}
	default:
		return &btcchaincfg.Params{Name: "mainnet"}
	}
}

// getZecExpiryHeight uses the outbound's THORChain height, not a validator's
// moving ZEC tip. All signers must construct the same digest on their first try.
func (c *Client) getZecExpiryHeight(thorchainHeight int64) (uint32, error) {
	height, err := c.bridge.GetLastObservedInHeightAtHeight(common.ZECChain, thorchainHeight)
	if err != nil {
		return 0, fmt.Errorf("get agreed ZEC height for expiry: %w", err)
	}
	if height <= 0 || height > int64(math.MaxUint32-ZecTxExpiry) {
		return 0, fmt.Errorf("invalid agreed ZEC height for expiry: %d", height)
	}
	return uint32(height) + ZecTxExpiry, nil
}

func checkZecExpiry(expiry uint32, chainHeight int64) error {
	if chainHeight <= 0 {
		return fmt.Errorf("invalid ZEC tip height: %d", chainHeight)
	}
	// ZIP-203 permits inclusion at expiry, but the next block after a tip at
	// expiry is already too late. Zero denotes a legacy non-expiring payload.
	if expiry != 0 && chainHeight >= int64(expiry) {
		return fmt.Errorf("%w: ZEC expiry height %d, current height %d", chainclienttypes.ErrTxExpired, expiry, chainHeight)
	}
	return nil
}

// checkZecBroadcastExpiry returns a confirmed txid when another signer already
// mined the payload. An ambiguous lookup remains retryable; an expired payload
// that is absent, orphaned, or still in the mempool must leave the retry queue.
func (c *Client) checkZecBroadcastExpiry(payload []byte, chainHeight int64) (string, error) {
	tx := &zecutil.MsgTx{MsgTx: &wire.MsgTx{}}
	if err := tx.ZecDeserialize(bytes.NewReader(payload)); err != nil {
		return "", fmt.Errorf("fail to deserialize ZEC payload: %w", err)
	}
	expiryErr := checkZecExpiry(tx.ExpiryHeight, chainHeight)
	if !errors.Is(expiryErr, chainclienttypes.ErrTxExpired) {
		return "", expiryErr
	}
	txid := chainhash.DoubleHashH(payload).String()
	confirmed, err := c.rpc.GetRawTransactionVerbose(txid)
	if err != nil {
		if isUTXOTxNotFoundErr(err) {
			return "", expiryErr
		}
		return "", fmt.Errorf("check expired ZEC transaction %s: %w", txid, err)
	}
	if confirmed == nil || confirmed.Txid != txid || (confirmed.Confirmations > 0 && confirmed.BlockHash == "") {
		return "", fmt.Errorf("invalid lookup response for expired ZEC transaction %s", txid)
	}
	if confirmed.Confirmations > 0 && confirmed.BlockHash != "" {
		return txid, nil
	}
	return "", expiryErr
}

func (c *Client) signUTXOZEC(zecTx *zecutil.MsgTx, tx stypes.TxOutItem, amount int64, sourceScript []byte, idx int, thorchainHeight int64) (err error) {
	var signable btctxscript.Signable
	if tx.VaultPubKey.Equals(c.nodePubKey) {
		signable = btctxscript.NewPrivateKeySignable(c.nodePrivKey)
	} else {
		signable = newTssSignableBTC(tx.VaultPubKey, c.tssKeySigner, c.log, thorchainHeight)
	}

	var cache *zecutil.TxSigHashes
	if cache, err = zecutil.NewTxSigHashes(zecTx); err != nil {
		return err
	}

	blakeHash, err := zecutil.Blake2bSignatureHash(sourceScript, cache, txscript.SigHashAll, zecTx, idx, amount)
	if err != nil {
		return err
	}

	signature, err := signable.Sign(blakeHash)
	if err != nil {
		return err
	}

	if !signature.Verify(blakeHash, signable.GetPubKey()) {
		return fmt.Errorf("signature verify failed")
	}

	pkData := signable.GetPubKey().SerializeCompressed()

	sigScript, err := btctxscript.NewScriptBuilder().
		AddData(append(signature.Serialize(), byte(txscript.SigHashAll))).
		AddData(pkData).
		Script()
	if err != nil {
		return err
	}

	zecTx.TxIn[idx].SignatureScript = sigScript

	return nil
}
