# Token Lists and Whitelists

THORChain (and MAYA Protocol) restrict support for tokens on EVM and Solana chains to explicitly whitelisted contracts. This protects users from malicious or incompatible tokens.

## Purpose of Token Whitelists

- Prevent unsupported tokens from being deposited, swapped, or added to pools
- Avoid edge-case ERC-20 behavior (e.g., tokens with transfer fees, blacklists, or non-standard returns)
- Provide transparency to integrators and wallets

## Whitelist Enforcement

- Applies to EVM chains (ETH, AVAX, BSC, ARB, BASE) and Solana (coming soon)
- If a token is not on the list, it will be rejected at the Router contract level
- Tokens must be listed to be eligible for THORChain pools, swaps, and LP adds

## Whitelist Sources

Each chain has its own maintained JSON whitelist:

| Chain                     | Whitelist URL                                                                                                                          |
| ------------------------- | -------------------------------------------------------------------------------------------------------------------------------------- |
| Ethereum (ETH)            | [`eth_mainnet_latest.json`](https://gitlab.com/thorchain/thornode/-/raw/develop/common/tokenlist/ethtokens/eth_mainnet_latest.json)    |
| Avalanche (AVAX)          | [`avax_mainnet_latest.json`](https://gitlab.com/thorchain/thornode/-/raw/develop/common/tokenlist/avaxtokens/avax_mainnet_latest.json) |
| Binance Smart Chain (BSC) | [`bsc_mainnet_latest.json`](https://gitlab.com/thorchain/thornode/-/raw/develop/common/tokenlist/bsctokens/bsc_mainnet_latest.json)    |
| Arbitrum (ARB)            | [`arb_mainnet_latest.json`](https://gitlab.com/mayachain/mayanode/-/raw/mainnet/common/tokenlist/arbtokens/arb_mainnet_latest.json)    |
| Base (BASE)               | [`base_mainnet_latest.json`](https://gitlab.com/thorchain/thornode/-/raw/develop/common/tokenlist/basetokens/base_mainnet_latest.json) |
| Solana (SOL)              | _Pending integration_                                                                                                                  |

**Note**: Some tokens like $USDT or $USDC exist across multiple chains and may require separate listing per chain.

## How to Request New Tokens

To add a token:

1. Fork the appropriate repository (e.g., [THORNode](https://gitlab.com/thorchain/thornode))
2. Locate the relevant token list (e.g., `common/tokenlist/ethtokens/eth_mainnet_latest.json`)
3. Add the token with required metadata (name, symbol, address, decimals)
4. Submit a merge request for review

See example MR: [thornode!2085](https://gitlab.com/thorchain/thornode/-/merge_requests/2085/diffs)

## Token Support on Solana

Solana uses SPL tokens, which follow a different format than ERC-20. A similar whitelist approach will apply, with support based on:

- Token mint address
- Decimals
- Associated metadata (e.g., name, symbol)

More details will be provided once Solana support is fully launched.
