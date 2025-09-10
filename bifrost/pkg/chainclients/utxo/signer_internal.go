package utxo

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"

	"github.com/eager7/dogutil"
	dogetxscript "gitlab.com/thorchain/thornode/v3/bifrost/txscript/dogd-txscript"
	"gitlab.com/thorchain/thornode/v3/constants"

	"github.com/gcash/bchutil"
	bchtxscript "gitlab.com/thorchain/thornode/v3/bifrost/txscript/bchd-txscript"

	"github.com/ltcsuite/ltcutil"
	ltctxscript "gitlab.com/thorchain/thornode/v3/bifrost/txscript/ltcd-txscript"

	"github.com/btcsuite/btcutil"
	btctxscript "gitlab.com/thorchain/thornode/v3/bifrost/txscript/txscript"

	stypes "gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	mem "gitlab.com/thorchain/thornode/v3/x/thorchain/memo"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// UTXO Selection
////////////////////////////////////////////////////////////////////////////////////////

func (c *Client) getMaximumUtxosToSpend() int64 {
	const mimirMaxUTXOsToSpend = `MaxUTXOsToSpend`
	utxosToSpend, err := c.bridge.GetMimir(mimirMaxUTXOsToSpend)
	if err != nil {
		c.log.Err(err).Msg("fail to get MaxUTXOsToSpend")
	}
	if utxosToSpend <= 0 {
		utxosToSpend = c.cfg.UTXO.MaxUTXOsToSpend
	}
	return utxosToSpend
}

// getAllUtxos will iterate unspend utxos for the given address and return the oldest
// set of utxos that can cover the amount.
func (c *Client) getUtxoToSpend(pubkey common.PubKey, total float64) ([]btcjson.ListUnspentResult, error) {
	// get all unspent utxos
	addr, err := pubkey.GetAddress(c.cfg.ChainID)
	if err != nil {
		return nil, fmt.Errorf("fail to get address from pubkey(%s): %w", pubkey, err)
	}
	utxos, err := c.rpc.ListUnspent(addr.String())
	if err != nil {
		return nil, fmt.Errorf("fail to get UTXOs: %w", err)
	}

	// spend UTXO older to younger
	sort.SliceStable(utxos, func(i, j int) bool {
		if utxos[i].Confirmations > utxos[j].Confirmations {
			return true
		} else if utxos[i].Confirmations < utxos[j].Confirmations {
			return false
		}
		return utxos[i].TxID < utxos[j].TxID
	})

	var result []btcjson.ListUnspentResult
	var toSpend float64
	minUTXOAmt := btcutil.Amount(c.cfg.ChainID.DustThreshold().Uint64()).ToBTC()
	utxosToSpend := c.getMaximumUtxosToSpend() // can be set by mimir

	for _, item := range utxos {
		if !c.isValidUTXO(item.ScriptPubKey) {
			c.log.Warn().Str("script", item.ScriptPubKey).Msgf("invalid utxo, unable to spend")
			continue
		}

		if item.Confirmations < c.cfg.UTXO.MinUTXOConfirmations || item.Amount < minUTXOAmt {
			// use all UTXOs sent from asgard, regardless of confirmations or dust threshold
			isSelfTx := c.isSelfTransaction(item.TxID)

			// confirm sender of the UTXO is not asgard in case of lost block meta
			if !isSelfTx {
				isSelfTx = c.isFromAsgard(item.TxID)
			}
			if !isSelfTx {
				continue
			}
		}

		result = append(result, item)
		toSpend += item.Amount

		// in the scenario that there are too many unspent utxos available, make sure it
		// doesn't spend too much as too much UTXO will cause huge pressure on TSS, also
		// make sure it will spend at least maxUTXOsToSpend so the UTXOs will be
		// consolidated
		if int64(len(result)) >= utxosToSpend && toSpend >= total {
			break
		}
	}
	return result, nil
}

// vinsUnspent will return true if all the vins are unspent.
func (c *Client) vinsUnspent(tx stypes.TxOutItem, vins []*wire.TxIn) (bool, error) {
	// get all unspent utxos
	addr, err := tx.VaultPubKey.GetAddress(c.cfg.ChainID)
	if err != nil {
		return false, fmt.Errorf("fail to get address from pubkey(%s): %w", tx.VaultPubKey, err)
	}
	utxos, err := c.rpc.ListUnspent(addr.String())
	if err != nil {
		return false, fmt.Errorf("fail to get UTXOs: %w", err)
	}
	unspent := make(map[string]bool, len(utxos))
	for _, utxo := range utxos {
		unspent[utxo.TxID] = true
	}

	// return false if any vin is spent
	allUnspent := true
	for _, vin := range vins {
		if !unspent[vin.PreviousOutPoint.Hash.String()] {
			c.log.Warn().
				Stringer("in_hash", tx.InHash).
				Stringer("vin", vin.PreviousOutPoint).
				Msg("vin is spent")
			allUnspent = false
		}
	}

	return allUnspent, nil
}

// isSelfTransaction check the block meta to see whether the transactions is broadcast
// by ourselves if the transaction is broadcast by ourselves, then we should be able to
// spend the UTXO even it is still in mempool as such we could daisy chain the outbound
// transaction
func (c *Client) isSelfTransaction(txID string) bool {
	bms, err := c.temporalStorage.GetBlockMetas()
	if err != nil {
		c.log.Err(err).Msg("fail to get block metas")
		return false
	}
	for _, item := range bms {
		for _, tx := range item.SelfTransactions {
			if strings.EqualFold(tx, txID) {
				c.log.Debug().Msgf("%s is self transaction", txID)
				return true
			}
		}
	}
	return false
}

func (c *Client) getPaymentAmount(tx stypes.TxOutItem) float64 {
	amtToPay1e8 := tx.Coins.GetCoin(c.cfg.ChainID.GetGasAsset()).Amount.Uint64()
	amtToPay := btcutil.Amount(int64(amtToPay1e8)).ToBTC()
	if !tx.MaxGas.IsEmpty() {
		gasAmt := tx.MaxGas.ToCoins().GetCoin(c.cfg.ChainID.GetGasAsset()).Amount
		amtToPay += btcutil.Amount(int64(gasAmt.Uint64())).ToBTC()
	}
	return amtToPay
}

// getSourceScript retrieve pay to addr script from tx source
func (c *Client) getSourceScript(tx stypes.TxOutItem) ([]byte, error) {
	sourceAddr, err := tx.VaultPubKey.GetAddress(c.cfg.ChainID)
	if err != nil {
		return nil, fmt.Errorf("fail to get source address: %w", err)
	}

	switch c.cfg.ChainID {
	case common.DOGEChain:
		var addr dogutil.Address
		addr, err = dogutil.DecodeAddress(sourceAddr.String(), c.getChainCfgDOGE())
		if err != nil {
			return nil, fmt.Errorf("fail to decode source address(%s): %w", sourceAddr.String(), err)
		}
		return dogetxscript.PayToAddrScript(addr)
	case common.BCHChain:
		var addr bchutil.Address
		addr, err = bchutil.DecodeAddress(sourceAddr.String(), c.getChainCfgBCH())
		if err != nil {
			return nil, fmt.Errorf("fail to decode source address(%s): %w", sourceAddr.String(), err)
		}
		return bchtxscript.PayToAddrScript(addr)
	case common.LTCChain:
		var addr ltcutil.Address
		addr, err = ltcutil.DecodeAddress(sourceAddr.String(), c.getChainCfgLTC())
		if err != nil {
			return nil, fmt.Errorf("fail to decode source address(%s): %w", sourceAddr.String(), err)
		}
		return ltctxscript.PayToAddrScript(addr)
	case common.BTCChain:
		var addr btcutil.Address
		addr, err = btcutil.DecodeAddress(sourceAddr.String(), c.getChainCfgBTC())
		if err != nil {
			return nil, fmt.Errorf("fail to decode source address(%s): %w", sourceAddr.String(), err)
		}
		return btctxscript.PayToAddrScript(addr)
	default:
		c.log.Fatal().Msg("unsupported chain")
		return nil, nil
	}
}

////////////////////////////////////////////////////////////////////////////////////////
// Build Transaction
////////////////////////////////////////////////////////////////////////////////////////

// estimateTxSize builds a dummy transaction with the given inputs and outputs and
// returns the exact virtual size (vbytes) according to BIP141.
// For non-segwit chains, it returns the actual serialized size.
func (c *Client) estimateTxSize(txes []btcjson.ListUnspentResult, memoScripts [][]byte, customerScript []byte, changeScript []byte) int64 {
	tx := wire.NewMsgTx(wire.TxVersion)

	// Add inputs with realistic witness/scriptSig data for size estimation
	for _, utxo := range txes {
		hash, err := chainhash.NewHashFromStr(utxo.TxID)
		if err != nil {
			c.log.Error().Err(err).Msg("failed to parse txid for size estimation")
			continue
		}
		outpoint := wire.NewOutPoint(hash, utxo.Vout)
		txIn := wire.NewTxIn(outpoint, nil, nil)

		// Add realistic scriptSig/witness data for accurate size estimation
		if c.isSegwitChain() {
			// For segwit chains (BTC, LTC), inputs have empty scriptSig but witness data
			// Typical P2WPKH witness: [signature (71-73 bytes), pubkey (33 bytes)]
			txIn.Witness = make([][]byte, 2)
			txIn.Witness[0] = make([]byte, 72) // signature
			txIn.Witness[1] = make([]byte, 33) // pubkey
		} else {
			// For non-segwit chains (DOGE, BCH), inputs have scriptSig
			// Typical P2PKH scriptSig: [signature (71-73 bytes), pubkey (33 bytes)]
			// Script format: <sig> <pubkey>
			txIn.SignatureScript = make([]byte, 107) // ~72 + 33 + 2 bytes overhead
		}

		tx.AddTxIn(txIn)
	}

	// Add customer output
	tx.AddTxOut(wire.NewTxOut(0, customerScript))

	// Add change output (will be added if balance > 0)
	tx.AddTxOut(wire.NewTxOut(0, changeScript))

	// Add memo outputs
	if len(memoScripts) > 0 {
		// First script is OP_RETURN (value = 0)
		tx.AddTxOut(wire.NewTxOut(0, memoScripts[0]))

		// Additional scripts are P2WPKH/P2PKH outputs with dust value
		for _, script := range memoScripts[1:] {
			tx.AddTxOut(wire.NewTxOut(0, script)) // value doesn't affect size
		}
	}

	// Calculate size based on chain type
	if c.isSegwitChain() {
		// For segwit chains, calculate virtual size (weight/4)
		strippedSize := tx.SerializeSizeStripped()
		totalSize := tx.SerializeSize()
		// Virtual size = (base_size * 3 + total_size) / 4
		return int64((strippedSize*3 + totalSize + 3) / 4) // +3 for proper rounding
	}

	// For non-segwit chains, return actual serialized size
	return int64(tx.SerializeSize())
}

// isSegwitChain returns true if the chain supports segwit transactions
func (c *Client) isSegwitChain() bool {
	switch c.cfg.ChainID {
	case common.BTCChain, common.LTCChain:
		return true
	case common.DOGEChain, common.BCHChain:
		return false
	default:
		c.log.Fatal().Msgf("unsupported chain: %s", c.cfg.ChainID)
		return false
	}
}

func (c *Client) getGasCoin(tx stypes.TxOutItem, vSize int64) common.Coin {
	gasRate := tx.GasRate

	// if the gas rate is zero, try to get from last transaction fee
	if gasRate == 0 {
		fee, vBytes, err := c.temporalStorage.GetTransactionFee()
		if err != nil {
			c.log.Error().Err(err).Msg("fail to get previous transaction fee from local storage")
			return common.NewCoin(c.cfg.ChainID.GetGasAsset(), cosmos.NewUint(uint64(vSize*gasRate)))
		}
		if fee != 0.0 && vSize != 0 {
			var amt btcutil.Amount
			amt, err = btcutil.NewAmount(fee)
			if err != nil {
				c.log.Err(err).Msg("fail to convert amount from float64 to int64")
			} else {
				gasRate = int64(amt) / int64(vBytes) // sats per vbyte
			}
		}
	}

	// default to configured value
	if gasRate == 0 {
		gasRate = c.cfg.UTXO.DefaultSatsPerVByte
	}

	return common.NewCoin(c.cfg.ChainID.GetGasAsset(), cosmos.NewUint(uint64(gasRate*vSize)))
}

func (c *Client) buildTx(tx stypes.TxOutItem, sourceScript []byte) (*wire.MsgTx, map[string]int64, error) {
	txes, err := c.getUtxoToSpend(tx.VaultPubKey, c.getPaymentAmount(tx))
	if err != nil {
		return nil, nil, fmt.Errorf("fail to get unspent UTXO")
	}
	redeemTx := wire.NewMsgTx(wire.TxVersion)
	totalAmt := int64(0)
	individualAmounts := make(map[string]int64, len(txes))
	for _, item := range txes {
		var txID *chainhash.Hash
		txID, err = chainhash.NewHashFromStr(item.TxID)
		if err != nil {
			return nil, nil, fmt.Errorf("fail to parse txID(%s): %w", item.TxID, err)
		}
		// double check that the utxo is still valid
		outputPoint := wire.NewOutPoint(txID, item.Vout)
		sourceTxIn := wire.NewTxIn(outputPoint, nil, nil)
		redeemTx.AddTxIn(sourceTxIn)
		var amt btcutil.Amount
		amt, err = btcutil.NewAmount(item.Amount)
		if err != nil {
			return nil, nil, fmt.Errorf("fail to parse amount(%f): %w", item.Amount, err)
		}
		individualAmounts[fmt.Sprintf("%s-%d", txID, item.Vout)] = int64(amt)
		totalAmt += int64(amt)
	}

	var buf []byte
	var nullDataScripts [][]byte
	switch c.cfg.ChainID {
	case common.DOGEChain:
		var outputAddr dogutil.Address
		outputAddr, err = dogutil.DecodeAddress(tx.ToAddress.String(), c.getChainCfgDOGE())
		if err != nil {
			return nil, nil, fmt.Errorf("fail to decode next address: %w", err)
		}
		buf, err = dogetxscript.PayToAddrScript(outputAddr)
		if err != nil {
			return nil, nil, fmt.Errorf("fail to get pay to address script: %w", err)
		}
		nullDataScripts, err = MemoToScripts(tx.Memo, dogetxscript.MaxDataCarrierSize, dogetxscript.NullDataScript, dogetxscript.PayToWitnessScript)
		if err != nil {
			return nil, nil, fmt.Errorf("fail to generate null data script: %w", err)
		}
	case common.BCHChain:
		var outputAddr bchutil.Address
		outputAddr, err = bchutil.DecodeAddress(tx.ToAddress.String(), c.getChainCfgBCH())
		if err != nil {
			return nil, nil, fmt.Errorf("fail to decode next address: %w", err)
		}
		buf, err = bchtxscript.PayToAddrScript(outputAddr)
		if err != nil {
			return nil, nil, fmt.Errorf("fail to get pay to address script: %w", err)
		}
		nullDataScripts, err = MemoToScripts(tx.Memo, bchtxscript.MaxDataCarrierSize, bchtxscript.NullDataScript, bchtxscript.PayToWitnessScript)
		if err != nil {
			return nil, nil, fmt.Errorf("fail to generate null data script: %w", err)
		}
	case common.LTCChain:
		var outputAddr ltcutil.Address
		outputAddr, err = ltcutil.DecodeAddress(tx.ToAddress.String(), c.getChainCfgLTC())
		if err != nil {
			return nil, nil, fmt.Errorf("fail to decode next address: %w", err)
		}
		buf, err = ltctxscript.PayToAddrScript(outputAddr)
		if err != nil {
			return nil, nil, fmt.Errorf("fail to get pay to address script: %w", err)
		}
		nullDataScripts, err = MemoToScripts(tx.Memo, ltctxscript.MaxDataCarrierSize, ltctxscript.NullDataScript, ltctxscript.PayToWitnessScript)
		if err != nil {
			return nil, nil, fmt.Errorf("fail to generate null data script: %w", err)
		}
	case common.BTCChain:
		var outputAddr btcutil.Address
		outputAddr, err = btcutil.DecodeAddress(tx.ToAddress.String(), c.getChainCfgBTC())
		if err != nil {
			return nil, nil, fmt.Errorf("fail to decode next address: %w", err)
		}
		buf, err = btctxscript.PayToAddrScript(outputAddr)
		if err != nil {
			return nil, nil, fmt.Errorf("fail to get pay to address script: %w", err)
		}
		nullDataScripts, err = MemoToScripts(tx.Memo, btctxscript.MaxDataCarrierSize, btctxscript.NullDataScript, btctxscript.PayToWitnessScript)
		if err != nil {
			return nil, nil, fmt.Errorf("fail to generate null data script: %w", err)
		}
	default:
		c.log.Fatal().Msg("unsupported chain")
	}

	if len(nullDataScripts) == 0 {
		return nil, nil, fmt.Errorf("no null data scripts generated, memo will not be included in the transaction")
	}

	totalSize := c.estimateTxSize(txes, nullDataScripts, buf, sourceScript)

	coinToCustomer := tx.Coins.GetCoin(c.cfg.ChainID.GetGasAsset())

	// maxFee in sats
	maxFeeSats := totalSize * c.cfg.UTXO.MaxSatsPerVByte
	gasCoin := c.getGasCoin(tx, totalSize)
	gasAmtSats := gasCoin.Amount.Uint64()

	// make sure the transaction fee is not more than the max, otherwise it might reject the transaction
	if gasAmtSats > uint64(maxFeeSats) {
		diffSats := gasAmtSats - uint64(maxFeeSats) // in sats
		c.log.Info().Msgf("gas amount: %d is larger than maximum fee: %d, diff: %d", gasAmtSats, uint64(maxFeeSats), diffSats)
		gasAmtSats = uint64(maxFeeSats)
	} else if gasAmtSats < c.minRelayFeeSats {
		diffStats := c.minRelayFeeSats - gasAmtSats
		c.log.Info().Msgf("gas amount: %d is less than min relay fee: %d, diff remove from customer: %d", gasAmtSats, c.minRelayFeeSats, diffStats)
		gasAmtSats = c.minRelayFeeSats
	}

	var memo mem.Memo
	if err == nil {
		// Parse the memo to be able to identify Migrate or Consolidate outbounds.
		memo, err = mem.ParseMemo(common.LatestVersion, tx.Memo)
		if err != nil {
			return nil, nil, fmt.Errorf("fail to parse memo: %w", err)
		}

		// if the total gas spend is more than max gas , then we have to take away some from the amount pay to customer
		if !tx.MaxGas.IsEmpty() {
			maxGasCoin := tx.MaxGas.ToCoins().GetCoin(c.cfg.ChainID.GetGasAsset())
			if gasAmtSats > maxGasCoin.Amount.Uint64() {
				c.log.Info().Msgf("max gas: %s, however estimated gas need %d", tx.MaxGas, gasAmtSats)
				gasAmtSats = maxGasCoin.Amount.Uint64()
			} else if gasAmtSats < maxGasCoin.Amount.Uint64() && memo.GetType() == mem.TxMigrate {
				// if the tx spend less gas then the estimated MaxGas , then the extra can be added to the coinToCustomer
				gap := maxGasCoin.Amount.Uint64() - gasAmtSats
				c.log.Info().Msgf("max gas is: %s, however only: %d is required, gap: %d goes to the vault migrated to", tx.MaxGas, gasAmtSats, gap)
				coinToCustomer.Amount = coinToCustomer.Amount.Add(cosmos.NewUint(gap))
			}
		} else if memo.GetType() == mem.TxConsolidate {
			gap := gasAmtSats
			c.log.Info().Msgf("consolidate tx, need gas: %d", gap)
			coinToCustomer.Amount = common.SafeSub(coinToCustomer.Amount, cosmos.NewUint(gap))
		}
	} else {
		// if the total gas spend is more than max gas , then we have to take away some from the amount pay to customer
		if !tx.MaxGas.IsEmpty() {
			maxGasCoin := tx.MaxGas.ToCoins().GetCoin(c.cfg.ChainID.GetGasAsset())
			if gasAmtSats > maxGasCoin.Amount.Uint64() {
				c.log.Info().Msgf("max gas: %s, however estimated gas need %d", tx.MaxGas, gasAmtSats)
				gasAmtSats = maxGasCoin.Amount.Uint64()
			} else if gasAmtSats < maxGasCoin.Amount.Uint64() {
				// if the tx spend less gas then the estimated MaxGas , then the extra can be added to the coinToCustomer
				gap := maxGasCoin.Amount.Uint64() - gasAmtSats
				c.log.Info().Msgf("max gas is: %s, however only: %d is required, gap: %d goes to customer", tx.MaxGas, gasAmtSats, gap)
				coinToCustomer.Amount = coinToCustomer.Amount.Add(cosmos.NewUint(gap))
			}
		} else {
			memo, err = mem.ParseMemo(common.LatestVersion, tx.Memo)
			if err != nil {
				return nil, nil, fmt.Errorf("fail to parse memo: %w", err)
			}
			if memo.GetType() == mem.TxConsolidate {
				gap := gasAmtSats
				c.log.Info().Msgf("consolidate tx, need gas: %d", gap)
				coinToCustomer.Amount = common.SafeSub(coinToCustomer.Amount, cosmos.NewUint(gap))
			}
		}
	}

	gasAmt := btcutil.Amount(gasAmtSats)
	if err = c.temporalStorage.UpsertTransactionFee(gasAmt.ToBTC(), int32(totalSize)); err != nil {
		c.log.Err(err).Msg("fail to save gas info to UTXO storage")
	}

	// pay to customer
	redeemTxOut := wire.NewTxOut(int64(coinToCustomer.Amount.Uint64()), buf)
	redeemTx.AddTxOut(redeemTxOut)

	// Calculate the total cost of P2WPKH outputs for extended memos
	p2wpkhOutputsCost := int64(0)
	if len(nullDataScripts) > 1 {
		// Each P2WPKH output (nullDataScripts[1:]) costs P2WPKHOutputValue()
		p2wpkhOutputsCost = int64(len(nullDataScripts)-1) * tx.Chain.P2WPKHOutputValue()
	}

	// balance to ourselves
	// add output to pay the balance back ourselves
	// Now properly account for P2WPKH outputs cost
	balance := totalAmt - redeemTxOut.Value - int64(gasAmt) - p2wpkhOutputsCost
	c.log.Info().Msgf("total: %d, to customer: %d, gas: %d, p2wpkh_outputs_cost: %d", totalAmt, redeemTxOut.Value, int64(gasAmt), p2wpkhOutputsCost)
	if balance < 0 {
		return nil, nil, fmt.Errorf("not enough balance to pay customer: %d", balance)
	}
	if balance > 0 {
		c.log.Info().Msgf("send %d back to self", balance)
		redeemTx.AddTxOut(wire.NewTxOut(balance, sourceScript))
	}

	// memo
	if len(tx.Memo) != 0 {
		redeemTx.AddTxOut(wire.NewTxOut(0, nullDataScripts[0]))
		for _, script := range nullDataScripts[1:] {
			redeemTx.AddTxOut(wire.NewTxOut(tx.Chain.P2WPKHOutputValue(), script))
		}
	}

	return redeemTx, individualAmounts, nil
}

// MemoToScripts converts a memo to UTXO scripts.
// Up to 80 bytes in a single OP_RETURN output; for longer memos, 79 bytes plus '^' marker in OP_RETURN,
// with remaining data in P2WPKH outputs (20 bytes each).
func MemoToScripts(memo string, maxDataCarrierSize int, nullDataScript func([]byte) ([]byte, error), payToWitnessKeyHashScript func([]byte) ([]byte, error)) ([][]byte, error) {
	if len(memo) == 0 {
		return nil, nil
	}

	if len(memo) > constants.MaxMemoSize {
		return nil, fmt.Errorf("memo size %d exceeds maximum size of %d bytes", len(memo), constants.MaxMemoSize)
	}

	data := []byte(memo)

	// Calculate number of scripts: 1 OP_RETURN + ceil(remaining_data / 20) P2WPKH outputs
	remainingDataSize := len(data)
	if remainingDataSize > maxDataCarrierSize {
		remainingDataSize -= (maxDataCarrierSize - 1) // Reserve 1 byte for '^'
	} else {
		remainingDataSize = 0
	}
	numScripts := 1 + (remainingDataSize+19)/20 // 1 for OP_RETURN, plus P2WPKH outputs (20 bytes each)
	scripts := make([][]byte, 0, numScripts)

	// First chunk OP_RETURN: up to 80 bytes; if > 80 bytes, 79 bytes + '^' marker
	firstChunkSize := len(data)
	continuation := false
	if firstChunkSize > maxDataCarrierSize { // Reserve 1 byte for '^' if needed
		firstChunkSize = maxDataCarrierSize - 1
		continuation = true
	}
	firstChunk := make([]byte, 0, maxDataCarrierSize)
	firstChunk = append(firstChunk, data[:firstChunkSize]...)
	if continuation {
		firstChunk = append(firstChunk, '^')
	}
	script, err := nullDataScript(firstChunk)
	if err != nil {
		return nil, fmt.Errorf("fail to create OP_RETURN script: %w", err)
	}
	scripts = append(scripts, script)

	// Remaining data (if any) goes into P2WPKH outputs, 20 bytes each
	if continuation {
		remainingData := data[firstChunkSize:]
		for i := 0; len(remainingData) > 0; i++ {
			// Take up to 20 bytes for this P2WPKH output
			chunkSize := len(remainingData)
			if chunkSize > 20 {
				chunkSize = 20
			}
			hash := make([]byte, 20)
			copy(hash, remainingData[:chunkSize])
			// Remaining bytes (if < 20) are padded with zeros, signaling the end
			// (getMemo stops at a hash ending with "00")
			p2wpkhScript, err := payToWitnessKeyHashScript(hash)
			if err != nil {
				return nil, fmt.Errorf("fail to create P2WPKH script at index %d: %w", i, err)
			}
			scripts = append(scripts, p2wpkhScript)
			// Move to the next chunk
			remainingData = remainingData[chunkSize:]
		}
	}

	return scripts, nil
}

////////////////////////////////////////////////////////////////////////////////////////
// UTXO Consolidation
////////////////////////////////////////////////////////////////////////////////////////

// consolidateUTXOs only required when there is a new block
func (c *Client) consolidateUTXOs() {
	defer func() {
		c.wg.Done()
		c.consolidateInProgress.Store(false)
	}()

	nodeStatus, err := c.bridge.FetchNodeStatus()
	if err != nil {
		c.log.Err(err).Msg("fail to get node status")
		return
	}
	if nodeStatus != types.NodeStatus_Active {
		c.log.Info().Msgf("node is not active , doesn't need to consolidate utxos")
		return
	}
	vaults, err := c.bridge.GetAsgards()
	if err != nil {
		c.log.Err(err).Msg("fail to get current asgards")
		return
	}
	utxosToSpend := c.getMaximumUtxosToSpend()
	for _, vault := range vaults {
		if !vault.Contains(c.nodePubKey) {
			// Not part of this vault , don't need to consolidate UTXOs for this Vault
			continue
		}
		// the amount used here doesn't matter , just to see whether there are more than 15 UTXO available or not
		var utxos []btcjson.ListUnspentResult
		utxos, err = c.getUtxoToSpend(vault.PubKey, 0.01)
		if err != nil {
			c.log.Err(err).Msg("fail to get utxos to spend")
			continue
		}
		// doesn't have enough UTXOs , don't need to consolidate
		if int64(len(utxos)) < utxosToSpend {
			continue
		}
		total := 0.0
		for _, item := range utxos {
			total += item.Amount
		}
		var addr common.Address
		addr, err = vault.PubKey.GetAddress(c.cfg.ChainID)
		if err != nil {
			c.log.Err(err).Msgf("fail to get address for pubkey: %s", vault.PubKey)
			continue
		}
		// THORChain usually pay 1.5 of the last observed fee rate
		feeRate := math.Ceil(float64(c.lastFeeRate) * 3 / 2)
		var amt btcutil.Amount
		amt, err = btcutil.NewAmount(total)
		if err != nil {
			c.log.Err(err).Msgf("fail to convert to amount: %f", total)
			continue
		}

		txOutItem := stypes.TxOutItem{
			Chain:            c.cfg.ChainID,
			ToAddress:        addr,
			VaultPubKey:      vault.PubKey,
			VaultPubKeyEddsa: vault.PubKeyEddsa,
			Coins: common.Coins{
				common.NewCoin(c.cfg.ChainID.GetGasAsset(), cosmos.NewUint(uint64(amt))),
			},
			Memo:    mem.NewConsolidateMemo().String(),
			MaxGas:  nil,
			GasRate: int64(feeRate),
		}
		var height int64
		height, err = c.bridge.GetBlockHeight()
		if err != nil {
			c.log.Err(err).Msg("fail to get THORChain block height")
			continue
		}
		var rawTx []byte

		signAndBroadcast := func() {
			lock := c.GetVaultLock(vault.PubKey.String())
			lock.Lock()
			defer lock.Unlock()

			rawTx, _, _, err = c.SignTx(txOutItem, height)
			if err != nil {
				c.log.Err(err).Msg("fail to sign consolidate txout item")
			}
			var txID string
			txID, err = c.BroadcastTx(txOutItem, rawTx)
			if err != nil {
				c.log.Err(err).Str("signed", string(rawTx)).Msg("fail to broadcast consolidate tx")
			} else {
				c.log.Info().Msgf("broadcast consolidate tx successfully, hash:%s", txID)
			}
		}

		signAndBroadcast()
	}
}
