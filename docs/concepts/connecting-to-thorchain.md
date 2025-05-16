# Connecting to THORChain

This guide helps developers connect to THORChain’s network for querying data, building wallets, dashboards, or debugging network issues. THORChain provides four primary data sources:

- **Midgard**: Consumer data for swaps, pools, and volume, ideal for dashboards.
- **THORNode**: Raw blockchain data for THORChain-specific queries, used by wallets and explorers.
- **Cosmos RPC**: Generic Cosmos SDK data, such as balances and transactions.
- **Tendermint RPC**: Consensus and node status data for monitoring.

```admonish info
The below endpoints are run by specific organisations for public use. There is a cost to running these services. If you want to run your own full node, please see [https://docs.thorchain.org/thornodes/overview.](https://docs.thorchain.org/thornodes/overview)
A list of endpoints operated by Nine Realms is located at [NineRealms THORChain Ops Dashboard](https://ops.ninerealms.com/links)
```

## Quick Reference

| Source             | Purpose                            | Mainnet URLs                                                                                                                                               | Stagenet URLs                         |
| ------------------ | ---------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------- |
| **Midgard**        | Swap, pool, volume, and user data  | \- midgard.thorswap.net<br>- midgard.ninerealms.com<br>- midgard.thorchain.liquify.com                                                                     | \- stagenet-midgard.ninerealms.com    |
| **THORNode**       | THORChain-specific blockchain data | \- thornode.thorswap.net<br>- thornode.ninerealms.com<br>- thornode.thorchain.liquify.com<br>- Pre-hard-fork (blocks ≤4786559): thornode-v0.ninerealms.com | \- stagenet-thornode.ninerealms.com   |
| **Cosmos RPC**     | Cosmos SDK data (e.g., balances)   | \- Example: thornode.ninerealms.com                                                                                                                        | \- Not publicly available; run a node |
| **Tendermint RPC** | Consensus and node status data     | \- rpc.ninerealms.com<br>- rpc.thorchain.liquify.com<br>- rpc.thorswap.net<br>- Pre-hard-fork (blocks ≤4786559): rpc-v0.ninerealms.com                     | \- stagenet-rpc.ninerealms.com        |

## Usage Guidelines

- **Rate Limits**: Public endpoints may enforce limits (e.g., 100 requests/minute for Midgard). Check provider docs or contact operators (e.g., THORSwap, Nine Realms).
- **Run Your Own Node**: For production apps, run a THORNode to avoid rate limits and ensure uptime. See THORNode Setup Guide.
- **Hard-Fork Note**: Mainnet hard-fork at block 4786560 requires post-hard-fork endpoints for newer data; use pre-hard-fork endpoints for historical queries (blocks ≤4786559).
- **Error Handling**: Handle HTTP 429 (rate limit) or 503 (node overload) with exponential backoff for retries.

## Midgard

Midgard provides time-series data for swaps, pools, volume, and liquidity providers, ideal for dashboards and analytics. It proxies THORNode queries to reduce network load and runs on every node.

- **Mainnet:**
  - [https://midgard.thorswap.net/v2/doc](https://midgard.thorswap.net/v2/doc)
  - [https://midgard.ninerealms.com/v2/doc](https://midgard.ninerealms.com/v2/doc)
  - [https://midgard.thorchain.liquify.com/v2/doc](https://midgard.thorchain.liquify.com/v2/doc)
- **Stagenet:**
  - [https://stagenet-midgard.ninerealms.com/v2/doc](https://stagenet-midgard.ninerealms.com/v2/doc)

## THORNode

THORNode provides raw blockchain data (e.g., balances, transactions) specific to THORChain’s state machine, critical for wallets and block explorers. Avoid excessive queries to public nodes to prevent overloading.

- **Mainnet (for post-hard-fork blocks 4786560 and later):**
  - [https://thornode.thorswap.net/thorchain/doc](https://thornode.thorswap.net/thorchain/doc)
  - [https://thornode.ninerealms.com/thorchain/doc](https://thornode.ninerealms.com/thorchain/doc)
  - [https://thornode.thorchain.liquify.com/thorchain/doc](https://thornode.thorchain.liquify.com/thorchain/doc)
  - **Pre-hard-fork blocks 4786559 and earlier**\
    [https://thornode-v0.ninerealms.com/thorchain/doc](https://thornode-v0.ninerealms.com/thorchain/doc)
- **Stagenet:**
  - [https://stagenet-thornode.ninerealms.com/thorchain/doc](https://stagenet-thornode.ninerealms.com/thorchain/doc)

## Cosmos RPC

Cosmos RPC provides generic Cosmos SDK data (e.g., account balances, transactions). Common endpoints include `/cosmos/bank/v1beta1/balances` and `/cosmos/base/tendermint/v1beta1/blocks`. Not all endpoints are enabled.

### Cosmos Documentation

- Cosmos SDK v0.50 RPC - [Cosmos gRPC Guide](https://docs.cosmos.network/v0.50/learn/advanced/grpc_rest)

### Cosmos Endpoints

Use THORNode URLs with `/cosmos/...` paths.

- **Example**
  - **Mainnet** [https://thornode.ninerealms.com/cosmos/bank/v1beta1/balances/thor1dheycdevq39qlkxs2a6wuuzyn4aqxhve4qxtxt](https://thornode.ninerealms.com/cosmos/bank/v1beta1/balances/thor1dheycdevq39qlkxs2a6wuuzyn4aqxhve4qxtxt)
  - **Stagenet** [https://stagenet-thornode.ninerealms.com/cosmos/bank/v1beta1/balances/sthor1qhm0wjsrlw8wpvzrnpj8xxqu87tcucd6h98le4](https://stagenet-thornode.ninerealms.com/cosmos/bank/v1beta1/balances/sthor1qhm0wjsrlw8wpvzrnpj8xxqu87tcucd6h98le4)

## Tendermint RPC

Tendermint (CometBFT) RPC provides consensus and node status data (e.g., block height, validator status), useful for monitoring and debugging.

### Tendermint Documentation

- CometBFT v0.38 RPC - [CometBFT RPC Guide](https://docs.cometbft.com/v0.38/rpc/)

### Ports

- MAINNET Port: `27147`
- STAGENET Port: `26657`

### Tendermint RPC Endpoints

- **Mainnet:(for post-hard-fork blocks 4786560 and later)**
  - [https://rpc.ninerealms.com](https://rpc.ninerealms.com)
  - [https://rpc.thorchain.liquify.com/genesis](https://rpc.thorchain.liquify.com/genesis)
  - [https://rpc.thorswap.net/](https://rpc.thorswap.net/)
- **Mainnet:Pre-hard-fork blocks 4786559 and earlier.**
  - [https://rpc-v0.ninerealms.com](https://rpc-v0.ninerealms.com)
- **Stagenet:**
  - [https://stagenet-rpc.ninerealms.com](https://stagenet-rpc.ninerealms.com/)

## P2P

P2P is the peer-to-peer network layer for node communication, useful for debugging connectivity issues between THORChain nodes.

- MAINNET Port: `27146`
- STAGENET Port: `26656`

## P2P Guide

- [https://docs.tendermint.com/master/spec/rpc/](https://docs.tendermint.com/master/spec/rpc/)

### Example: Check Peer Connections

```bash
curl -X GET "https://rpc.ninerealms.com/net_info" -H "accept: application/json"
```
