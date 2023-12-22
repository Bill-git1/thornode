# Transaction Memos

## Overview

Transactions to THORChain pass user intent with the `MEMO` field on their respective chains. THORChain inspects the transaction object and the `MEMO` in order to process the transaction, so care must be taken to ensure the `MEMO` and the transaction are both valid. If not, THORChain will automatically refund the assets. All memos are listed [here](https://gitlab.com/thorchain/thornode/-/blob/develop/x/thorchain/memo/memo.go#L41).

THORChain uses specific [Asset Notation](asset-notation.md) for all assets. Assets and functions can be abbreviated and Affiliate Addresses and asset amounts can be shortened to [reduce memo length](memo-length-reduction.md).

Guides have been created for [Swap](../swap-guide/quickstart-guide.md), [Savers](../saving-guide/quickstart-guide.md) and [Lending](../lending/quick-start-guide.md) to enable quoting and the automatic construction of memos for simplicity.

### Memo Size Limits

THORChain has a [memo size limit of 250 bytes](https://gitlab.com/thorchain/thornode/-/blob/develop/constants/constants.go?ref_type=heads#L32), any inbound tx sent with a larger memo will be ignored. Additionally, memos on UTXO chains are further constrained by the `OP_RETURN` size limit, which is [80 bytes](https://developer.bitcoin.org/devguide/transactions.html#null-data).

## Format

All memos follow the format: `FUNCTION:PARAM1:PARAM2:PARAM3:PARAM4`

The function is invoked by a string, which in turn calls a particular handler in the state machine. The state machine parses the memo looking for the parameters which it simply decodes from human-readable strings.

In addition, some parameters are optional. Simply leave them blank, but retain the `:` separator:

`FUNCTION:PARAM1:::PARAM4`

## Permitted Functions

The following functions can be put into a memo:

1. [**SWAP**](memos.md#swap)
2. [**DEPOSIT** **Savers**](memos.md#deposit-savers)
3. [**WITHDRAW Savers**](memos.md#withdraw-savers)
4. [**OPEN** **Loan**](memos.md#open-loan)
5. [**REPAY Loan**](memos.md#repay-loan)
6. [**ADD** **Liquidity**](memos.md#add-liquidity)
7. [**WITHDRAW** **Liquidity**](memos.md#withdraw-liquidity)
8. [**BOND**, **UNBOND** & **LEAVE**](memos.md#bond-unbond-and-leave)
9. [**DONATE** & **RESERVE**](memos.md#donate-and-reserve)
10. MIGRATE
11. [**NOOP**](memos.md#noop)

### Swap

Perform a swap.

**`SWAP:ASSET:DESTADDR:LIM/INTERVAL/QUANTITY:AFFILIATE:FEE`**

Perform a swap.

**`SWAP:ASSET:DESTADDR:LIM/INTERVAL/QUANTITY:AFFILIATE:FEE`**

| Parameter    | Notes                                                                                 | Conditions                                                                            |
| ------------ | ------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| Payload      | Send the asset to swap.                                                               | Must be an active pool on THORChain.                                                  |
| `SWAP`       | The swap handler.                                                                     | [Also](memo-length-reduction.md#mechanism-for-transaction-intent-2) `s`, `=`          |
| `:ASSET`     | The [asset identifier](asset-notation.md).                                            | Can be [shortened](memo-length-reduction.md#shortened-asset-names).                   |
| `:DESTADDR`  | The destination address to send to.                                                   | Can use THORName.                                                                     |
| `:LIM`       | The trade limit, i.e., set 100000000 to get a minimum of 1 full asset, else a refund. | Optional, 1e8 or [Scientific Notation](memo-length-reduction.md#scientific-notation). |
| `/INTERVAL`  | Swap interval in blocks.                                                              | Optional, 1 means do not stream.                                                      |
| `/QUANTITY`  | Swap Quantity. Swap interval times every Interval blocks.                             | Optional, if 0, network will determine the number of swaps.                           |
| `:AFFILIATE` | The [affiliate address](fees.md#affiliate-fee). RUNE is sent to Affiliate.            | Optional. Must be THORName or THOR Address.                                           |
| `:FEE`       | The affiliate fee. Limited from 0 to 1000 Basis Points.                               | Optional. Limited from 0 to 1000 Basis Points.                                        |

**Syntactic Examples:**

- **`SWAP:ASSET:DESTADDR`** simple swap
- **`SWAP:ASSET:DESTADDR:LIM`** swap with trade limit
- **`SWAP:ASSET:DESTADDR:LIM/1/1`** swap with limit, do not stream swap
- **`SWAP:ASSET:DESTADDR:LIM/3/0`** swap with limit, optimise swap amount, every 3 blocks
- **`SWAP:ASSET:DESTADDR:LIM/1/0:AFFILIATE:FEE`** swap with limit, optimised and affiliate fee

**Actual Examples:**

- `SWAP:ETH.ETH:0xe6a30f4f3bad978910e2cbb4d97581f5b5a0ade0` - swap to Ether, send output to the specified address.
- `SWAP:ETH.ETH:0xe6a30f4f3bad978910e2cbb4d97581f5b5a0ade0:10000000,` same as above except the Ether output should be more than 0.1 Ether else refund.
- `SWAP:ETH.ETH:0xe6a30f4f3bad978910e2cbb4d97581f5b5a0ade0:10000000/1/1,` same as above except explicitly stated, do not stream the swap.
- `SWAP:ETH.ETH:0xe6a30f4f3bad978910e2cbb4d97581f5b5a0ade0:10000000/3/0,` same as above except told to allow streaming swap, mini swap every 3 blocks and THORChain to work out the number of swaps required to achieve optimal price efficiency.
- `SWAP:ETH.ETH:0xe6a30f4f3bad978910e2cbb4d97581f5b5a0ade0:10000000/3/0:t:10`- same as above except will send 10 basis points from the input and send it to `t` (THORSwap's [THORName](../affiliate-guide/thorname-guide.md)).

The above memo can be further [reduced](memo-length-reduction.md) to:

`=:ETH.ETH:0xe6a30f4f3bad978910e2cbb4d97581f5b5a0ade0:1e6/3/0:t:10`

**Other examples:**

- `=:r:thor1el4ufmhll3yw7zxzszvfakrk66j7fx0tvcslym:19779138111` - swap to at least 197.79 RUNE
- `=:BNB/BUSD-BD1:thor15s4apx9ap7lazpsct42nmvf0t6am4r3w0r64f2:628197586176 -` Swap to at least 6281.9 Synthetic BUSD.
- `=:BNB.BNB:bnb108n64knfm38f0mm23nkreqqmpc7rpcw89sqqw5:544e6/2/6` - swap to at least 5.4 BNB, using streaming swaps, six swaps, every two blocks.

### **Deposit Savers**

**`ADD:POOL::AFFILIATE:FEE`**

Depositing savers can work without a memo; however, memos are recommended to be explicit about the transaction intent.

| Parameter    | Notes                                                                      | Conditions                                     |
| ------------ | -------------------------------------------------------------------------- | ---------------------------------------------- |
| Payload      | The asset to add liquidity with.                                           | Must be supported by THORChain.                |
| `ADD`        | The Deposit handler.                                                       | Also `a` `+`                                   |
| `:POOL`      | The pool to add liquidity to.                                              | Gas and stablecoin pools only.                 |
| `:`          | Must be empty                                                              | Optional, Required if adding affiliate and fee |
| `:AFFILIATE` | The [affiliate](fees.md#affiliate-fee) address. RUNE is sent to Affiliate. | Optional. Must be THORName or THOR Address.    |
| `:FEE`       | The affiliate fee. Limited from 0 to 1000 Basis Points.                    | Optional. Limited from 0 to 1000 Basis Points. |

**Examples:**

- `+:BTC/BTC` add to the BTC Savings Vault
- `a:ETH/ETH` add to the ETH Savings Vault
- `+:BTC/BTC::t:10` Deposit with a 10 basis points affiliate

### Withdraw Savers

**`WITHDRAW:POOL`**

| Parameter      | Notes                                                                                                                                                        | Extra                                                                                                                                                    |
| -------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Payload        | Send the [dust threshold](https://midgard.ninerealms.com/v2/thorchain/inbound_addresses) of the asset to cause the transaction to be picked up by THORChain. | Caution [Dust Limits](https://midgard.ninerealms.com/v2/thorchain/inbound_addresses): BTC,BCH,LTC chains 10k sats; DOGE 1m Sats; ETH 0 wei; THOR 0 RUNE. |
| `WITHDRAW`     | The withdraw handler.                                                                                                                                        | Also `-` `wd`                                                                                                                                            |
| `:POOL`        | The pool to withdraw liquidity from.                                                                                                                         | Gas and stablecoin pools only.                                                                                                                           |
| `:BASISPOINTS` | Basis points (0-10000, where 10000=100%).                                                                                                                    | Optional. Limited from 0 to 1000 Basis Points.                                                                                                           |

**Examples:**

- `-:BTC/BTC:10000` Withdraw 100% from BTC Savers
- `w:ETH/ETH:5000` Withdraw 50% from ETH Savers

### **Open Loan**

**`LOAN+:ASSET:DESTADDR:MINOUT:AFFILIATE:FEE`**

| Parameter    | Notes                                                                                        | Conditions                                     |
| ------------ | -------------------------------------------------------------------------------------------- | ---------------------------------------------- |
| Payload      | The collateral to open the loan with.                                                        | Must be L1 supported by THORChain.             |
| `LOAN+`      | The Loan Open handler.                                                                       | also `$+`                                      |
| `:ASSET`     | Target debt [asset identifier](asset-notation.md).                                           | Can be [shortened](memo-length-reduction.md).  |
| `:DESTADDR`  | The destination address to send the debt to.                                                 | Can use THORName.                              |
| `:MINOUT`    | Similar to LIM, Min debt amount, else a refund.                                              | Optional, 1e8 format.                          |
| `:AFFILIATE` | The [affiliate](fees.md#affiliate-fee) address. The affiliate is added to the pool as an LP. | Optional. Must be THORName or THOR Address.    |
| `:FEE`       | The affiliate fee. Fee is allocated to the affiliate.                                        | Optional. Limited from 0 to 1000 Basis Points. |

```admonish warning
Affiliate and Affiliate Fee yet to be implemented
```

**Examples:**

- `$+:BNB.BUSD:bnb177kuwn6n9fv83txq04y2tkcsp97s4yclz9k7dh` - Open a loan with BUSD as the debt asset
- `$+:ETH.USDC-0XA0B86991C6218B36C1D19D4A2E9EB0CE3606EB48:0x1c7b17362c84287bd1184447e6dfeaf920c31bbe:10400000000` Open a loan where the debt is at least 104 USDT.

### **Repay Loan**

**`LOAN-:ASSET:DESTADDR:MINOUT`**

| Parameter   | Notes                                                    | Conditions                                                    |
| ----------- | -------------------------------------------------------- | ------------------------------------------------------------- |
| Payload     | The repayment for the loan.                              | Must be L1 supported on THORChain.                            |
| `LOAN-`     | The Loan Repayment handler.                              | also `$-`                                                     |
| `:ASSET`    | Target collateral [asset identifier](asset-notation.md). | Can be [shortened](memo-length-reduction.md).                 |
| `:DESTADDR` | The destination address to send the collateral to.       | Can use THORName.                                             |
| `:MINOUT`   | Similar to LIM, Min collateral to receive else a refund. | Optional, 1e8 format, loan needs to be fully repaid to close. |

**Examples:**

- `LOAN-:BTC.BTC:bc1qp2t4hl4jr6wjfzv28tsdyjysw7p5armf7px55w` Repay BTC loan owned by owner bc1qp2t4hl4jr6wjfzv28tsdyjysw7p5armf7px55w.
- `LOAN-:ETH.ETH:0xe9973cb51ee04446a54ffca73446d33f133d2f49:404204059`. Repay ETH loan owned by `0xe9973cb51ee04446a54ffca73446d33f133d2f49` and receive at least 4.04 ETH collateral back, else send back a refund.

### Add Liquidity

There are rules for adding liquidity, see [the rules here](https://docs.thorchain.org/learn/getting-started#entering-and-leaving-a-pool).\
**`ADD:POOL:PAIREDADDR:AFFILIATE:FEE`**

| Parameter     | Notes                                                                                                                                                                                                                                  | Conditions                                                                  |
| ------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------- |
| Payload       | The asset to add liquidity with.                                                                                                                                                                                                       | Must be supported by THORChain.                                             |
| `ADD`         | The Add Liquidity handler.                                                                                                                                                                                                             | also `a` `+`                                                                |
| `:POOL`       | The pool to add liquidity to.                                                                                                                                                                                                          | Can be [shortened](memo-length-reduction.md).                               |
| `:PAIREDADDR` | The other address to link with. If on external chain, link to THOR address. If on THORChain, link to external address. If a paired address is found, the LP is matched and added. If none is found, the liquidity is put into pending. | Optional. If not specified, a single-sided add-liquidity action is created. |
| `:AFFILIATE`  | The [affiliate](fees.md#affiliate-fee) address. The affiliate is added to the pool as an LP.                                                                                                                                           | Optional. Must be THORName or THOR Address.                                 |
| `:FEE`        | The affiliate fee. Fee is allocated to the affiliate.                                                                                                                                                                                  | Optional. Limited from 0 to 1000 Basis Points.                              |

**Examples:**

- **`ADD:POOL`** single-sided add liquidity. If this is a position's first add, liquidity can only be withdrawn to the same address.
- **`+:POOL:PAIREDADDR`** add on both sides.
- **`+:POOL:PAIREDADDR:AFFILIATE:FEE`** add with affiliate
- `+:BTC.BTC:`

### Withdraw Liquidity

Withdraw liquidity from a pool.\
A withdrawal can be either dual-sided (withdrawn based on pool's price) or entirely single-sided (converted to one side and sent out).

**`WD:POOL:BASISPOINTS:ASSET`**

| Parameter      | Notes                                                                                       | Extra                                                                                                                                                    |
| -------------- | ------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Payload        | Send the dust threshold of the asset to cause the transaction to be picked up by THORChain. | Caution [Dust Limits](https://midgard.ninerealms.com/v2/thorchain/inbound_addresses): BTC,BCH,LTC chains 10k sats; DOGE 1m Sats; ETH 0 wei; THOR 0 RUNE. |
| `WITHDRAW`     | The withdraw handler.                                                                       | Also `-` `wd`                                                                                                                                            |
| `:POOL`        | The pool to withdraw liquidity from.                                                        | Can be [shortened](memo-length-reduction.md).                                                                                                            |
| `:BASISPOINTS` | Basis points (0-10000, where 10000=100%).                                                   |                                                                                                                                                          |
| `:ASSET`       | Single-sided withdraw to one side.                                                          | Optional. Can be shortened. Must be either RUNE or the ASSET.                                                                                            |

**Examples:**

- **`WITHDRAW:POOL:10000`** dual-sided 100% withdraw liquidity. If a single-address position, this withdraws single-sidedly instead.
- **`-:POOL:1000`** dual-sided 10% withdraw liquidity.
- **`wd:POOL:5000:ASSET`** withdraw 50% liquidity as the asset specified while the rest stays in the pool, eg:
- `wd:BTC.BTC:5000:BTC.BTC`

### DONATE & RESERVE

Donate to a pool or the RESERVE.

**`DONATE:POOL`**

| Parameter | Notes                                    | Extra                                                 |
| --------- | ---------------------------------------- | ----------------------------------------------------- |
| Payload   | The asset to donate to a THORChain pool. | Must be supported by THORChain. Can be RUNE or ASSET. |
| `DONATE`  | The donate handler.                      | Also `%`                                              |
| `:POOL`   | The pool to withdraw liquidity from.     | Can be [shortened](memo-length-reduction.md).         |

**Example:** `DONATE:ETH.ETH` - Donate to the ETH pool.

**`RESERVE`**

| Parameter | Notes                | Extra                              |
| --------- | -------------------- | ---------------------------------- |
| Payload   | THOR.RUNE.           | The RUNE to credit to the RESERVE. |
| `RESERVE` | The reserve handler. |                                    |

### BOND, UNBOND & LEAVE

Perform node maintenance features. Also see [Pooled Nodes](https://docs.thorchain.org/thornodes/pooled-thornodes).

**`BOND:NODEADDR:PROVIDER:FEE`**

| Parameter   | Notes                                    | Extra                                                                                  |
| ----------- | ---------------------------------------- | -------------------------------------------------------------------------------------- |
| Payload     | The asset to bond to a Node.             | Must be RUNE.                                                                          |
| `BOND`      | The bond handler.                        | Anytime.                                                                               |
| `:NODEADDR` | The node to bond with.                   |                                                                                        |
| `:PROVIDER` | Whitelist in a provider.                 | Optional, add a provider                                                               |
| `:FEE`      | Specify an Operator Fee in Basis Points. | Optional, default will be the mimir value (2000 Basis Points). Can be changed anytime. |

**`UNBOND:NODEADDR:AMOUNT`**

| Parameter   | Notes                    | Extra                                                                 |
| ----------- | ------------------------ | --------------------------------------------------------------------- |
| Payload     | None required.           | Use `MsgDeposit`.                                                     |
| `UNBOND`    | The unbond handler.      |                                                                       |
| `:NODEADDR` | The node to unbond from. | Must be in standby only.                                              |
| `:AMOUNT`   | The amount to unbond.    | In 1e8 format. If setting more than actual bond, then capped at bond. |
| `:PROVIDER` | Unwhitelist a provider.  | Optional, remove a provider                                           |

**`LEAVE:NODEADDR`**

| Parameter   | Notes                       | Extra                                                                                                    |
| ----------- | --------------------------- | -------------------------------------------------------------------------------------------------------- |
| Payload     | None required.              | Use `MsgDeposit`.                                                                                        |
| `LEAVE`     | The leave handler.          |                                                                                                          |
| `:NODEADDR` | The node to force to leave. | If in Active, request a churn out to Standby for 1 churn cycle. If in Standby, forces a permanent leave. |

**Examples:**

- `BOND:thor19m4kqulyqvya339jfja84h6qp8tkjgxuxa4n4a`
- `UNBOND:thor1x2whgc2nt665y0kc44uywhynazvp0l8tp0vtu6:750000000000`
- `LEAVE:thor1hlhdm0ngr2j4lt8tt8wuvqxz6aus58j57nxnps`

### MIRGRATE

Internal memo type used to mark migration transactions between a retiring vault and a new Asgard vault during churn. Special THORChain triggered outbound tx without a related inbound tx.

**`:MIGRATE`**

| Parameter      | Notes                           | Extra                       |
| -------------- | ------------------------------- | --------------------------- |
| Payload        | Assets migrating                |                             |
| `MIGRATE`      | The migrate Handler             |                             |
| `:BlockHeight` | THORChain Blockhight to migrate | Must be a valid blockheight |

[Example](https://viewblock.io/thorchain/tx/661AA4D05E75FD60FDE340B25716B840891B13F058E1756C8C9C335067DB1D9A): `MIGRATE:3494355`

### NOOP

Dev-centric functions to fix THORChain state. Caution: may cause loss of funds if not done exactly right at the right time.

\*`NOOP`\*\*

| Parameter  | Notes                           | Extra                                                    |
| ---------- | ------------------------------- | -------------------------------------------------------- |
| Payload    | The asset to credit to a vault. | Must be ASSET or RUNE.                                   |
| `NOOP`     | The noop handler.               | Adds to the vault balance, but does not add to the pool. |
| `:NOVAULT` | Do not credit the vault.        | Optional. Just fix the insolvency issue.                 |

### Refunds

The following are the conditions for refunds:

| Condition                | Notes                                                                                                        |
| ------------------------ | ------------------------------------------------------------------------------------------------------------ |
| Invalid `MEMO`           | If the `MEMO` is incorrect the user will be refunded.                                                        |
| Invalid Assets           | If the asset for the transaction is incorrect (adding an asset into a wrong pool) the user will be refunded. |
| Invalid Transaction Type | If the user is performing a multi-send vs a send for a particular transaction, they are refunded.            |
| Exceeding Price Limit    | If the final value achieved in a trade differs to expected, they are refunded.                               |

Refunds cost fees to prevent Denial of Service attacks. The user will pay the correct outbound fee for that chain.

### **Other Internal Memos**

- `donate` - add funds to a pool (example:`DONATE:ETH.ETH`).
- `consolidate` - consolidate UTXO transactions.
- `ragnarok` - only used to delist pools.
- `yggdrasilfund` and `yggdrasilreturn` - not used as Yggdrasil vaults are no longer used (ADR 002).
- `switch` - no longer used as killswich has ended.
- `reserve` - Used to add RUNE to the Reserve Module as MsgSend to network modules is disallowed.
