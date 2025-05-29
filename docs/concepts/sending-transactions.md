# Sending Transactions

Confirm you have:

- [ ] Connected to Midgard or THORNode
- [ ] Located the latest vault (and router) for the chain
- [ ] Prepared the transaction details (and memo)
- [ ] Checked the network is not halted for your transaction

You are ready to make the transaction and swap via THORChain.

## UTXO Chains

### Memo less or equal 80 characters

- [ ] Ensure the [address type](./querying-thorchain.md#supported-address-formats) is supported
- [ ] Send the transaction with Asgard vault as VOUT0
- [ ] Pass all change back to the VIN0 address in a subsequent VOUT e.g. VOUT1
- [ ] Include the memo as an OP_RETURN in a subsequent VOUT e.g. VOUT2
- [ ] Use a high enough `gas_rate` to be included
- [ ] Do not make deposits below the dust threshold (e.g., 10k sats for BTC, BCH, LTC; 1 DOGE) into the vault. See [Dust Thresholds and Transaction Validation](#dust-thresholds-and-transaction-validation) for exhaustive values.
- [ ] Do not send funds that are part of a transaction with more than 10 outputs

### Memo greater than 80 characters

- [ ] Ensure the [address type](./querying-thorchain.md#supported-address-formats) is supported
- [ ] Send the transaction with Asgard vault as VOUT0
- [ ] Pass all change back to the VIN0 address in a subsequent VOUT e.g. VOUT1
- [ ] Take the first 79 characters of the memo and append '^' and use that as an OP_RETURN in a subsequent VOUT e.g. VOUT2
- [ ] Add remaining characters encoded as p2wpkh address as subsequent VOUT
  - [ ] encode remaining characters to hex representation
  - [ ] split the resulting string into chunks of 40 characters each, append "00" to the last chunk until its length also matches 40 characters
  - [ ] for each hex encoded chunk, create a VOUT sending the [minimum allowed amount of sats](../../x/thorchain/querier_quotes.go#L39) for the specific chain to the script pub key: '0014' + `<chunk>`
- [ ] Use a high enough `gas_rate` to be included
- [ ] Do not send below the dust threshold (10k Sats BTC, BCH, LTC, 1m DOGE), exhaustive values can be found on the [Inbound Addresses](https://thornode.ninerealms.com/thorchain/inbound_addresses) endpoint
- [ ] Do not send funds that are part of a transaction with more than 10 outputs

### Examples using dummy txs

#### P2WPKH (BTC, LTC)

Memo:
`SWAP:GAIA.ATOM:cosmos1fegapd4jc3ejqeg0eu3jk4hvr74hg66076gyyd/bc1qnfw0gkk05qxl38mslc69hc6vc64mksyw6zzxhg`

1. Put first 79 characters + `^` into `OP_RETURN`: `SWAP:GAIA.ATOM:cosmos1fegapd4jc3ejqeg0eu3jk4hvr74hg66076gyyd/bc1qnfw0gkk05qxl38^`
2. Hex encode remaining string `mslc69hc6vc64mksyw6zzxhg` and split it into chunks of 40 characters (fill the last chunk with zeros): `6d736c633639686336766336346d6b737977367a`, `7a78686700000000000000000000000000000000`
3. Create two subsequent VOUTs (prepend `0014`):
   1. send 294sats (2940sats on LTC) to: `00146d736c633639686336766336346d6b737977367a`
      - BTC: bc1qd4ekccek895xxdnkvvmrgmttwduhwdn622k6gj
      - LTC: ltc1qd4ekccek895xxdnkvvmrgmttwduhwdn6wkv7sz
   2. send 294sats (2940sats on LTC) to: `00147a78686700000000000000000000000000000000`
      - BTC: bc1q0fuxsecqqqqqqqqqqqqqqqqqqqqqqqqq2alhdv
      - LTC: ltc1q0fuxsecqqqqqqqqqqqqqqqqqqqqqqqqqwp9n4u

#### P2PKH (BCH, DOGE)

Memo:
`SWAP:GAIA.ATOM:cosmos1fegapd4jc3ejqeg0eu3jk4hvr74hg66076gyyd/bc1qnfw0gkk05qxl38mslc69hc6vc64mksyw6zzxhg`

1. Put first 79 characters + `^` into `OP_RETURN`: `SWAP:GAIA.ATOM:cosmos1fegapd4jc3ejqeg0eu3jk4hvr74hg66076gyyd/bc1qnfw0gkk05qxl38^`
2. Hex encode remaining string `mslc69hc6vc64mksyw6zzxhg` and split it into chunks of 40 characters (fill the last chunk with zeros): `6d736c633639686336766336346d6b737977367a`, `7a78686700000000000000000000000000000000`
3. Create two subsequent VOUTs (prepend `76a914` & append `88ac`):
   1. send 546sats to: `76a9146d736c633639686336766336346d6b737977367a88ac`
      - BCH: qpkhxmrrxcukscekwe3nvdrdddehjaek0gldczy2mv
      - DOGE: DF7pTvdzyY2zoVJ3AFQr8oSYDiqw3m6hCy
   2. send 546sats to: `76a9147a7868670000000000000000000000000000000088ac`
      - BCH: qpa8s6r8qqqqqqqqqqqqqqqqqqqqqqqqqq99h3g9e2
      - DOGE: DGJfDk6cwyNpzebP7MyzueRtXEWGEaaHz9

```admonish warning
Inbound transactions should not be delayed for any reason else there is risk funds will be sent to an unreachable address. Use standard transactions, check the [`Inbound_Address`](querying-thorchain.md#getting-the-asgard-vault) before sending and use the recommended [`gas rate`](querying-thorchain.md#getting-the-asgard-vault) to ensure transactions are confirmed in the next block to the latest `Inbound_Address`.
```

```admonish info
Memo limited to 80 bytes in `OP_RETURN` on BTC, BCH, LTC and DOGE. Use [abbreviated options](./memo-length-reduction.md) where possible.
```

```admonish warning
Do not use HD wallets that forward the change to a new address, because THORChain IDs the user as the address in VIN0. The user must keep their VIN0 address funded for refunds.
```

```admonish danger
Override randomised VOUT ordering; THORChain requires specific output ordering. Funds using wrong ordering are very likely to be lost.
```

### EVM Chains

[Router Contract](https://gitlab.com/thorchain/thornode/-/blob/develop/chain/ethereum/contracts/THORChain_Router.sol)

```go
depositWithExpiry(vault, asset, amount, memo, expiry)
```

- [ ] If ERC20, approve the router to spend an allowance of the token first
- [ ] Send the transaction as a `depositWithExpiry()` on the router
- [ ] Vault is the Asgard vault address, asset is the token address to swap, memo as a string
- [ ] Use an expiry which is +60mins on the current time (if the tx is delayed, it will get refunded). The timestamp is in seconds (Solidity's `block.timestamp`).
- [ ] Use a high enough `gas_rate` to be included, otherwise the tx will get stuck

```admonish info
ETH is `0x0000000000000000000000000000000000000000`
```

```admonish danger
ETH is sent and received as an internal transaction. Your wallet may not be set to read internal balances and transactions.
```

### BFT Chains

- [ ] Send the transaction to the Asgard vault
- [ ] Include the memo
- [ ] Only use the base asset as the choice for gas asset

### XRP Ledger

- [ ] Send the transaction to the Asgard vault
- [ ] Include the `DestinationTag` in the memo field if sending to a centralised exchange or shared address
- [ ] Include the [memo](./memos.md) with user intent in the transaction
- [ ] Use a high enough `gas_rate` to be included, as specified in the [Inbound Addresses](https://thornode.ninerealms.com/thorchain/inbound_addresses) endpoint
- [ ] Ensure the transaction amount exceeds the dust threshold; see [Dust Thresholds and Transaction Validation](#dust-thresholds-and-transaction-validation)
- [ ] Ensure the transaction is a Payment type, as THORChain only processes XRP Payment transactions
- [ ] Use the correct sequence number for the sending account, obtainable via the `GetAccount` method

## Dust Thresholds and Transaction Validation

THORChain enforces dust thresholds to prevent dust attacks, where negligible amounts are sent to clog the network. The dust threshold is the minimum transaction amount required for a Layer 1 (L1) chain to ensure THORChain processes the transaction and its associated memo. Transactions with amounts **equal to or below the dust threshold** are ignored by the network.

### Dust Threshold Rules

- **General Rule**: Transactions must exceed the chain’s dust threshold in base units (e.g., wei for EVM chains, sats for UTXO chains, uatom for GAIA) to be processed by THORChain. This ensures the network recognizes the transaction and executes the instruction specified in the memo.
- **Unit Clarification**: The `gas_rate_units` field (e.g., gwei for EVM chains, satsperbyte for UTXO chains) in the [Inbound Addresses](https://thornode.ninerealms.com/thorchain/inbound_addresses) endpoint refers to gas pricing, while dust thresholds are specified in base units.
- **Source of Truth**: Dust thresholds may be updated by the network. Always check the latest values at the [Inbound Addresses](https://thornode.ninerealms.com/thorchain/inbound_addresses) endpoint before sending a transaction.
- **Important**: Convert “human-readable” amounts (e.g., 1 BTC) to base units (e.g., 100,000,000 sats) when calculating the transaction amount to comply with the dust threshold.

### Chain-Specific Dust Thresholds

- **AVAX**: 1 gwei
- **BASE**: 1 gwei
- **BCH**: 10,000 sats
- **BSC**: 1 gwei
- **BTC**: 10,000 sats
- **DOGE**: 100,000,000 sats (1 DOGE)
- **ETH**: 1 gwei
- **GAIA**: 1 uatom
- **LTC**: 10,000 sats
- **XRP**: 1,000,000 drops (1 XRP)

```admonish warning
Ensure transaction amounts exceed the dust threshold for the chain to avoid being ignored. Verify the latest dust threshold at [Inbound Addresses](https://thornode.ninerealms.com/thorchain/inbound_addresses) endpoint before sending to ensure the amount is sufficient to trigger the desired action on THORChain.
```

## THORChain

To initiate a $RUNE -> $ASSET swap a `MsgDeposit` must be broadcasted to the THORChain blockchain. The `MsgDeposit` does not have a destination address, and has the following properties. The full definition can be found [here](https://gitlab.com/thorchain/thornode/-/blob/develop/x/thorchain/types/msg_deposit.go).

```go
MsgDeposit{
    Coins:  coins,
    Memo:   memo,
    Signer: signer,
}
```

If you are using Javascript, [CosmJS](https://github.com/cosmos/cosmjs) is the recommended package to build and broadcast custom message types. [Here is a walkthrough](https://github.com/cosmos/cosmjs/blob/main/packages/stargate/CUSTOM_PROTOBUF_CODECS.md).

### Code Examples (Javascript)

1. **Generate codec files.** To build/broadcast native transactions in Javascript/Typescript, the protobuf files need to be generated into js types. The below script uses `pbjs` and `pbts` to generate the types using the relevant files from the THORNode repo. Alternatively, the .`js` and `.d.ts` files can be downloaded directly from the [XChainJS repo](https://github.com/xchainjs/xchainjs-lib/tree/master/packages/xchain-thorchain/src/types/proto).

   ```bash
   #!/bin/bash

   # this script checks out thornode master and generates the proto3 typescript buindings for MsgDeposit and MsgSend

   MSG_COMPILED_OUTPUTFILE=src/types/proto/MsgCompiled.js
   MSG_COMPILED_TYPES_OUTPUTFILE=src/types/proto/MsgCompiled.d.ts

   TMP_DIR=$(mktemp -d)

   tput setaf 2; echo "Checking out https://gitlab.com/thorchain/thornode to $TMP_DIR";tput sgr0
   (cd $TMP_DIR && git clone https://gitlab.com/thorchain/thornode)

   # Generate msgs
   tput setaf 2; echo "Generating $MSG_COMPILED_OUTPUTFILE";tput sgr0
   yarn run pbjs -w commonjs -t static-module $TMP_DIR/thornode/proto/thorchain/v1/common/common.proto $TMP_DIR/thornode/proto/thorchain/v1/x/thorchain/types/msg_deposit.proto $TMP_DIR/thornode/proto/thorchain/v1/x/thorchain/types/msg_send.proto $TMP_DIR/thornode/third_party/proto/cosmos/base/v1beta1/coin.proto -o $MSG_COMPILED_OUTPUTFILE

   tput setaf 2; echo "Generating $MSG_COMPILED_TYPES_OUTPUTFILE";tput sgr0
   yarn run pbts $MSG_COMPILED_OUTPUTFILE -o $MSG_COMPILED_TYPES_OUTPUTFILE

   tput setaf 2; echo "Removing $TMP_DIR/thornode";tput sgr0
   rm -rf $TMP_DIR
   ```

2. **Using @cosmjs build/broadcast the TX.**

   ```javascript
   const {
     DirectSecp256k1HdWallet,
     Registry,
   } = require("@cosmjs/proto-signing");
   const {
     defaultRegistryTypes: defaultStargateTypes,
     SigningStargateClient,
   } = require("@cosmjs/stargate");
   const { stringToPath } = require("@cosmjs/crypto");
   const bech32 = require("bech32-buffer");

   const { MsgDeposit } = require("./types/MsgCompiled").types;

   async function main() {
     const myRegistry = new Registry(defaultStargateTypes);
     myRegistry.register("/types.MsgDeposit", MsgDeposit);

     const signerMnemonic = "mnemonic here";
     const signerAddr = "thor1...";

     const signer = await DirectSecp256k1HdWallet.fromMnemonic(signerMnemonic, {
       prefix: "thor", // THORChain prefix
       hdPaths: [stringToPath("m/44'/931'/0'/0/0")], // THORChain HD Path
     });

     const client = await SigningStargateClient.connectWithSigner(
       "https://rpc.ninerealms.com/",
       signer,
       { registry: myRegistry },
     );

     const memo = `=:ETH/ETH:${signerAddr}`; // THORChain memo

     const msg = {
       coins: [
         {
           asset: {
             chain: "THOR",
             symbol: "RUNE",
             ticker: "RUNE",
           },
           amount: "100000000", // Value in 1e8 (100000000 = 1 RUNE)
         },
       ],
       memo: memo,
       signer: bech32.decode(signerAddr).data,
     };

     const depositMsg = {
       typeUrl: "types.MsgDeposit",
       value: MsgDeposit.fromObject(msg),
     };

     const fee = {
       amount: [],
       gas: "50000000", // Set arbitrarily high gas limit; this is not actually deducted from user account.
     };

     const response = await client.signAndBroadcast(
       signerAddr,
       [depositMsg],
       fee,
       memo,
     );
     console.log("response: ", response);

     if (response.code !== 0) {
       console.log("Error: ", response.rawLog);
     } else {
       console.log("Success!");
     }
   }

   main();
   ```

### Native Transaction Fee

As of [ADR-009](https://gitlab.com/thorchain/thornode/-/blob/develop/docs/architecture/adr-009-reserve-income-fee-overhaul.md), the native transaction fee for $RUNE transfers or inbound swaps is USD-denominated, but ultimately paid in $RUNE, which means the fee is dynamic. Interfaces should pull the native transaction fee from THORNode before each new transaction is built/broadcasted.

**THORNode Network Endpoint**: [/thorchain/network](https://thornode.ninerealms.com/thorchain/network)

```json
{
  ...
  "native_outbound_fee_rune": "2000000", // (1e8) Outbound fee for $Asset -> $RUNE swaps
  "native_tx_fee_rune": "2000000", // (1e8) Fee for $RUNE transfers or $RUNE -> $Asset swaps
  ...
  "rune_price_in_tor": "354518918", // (1e8) Current $RUNE price in USD
  ...
}
```

The native transaction fee is automatically deducted from the user's account for $RUNE transfers and inbound swaps. Ensure the user's balance exceeds `tx amount + native_tx_fee_rune` before broadcasting the transaction.
