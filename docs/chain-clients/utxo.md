# UTXO Chain Clients

THORChain supports several UTXO-based chains such as Bitcoin, Litecoin, Bitcoin Cash, and Dogecoin. These chains share a common implementation based on `utxo/client.go`, which handles block scanning, mempool observations, signing, consolidation, and solvency reporting.

## Shared Architecture

UTXO chains use a shared client located at:

```text
/bifrost/pkg/chainclients/utxo/client.go
```

Each chain (e.g. BTC, LTC) wraps this logic with minimal overrides:

```text
/bifrost/pkg/chainclients/bitcoin/client.go
/bifrost/pkg/chainclients/litecoin/client.go
```

The shared logic handles:

- Block and mempool scanning
- UTXO input/output analysis
- Re-org tracking (BlockCache)
- Child-pays-for-parent (CPFP) support
- Fee rate reporting (sats/byte)
- Transaction signing with `btcutil`
- Vault balance tracking and solvency reporting

## Inbound Observations

Inbound UTXOs are detected by scanning new blocks and mempool data. A transaction is valid for observation if:

- It has at least one output to an Asgard vault
- It includes an `OP_RETURN` output with a valid memo

The observer parses these and pushes a `TxIn` to THORChain once confirmed.

See [Inbound Transactions](../bifrost/how-bifrost-works.md#observing-inbound-transactions).
For wallet developers, see [Sending UTXO Transactions](../concepts/sending-transactions.md#utxo-chains) for required transaction structure, dust thresholds, OP_RETURN handling, and examples of short and long memos.

## Confirmation Counting

THORChain dynamically delays finality based on transaction value and miner incentives.

See [Finality & Confirmation Counting](../bifrost/how-bifrost-works.md#finality--pre-confirmation).

### UTXO Specifics

- `txValue` is the sum of all vault-receiving outputs
- `blockReward` includes the subsidy + miner fees
- If no coinbase tx is found, defaults to 3.125 (for BTC). Rewards for each UTXO chain are defined `DefaultCoinbase()`.

## Gas Tracking

Fee is calculated as:

```go
Fee = total inputs - total outputs
```

Reported to THORChain as `sats/byte` based on the previous block and max of last 20.

See [Gas Tracking](../bifrost/how-bifrost-works.md#gas-tracking).

---

## Vault Address Derivation

UTXO vaults use compressed ECDSA public keys to derive P2WPKH addresses.

```go
btcec.PublicKey.SerializeCompressed()
```

## Reorg Handling

UTXO clients cache the last `BlockCacheSize = 144` blocks. When a block reappears at an existing height, previously observed transactions are checked. If missing, they are re-orged and an `ErrataTx` is submitted to revert state.

See [Re-orgs & Errata](../bifrost/how-bifrost-works.md#re-org-detection).

## UTXO Consolidation

Bitcoin and similar chains limit unconfirmed transaction ancestry to \~20 ancestors. Additionally, signing large transactions with many inputs leads to long, resource-heavy ceremonies. While TSS signing is parallelized, this can still create bottlenecks.

To manage this:

- **Trigger**: Consolidation is initiated when a vault holds more than `MaxUTXOsToSpend` UTXOs. This threshold is chain-specific and controlled via Mimir.
- **Frequency**: Evaluated once per block, but only if no consolidation is already pending.
- **Process**: Bifrost generates a consolidation transaction that sends funds from the vault back to itself with memo `consolidate`.

THORChain validates these transactions using the `MsgConsolidate` handler, ensuring:

- Inputs belong to the vault
- Output address matches the vault
- Memo is correct

If validation fails, the vault is slashed for the attempted consolidation amount.

Consolidation is **initiated by Bifrost** but **validated by THORChain**. This logic ensures inputs are managed proactively to prevent large, unmanageable signing payloads.

## RBF Handling

Replace-by-fee (RBF) is supported for user-initiated deposits to Asgard vaults. This allows users to bump the fee on a stuck transaction by rebroadcasting it with a higher fee. Vault-generated transactions do not use RBF. Instead, fee bumping (if required) is handled via other mechanisms such as child-pays-for-parent (CPFP).

## Related Pages

- [Chain Clients Overview](./README.md)
- [How Bifrost Works](../bifrost/how-bifrost-works.md)
- [Sending Transactions](../concepts/sending-transactions.md)
- [Memos](../concepts/memos.md)
