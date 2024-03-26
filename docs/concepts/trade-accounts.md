# Trade Accounts

More capital-efficient trading and arbitration than Synthetics

Trade accounts provide professional traders (mostly arbitrage bots) a method to execute instant trades on THORChain without involving Layer1 transactions on external blockchains. Trade Accounts creates a new type of asset, backed by the network security rather than the liquidity in a pool (Synthetics), or by the RUNE asset (Derived Assets).

Arbitrage bots can arbitrage the pools faster and with more capital efficiency than Synthetics can. This is because Synthetics adds or removes from one side of the pool depth but not the other, causing the pool to move only half the distance in terms of price. For example a $100 RUNE --> BTC swap requires $200 of Synthetic BTC to be burned to correct the price. Trade accounts have twice the efficiency, so $100 RUNE --> BTC swap would require $100 from trade accounts to correct the price. This allows arbitrageurs to restore big deviations quicker using less capital.

## How it Works

1. Traders deposit Layer1 assets into the network, minting a [Trade Asset](./asset-notation.md#trade-assets) in a 1:1 ratio within a Network Trade module held by the network, not the user's wallet. This is held outside of the Liquidity Pools.
1. Trader receives accredited shares of this module relative to their deposit versus module depth. This is done using the same logic as savers.
1. Trader can swap/trade assets <> RUNE (or other trade asset) to and from the trade module. Because this occurs completely within THORNode, execution times are fast and efficient. Swap fees are the same as any other Layer1 swap.
1. Trader can withdraw some or all of their balance from their Trade Account. [Outbound delay](./delays.md) applies when they withdraw.

RUNE and Synthetics cannot be added to the Trade Account.

## Security

As assets within the trade account are not held in the pools, the combined pool and trade account value (combined Layer1 asset value) could exceed the total bonded. To ensure this does not occur:

1. The calculation of the Incentive Pendulum now operates based on Layer1 assets versus bonds, rather than solely on pool depths versus bonds. This ensures there is always "space" for arbitrageurs to exist in the network and be able to arbitrage pools effectively (versus synths hitting caps).
1. If the combined Layer1 asset value exceeds the total bonded value, trade assets are sold/liquidated (reducing liability) to buy RUNE and are deposited into the bond module (increasing security). In this scenario a Trade Account may be subject to a negative interest rate. This safeguard effectively redistributes liquidity from all Trade Account holders to Active Node Operators and only occurs if the [Incentive Pendulum](https://docs.thorchain.org/how-it-works/incentive-pendulum) reaches a fully underbonded state.

```admonish warning
Trade Accounts are yet to be activated.
```
