# Chain Clients

THORChain supports multiple Layer 1 (L1) blockchains via **chain clients**, which are pluggable modules under the Bifrost subsystem. Each client enables THORChain to:

- Detect inbound transactions
- Construct and sign outbound transactions
- Track balances and confirm vault solvency
- Handle chain-specific logic like mempool handling, gas tracking, re-orgs, and finality

Chain clients are located in:

```text
/bifrost/pkg/chainclients
```

## Structure and Composition

Each chain lives in its own folder and typically inherits logic from a shared implementation:

| Client Folder | Purpose                 | Used For             |
| ------------- | ----------------------- | -------------------- |
| `evm/`        | Shared EVM logic        | ETH, BSC, AVAX, BASE |
| `utxo/`       | Shared UTXO logic       | BTC, LTC, BCH, DOGE  |
| `xrp/`        | XRP-specific logic      | XRP                  |
| `tron/`       | Tron-specific logic     | TRON                 |
| `solana/`     | Solana-specific logic   | SOL                  |
| `cosmos/`     | Cosmos SDK-based chains | ATOM.                |

Chains that do not fit a shared model can implement their own logic from scratch. For UTXO-based chains, the actual `Client` is constructed using the shared implementation in `utxo/client.go`, which handles block scanning, mempool fetches, reorg tracking, and solvency reporting.

Each specific chain such as BTC, LTC, or DOGE wraps this shared logic with minimal overrides in `btc/client.go`, `ltc/client.go`, etc.

Similarly, for EVM-based chains like Ethereum, Avalanche, and BSC, shared functionality is implemented in evm/client.go and reused by each specific EVM client (e.g., `ethereum/client.go`, `bsc/client.go`).

## ChainClient Interface

Every chain client in Bifrost must implement the `ChainClient` interface defined in [`types/types.go`](https://gitlab.com/thorchain/thornode/-/blob/develop/bifrost/pkg/chainclients/shared/types/types.go). It encapsulates the lifecycle of chain interactions — scanning, observation, signing, broadcasting, solvency reporting, and account inspection. Clients may override additional chain-specific behavior using optional interfaces or embedded logic.

```go
// ChainClient defines the behavior a chain client must implement.
type ChainClient interface {
	// Start begins the chain client with queues for txs, errata, solvency, and network fees.
	Start(
		globalTxsQueue chan types.TxIn,
		globalErrataQueue chan types.ErrataBlock,
		globalSolvencyQueue chan types.Solvency,
		globalNetworkFeeQueue chan common.NetworkFee,
	)

	// Stop halts the chain client and any background processes.
	Stop()
	// IsBlockScannerHealthy reports if the block scanner is functioning normally.
	IsBlockScannerHealthy() bool
	// SignTx prepares and returns the signed transaction bytes.
	SignTx(tx types.TxOutItem, height int64) ([]byte, []byte, *types.TxInItem, error)
	// BroadcastTx sends the signed transaction to the chain.
	BroadcastTx(tx types.TxOutItem, rawTx []byte) (string, error)
	// GetHeight returns the current on-chain block height.
	GetHeight() (int64, error)
	// GetAddress derives an address from a given vault public key.
	GetAddress(poolPubKey common.PubKey) string
	// GetAccount returns the on-chain account details at a specific block height.
	GetAccount(poolPubKey common.PubKey, height *big.Int) (common.Account, error)
	// GetAccountByAddress returns account details by address.
	GetAccountByAddress(address string, height *big.Int) (common.Account, error)
	// GetChain returns the chain this client handles (e.g., BTC, ETH).
	GetChain() common.Chain
	// GetConfig returns chain-specific Bifrost configuration.
	GetConfig() config.BifrostChainConfiguration
	// OnObservedTxIn processes inbound transactions observed from the scanner.
	OnObservedTxIn(txIn types.TxInItem, blockHeight int64)
	// GetConfirmationCount returns how many confirmations a transaction has.
	GetConfirmationCount(txIn types.TxIn) int64
	// ConfirmationCountReady determines if a transaction has reached finality.
	ConfirmationCountReady(txIn types.TxIn) bool
	// GetBlockScannerHeight returns the block height tracked by the scanner.
	GetBlockScannerHeight() (int64, error)
	// GetLatestTxForVault fetches the last inbound and outbound tx for a vault.
	GetLatestTxForVault(vault string) (string, string, error)
	// RollbackBlockScanner rewinds the block scanner to reprocess recent blocks.
	RollbackBlockScanner() error
}
```

### Solvency Reporter

Bifrost also defines a solvency check function type:

```go
// SolvencyReporter reports the solvency of the chain at the given height.
type SolvencyReporter func(height int64) error
```

Solvency reports ensure THORChain’s view of vault balances matches on-chain state, helping detect drift or failures in observation.

## Observer

The Observer component is responsible for detecting relevant inbound transactions to THORChain vaults. Each Observer implementation runs continuously and:

1. Monitors new blocks from the connected full node.
2. Extracts transactions involving THORChain vault addresses.
3. Constructs `ObservedTx` structs.
4. Broadcasts valid observations for consensus.

Observer accuracy is critical for:

- Preventing missed deposits
- Triggering appropriate finality logic (pre-confirmation)
- Tracking aggregator metadata (e.g., transferOutAndCall)

Inbound transactions must be:

- Sent to an `Active` or `Retiring` vault
- Observed by at least 67% of nodes to be finalized
- Filtered for chain-specific rules (e.g., XRP tags, Cosmos memos)

## BlockScanner

The BlockScanner component is responsible for scanning the blockchain to detect inbound activity. It is used on **most chains**, and is required for UTXO chains like Bitcoin, Litecoin, and Dogecoin, where mempool visibility and historical block scanning are essential for:

- Detecting missed or orphaned transactions
- Reorg recovery and ErrataTx detection
- Solvency reporting and block-level metadata
- Fetching mempool transactions to speed up inbound reporting

### Required Interface

```go
// BlockScannerFetcher defines the methods a block scanner must implement
type BlockScannerFetcher interface {
    // FetchMemPool scan the mempool
    FetchMemPool(height int64) (types.TxIn, error)
    // FetchTxs scan block with the given height
    FetchTxs(height int64) (types.TxIn, error)
    // GetHeight return current block height
    GetHeight() (int64, error)
}
```

Chains that support mempool visibility (e.g., Cosmos SDK, Solana) also use this interface for near-instant detection of new txs. For EVM chains, where event subscriptions are the primary mechanism, BlockScanner is optional but sometimes included for fallback and deep scan support.

## Vault Address Derivation

Each chain client must derive vault addresses from TSS public keys using the appropriate signature scheme:

| Chain Type | Algorithm | Derivation Example                        |
| ---------- | --------- | ----------------------------------------- |
| Bitcoin    | ECDSA     | P2WPKH from compressed pubkey             |
| Ethereum   | ECDSA     | `keccak256(pubkey)[12:]`, EIP-55 checksum |
| Solana     | EDDSA     | Base58 from ed25519                       |

THORChain provides both `PubKey` and `PubKeyEddsa` for each vault. Clients derive addresses based on the chain's native format.

See the full list of [support address formats here](../concepts/querying-thorchain.md#supported-address-formats).

## Shared Responsibilities

All chain clients must handle the following responsibilities consistently:

### Observing Inbound Transactions

- Clients scan new blocks and detect transactions to THORChain-controlled vaults.
- Detected transactions are wrapped in `ObservedTx` structs and submitted via `MsgObservedTxIn`.
- Finality is enforced based on chain type (instant vs delayed finality).

### Signing and Broadcasting Outbound Transactions

- Clients receive `TxOut` instructions from THORChain to spend funds from vaults.
- Bifrost handles TSS signing.
- Clients broadcast the transaction and submit `MsgObservedTxOut`.
- Outbound transactions are also instantly observed using [AutoObserve](../bifrost/how-bifrost-works.md#autoobserve)

### Confirmation Counting

- For delayed-finality chains (EVM, UTXO), transactions require dynamic confirmations.

- Confirmations are calculated using:

  ```go
  RequiredConfirmations = min(
    (TxValue / BlockReward) × ConfMultiplier,
    MAXCONFIRMATIONS
  )
  ```

- Finality is only reached after this threshold is met.

`MAXCONFIRMATIONS` values are defined per chain in Mimir:

```json
{
  "MAXCONFIRMATIONS-BTC": 2,
  "MAXCONFIRMATIONS-BCH": 3,
  "MAXCONFIRMATIONS-LTC": 6,
  "MAXCONFIRMATIONS-DOGE": 15,
  "MAXCONFIRMATIONS-ETH": 14
}
```

### Re-org Detection

- Clients must detect chain reorganizations.
- Transactions removed by a re-org are reported as `ErrataTx`.

### Stuck or Dropped Transactions

- EVM clients must handle nonce gaps by replacing stuck transactions with a higher gas self-send (the "unstuck tx").
- UTXO clients should support CPFP (Child Pays For Parent) and RBF (Replace-by-Fee) to resolve stuck sends.

## Token Whitelists

For chains that support tokens (EVM, Solana), THORChain maintains per-chain whitelists to reduce attack surface. These lists are JSON files stored in the Thornode repository and periodically fetched by clients.

See [token-lists.md](./token-lists.md) for details.

## Related Pages

- [How Bifrost Works](../bifrost/how-bifrost-works.md)
- [Vault Behaviors](../bifrost/vault-behaviors.md)
- [Integrating New Chains](../new-chains/implementation-guide.md)
- [Sending Transactions](../concepts/sending-transactions.md)
