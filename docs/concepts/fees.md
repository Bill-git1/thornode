# Fees

## Overview

There are 4 different fees the user should know about.

1. Inbound Fee (sourceChain: gasRate \* txSize)
2. Affiliate Fee (affiliateFee \* swapAmount)
3. Liquidity Fee (swapSlip \* swapAmount)
4. Outbound Fee (destinationChain: gasRate \* txSize)

### **Terms**

- **SourceChain**: the chain the user is swapping from
- **DestinationChain**: the chain the user is swapping to txSize: the size of the transaction in bytes (or units)
- **gasRate**: the current gas rate of the external network
- **swapAmount**: the amount the user is swapping swapSlip: the slip created by the as a function of poolDepth
- **affiliateFee**: optional fee set by interface in basis points

## Fee Ordering for Swaps

Fees are taken in the following order when conducting a swap.

1. Inbound Fee (user wallet controlled, not THORChain controlled)
1. Swap Fee (denoted in output asset)
1. Affiliate Fee (if any)
1. Outbound Fee (taken from the swap output)

To work out the total fees, fees should be converted to a common asset (e.g. RUNE or USD) then added up. Total fees should be less than the input else it is likely to result in a refund.

```admonish info
Because the affiliate fee is deducted after the Swap Fee, using streaming swaps is recommended for better swap efficiency.
```

## Fees Details

### Inbound Fee

This is the fee the user pays to make a transaction on the source chain, which the user pays directly themselves. The gas rate recommended to use is `fast` where the tx is guaranteed to be committed in the next block. Any longer and the user will be waiting a long time for their swap and their price will be invalid (thus they may get an unnecessary refund).

$$
inboundFee = txSize * gasRate
$$

```admonish success
THORChain calculates and posts fee rates at [`https://thornode.ninerealms.com/thorchain/inbound_addresses`](https://thornode.ninerealms.com/thorchain/inbound_addresses)

Gas Rate is calculated by taking the highest gas rate over the last 10 blocks then times 1.5.
```

```admonish warning
Always use a "fast" or "fastest" fee, if the transaction is not confirmed in time, it could be abandoned by the network or failed due to old prices. You should allow your users to cancel or re-try with higher fees.
```

### Liquidity Fee

This is simply the slip created by the transaction multiplied by its amount. It is priced and deducted from the destination amount automatically by the protocol.

$$
slip = \frac{swapAmount}{swapAmount + poolDepth}
$$

$$
fee =slip * swapAmount
$$

See more information in the [Liquidity Section]

```admonish warning
A minimum swap fee in basis points (bps) applies for different asset types, governed by the [mimir network settings](../mimir.md#swapping).
```

### Affiliate Fee

Within the transactions you build for your users you can include an affiliate for your exchange.

- Affiliate fees are possible for: swaps, saving despoit, lending addition, RUNEPool withdrawal.
- The affiliate fee is in basis points (0-10,000) and will be deducted from the inbound or outbound transaction amount.
- A THORName is required to collect affiliate address. See a guide on creating THORNames [here](../affiliate-guide/thorname-guide.md).
- Affiliates are paid in $RUNE by default however a [preferred asset](../affiliate-guide/thorname-guide.md#preferred-asset-for-affiliate-fees) can be specified within the THORName.
- Mupiple Affiliates are possible for swaps.

$$
affliateFee = \frac{feeInBasisPoints * txAmount}{10000}
$$

See the [Affiliate Fee Guide](../affiliate-guide/affiliate-fee-guide.md) for more information.

### Outbound Fee

This is the fee the Network pays on behalf of the user to send the outbound transaction. To adequately pay for network resources (TSS, compute, state storage) the fee is marked up from what nodes actually pay on-chain by an "Outbound Fee Multiplier" (OFM).

The OFM moves between a `MaxOutboundFeeMultiplier` and a `MinOutboundFeeMultiplier`(defined as [Network Constants](https://gitlab.com/thorchain/thornode/-/blob/develop/constants/constants_v1.go) or as [Mimir Values](https://thornode.ninerealms.com/thorchain/mimir)), based on the network's current outbound fee "surplus" in relation to a "target surplus". The outbound fee "surplus" is the cumulative difference (in $RUNE) between what the users are charged for outbound fees and what the nodes actually pay. As the network books a "surplus" the OFM slowly decreases from the Max to the Min. Current values for the OFM can be found on the [Network Endpoint](https://thornode.ninerealms.com/thorchain/network).

$$
outboundFee = txSize * gasRate * OFM
$$

The minimum Outbound Layer1 Fee the network will charge is on `/thorchain/mimir` and is priced in USD (based on THORChain's USD pool prices). This means really cheap chains still pay their fair share. It is currently set to `100000000` = $1.00

See [Outbound Fee](https://docs.thorchain.org/how-it-works/fees#outbound-fee) for more information.

### Refunds and Minimum Swappable Amount

If a transaction fails, it is refunded, thus it will pay the `outboundFee` for the **SourceChain** not the DestinationChain. Thus devs should always swap an amount that is a maximum of the following, multiplier by a buffer of at least 4x to allow for sudden gas spikes:

1. The Destination Chain outbound_fee
2. The Source Chain outbound_fee
3. $1.00 (the minimum)

The outbound_fee for each chain is returned on the [Inbound Addresses](https://thornode.ninerealms.com/thorchain/inbound_addresses) endpoint, priced in the gas asset.

It is strongly recommended to use the `recommended_min_amount_in` value that is included on the [Swap Quote](broken-reference) endpoint, which is the calculation described above. This value is priced in the inbound asset of the quote request (in 1e8). This should be the minimum-allowed swap amount for the requested quote.

_Remember, if the swap limit is not met or the swap is otherwise refunded the outbound_fee of the Source Chain will be deducted from the input amount, so give your users enough room._

### Understanding gas_rate

THORNode keeps track of current gas prices. Access these at the `/inbound_addresses` endpoint of the [THORNode API](./connecting-to-thorchain.md#thornode). The response is an array of objects like this:

```json
{
    "chain": "ETH",
    "pub_key": "thorpub1addwnpepqfzafst6y2f33pdvheq6qe25xyzrwy542m4tq0nfnh6cn67d56n3g3lfwej",
    "address": "0x215520b3943c89e4fa501902ef7b76fdd199023b",
    "router": "0xD37BbE5744D730a1d98d8DC97c42F0Ca46aD7146",
    "halted": false,
    "global_trading_paused": false,
    "chain_trading_paused": false,
    "chain_lp_actions_paused": false,
    "gas_rate": "60",
    "gas_rate_units": "gwei",
    "outbound_tx_size": "100000",
    "outbound_fee": "180200",
    "dust_threshold": "0
}
```

The `gas_rate` property can be used to estimate network fees for each chain the swap interacts with. For example, if the swap is `BTC -> ETH` the swap will incur fees on the bitcoin network and Ethereum network. The `gas_rate` property works differently on each chain "type" (e.g. EVM, UTXO, BFT).

The `gas_rate_units` explain what the rate is for chain, as a prompt to the developer.

The `outbound_tx_size` is what THORChain internally budgets as a typical transaction size for each chain.

The `outbound_fee` is `gas_rate * outbound_tx_size * OFM` and developers can use this to budget for the fee to be charged to the user. The current Outbound Fee Multiplier (OFM) can be found on the [Network Endpoint](https://thornode.ninerealms.com/thorchain/network).

Keep in mind the `outbound_fee` is priced in the gas asset of each chain. For chains with tokens, be sure to convert the `outbound_fee` to the outbound token to determine how much will be taken from the outbound amount. To do this, use the `getValueOfAsset1InAsset2` formula described in the [`Math`](./math.md#example) section.

## Fee Calculation by Chain

### **THORChain (Native Rune)**

The THORChain blockchain has a set 0.02 RUNE fee. This is set within the THORChain [Constants](https://thornode.ninerealms.com/thorchain/constants) by `NativeTransactionFee`. As THORChain is 1e8, `2000000 TOR = 0.02 RUNE`

### UTXO Chains like Bitcoin

For UXTO chains link Bitcoin, `gas_rate`is denoted in Satoshis. The `gas_rate` is calculated by looking at the average previous block fee seen by the THORNodes.

All THORChain transactions use BECH32 so a standard tx size of 250 bytes can be used. The standard UTXO fee is then `gas_rate`\* 250.

### EVM Chains like Ethereum

For EVM chains like Ethereum, `gas_rate`is denoted in GWEI. The `gas_rate` is calculated by looking at the average previous block fee seen by the THORNodes

An Ether Tx fee is: `gasRate * 10^9 (GWEI) * 21000 (units).`

An ERC20 Tx is larger: `gasRate * 10^9 (GWEI) * 70000 (units)`

```admonish success
THORChain calculates and posts gas fee rates at [`https://thornode.ninerealms.com/thorchain/inbound_addresses`](https://thornode.ninerealms.com/thorchain/inbound_addresses)
```
