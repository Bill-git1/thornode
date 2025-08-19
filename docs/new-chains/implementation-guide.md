# Chain Client Implementation Guide

This guide explains how to implement a new `ChainClient` in THORChain's Bifrost module. It assumes the chain has passed the [evaluation process](./evaluating-new-chains.md) and received [Node Mimir approval](./new-chain-process.md).

Chain clients enable THORChain to:

- Observe inbound L1 transactions
- Sign and broadcast outbound vault transactions
- Track vault balances and emit solvency reports
- Handle chain-specific behaviors like mempool handling, reorgs, and gas estimation

> Most chains extend existing clients — see [EVM](../chain-clients/evm.md), [UTXO](../chain-clients/utxo.md), or [BFT](../chain-clients/bft.md).

## Directory Layout

All chain clients live under:

```text
/bifrost/pkg/chainclients
```

If your chain fits an existing type (EVM, UTXO, BFT), you **should place your client in the corresponding shared folder** and extend the base implementation. This allows you to reuse common logic and minimize custom code.

| Folder    | Type   | Purpose / Notes                           | Example Chains              |
| --------- | ------ | ----------------------------------------- | --------------------------- |
| `evm/`    | Shared | For EVM-compatible chains                 | Ethereum, BSC, AVAX, Base   |
| `utxo/`   | Shared | For Bitcoin-style UTXO chains             | Bitcoin, Litecoin, Dogecoin |
| `bft/`    | Shared | For Cosmos SDK or Tendermint-style chains | Cosmos Hub, GIA, Noble      |
| `solana/` | Custom | Custom logic (non-shared Solana client)   | Solana                      |
| `xrp/`    | Custom | Custom Ripple client                      | XRP                         |
| `tron/`   | Custom | Custom TRON client                        | TRON                        |

### How to Choose

- Use an existing shared type folder (`evm/`, `utxo/`, `bft/`) if your chain's architecture matches.
- Use a new folder (e.g. `solana/`, `xrp/`, `tron/`) only if the chain requires custom observation, signing, or RPC handling that doesn't align with existing types.

Example:

- `evm/ethereum/client.go` – Ethereum-specific config using shared EVM logic
- `utxo/bitcoin/client.go` – Bitcoin-specific config using shared UTXO logic
- `xrp/client.go` – XRP-specific logic using custom implementation

## Required Interfaces

You must implement the following core interfaces:

| Interface      | Purpose                                           |
| -------------- | ------------------------------------------------- |
| `ChainClient`  | Main interface for observation, signing, solvency |
| `Observer`     | Watches for inbound txs                           |
| `Signer`       | Builds and signs outbound txs                     |
| `BlockScanner` | Scans blocks and mempool                          |

See the [`ChainClient` interface](../chain-clients/README.md#chainclient-interface) for method-level detail.

## Vault Address Derivation

Every client must derive vault addresses from the TSS public key:

| Chain    | Algo  | Method                                |
| -------- | ----- | ------------------------------------- |
| Bitcoin  | ECDSA | P2WPKH from compressed pubkey         |
| Ethereum | ECDSA | `keccak256(pubkey)[12:]`, checksummed |
| Solana   | EDDSA | Base58-encoded `ed25519`              |

Use:

```go
btcec.PublicKey.SerializeCompressed()     // ECDSA
edwards25519.PublicKey.Bytes()            // EDDSA
```

Implement:

```go
func (c *YourChain) GetAddress(pubkey common.PubKey) string
```

- Handle both `PubKey` and `PubKeyEddsa`
- Ensure address format is deterministic

## Observation Logic

Each client must observe inbound txs and forward them to THORChain.

Implement:

```go
func (c *YourChain) FetchTxs(height int64) ([]types.TxIn, error)
func (c *YourChain) FetchMemPool() ([]types.TxIn, error) // optional
```

Inbound txs must:

- Be directed to active or retiring vaults
- Include a valid THORChain [memo](../concepts/memos.md)
- Be pushed to the global tx queue:

```go
globalTxsQueue <- types.TxIn
```

### Dust Threshold

Prevent spam by implementing:

```go
func (c *YourChain) GetThreshold() cosmos.Uint
```

Return the minimum inbound amount considered valid.

## Memo Parsing

Memos must be:

- Present
- Decoded and parsed via `x/thorchain/memo`
- Rejected if invalid or missing

## Confirmation Counting

Chains with delayed finality (EVM, UTXO) must track confirmations:

```go
func (c *YourChain) GetConfirmationCount(txIn types.TxIn) int64
```

Use:

```go
RequiredConfirmations = min((TxValue / BlockReward) × Multiplier, MAXCONFIRMATIONS)
```

- `MAXCONFIRMATIONS-<CHAIN>` is set via Mimir

## Outbound Signing

Outbound txs are signed by the TSS and passed to your `ChainClient`.

Implement:

```go
SignTx(txOut types.TxOutItem, height int64) ([]byte, []byte, *types.TxInItem, error)
BroadcastTx(txOut types.TxOutItem, rawTx []byte) (string, error)
```

- Encode signature correctly (ECDSA `r,s,v` or EDDSA)
- Return the tx hash
- Submit outbound observation via `MsgOutboundTx`

> THORChain does not verify the tx on-chain — incorrect hashes are slashable.

## Gas Estimation

Clients must report gas rates and outbound fees:

- `GetGasRate()` — gas price estimate (per block)
- `GetMaxGas()` — ceiling per tx
- `GetFee()` — base fee (used for outbound calculation)

See [Gas Tracking](../bifrost/how-bifrost-works.md#gas-tracking) for details.

## Reorg Handling

If a previously observed tx disappears due to a reorg:

- Emit an `ErrataTx`
- Allow THORChain to revert state

Ensure short-range reorgs are gracefully handled in block polling logic.

## Solvency Reporting

Clients must emit vault balance data periodically:

```go
globalSolvencyQueue <- types.Solvency
```

Missing funds (beyond [`PermittedSolvencyGap`](../mimir.md)) will halt the chain.

## Stuck Transactions

Clients must detect stuck outbound txs due to:

- Low gas
- Dust
- Mempool eviction

EVM chains must implement the **unstuck logic**:

- Reuse the same nonce
- Send a 0-value tx to self
- Use max(gasPrice × 1.1, 2 × current median gas)

## Testing & Simulation

Before submitting your PR:

- Launch the chain on stagenet or testnet
- Test:

  - Inbound observation
  - Outbound signing
  - Vault churn
  - Memo parsing
  - Fee behavior
  - Errata + solvency

- Use:

  - [`thornode/test`](https://gitlab.com/thorchain/thornode/-/tree/develop/test)
  - Local regnet if available

Also see [New Chain Process](./new-chain-process.md#phase-ii-development-and-stagenet-testing) for required test volumes.
