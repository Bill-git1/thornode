# THORChain (RUNE) Technical Custody & Network FAQs - Technical Integration

## Differentiating Features and Protocol Customizations

THORChain is built on the [Cosmos SDK](https://docs.thorchain.org/technology/cosmos-sdk), with several alterations and customizations tailored to its use case.

- **Custom Handlers:**

  - **[Add liquidity, withdraw liquidity, and swap handlers](./concepts/memos.md)**: Facilitate cross-chain liquidity pooling and swaps.
  - **[Lending module](https://docs.thorchain.org/thorchain-finance/lending)**: Users post collateral and receive debt in USD, with collateral held in THORChain’s pools. Repayment of USD-denominated debt returns the collateral. See the [Dashboard](https://dashboards.ninerealms.com/#lending) for more information.
  - **[Reserve module](https://runescan.io/address/thor1dheycdevq39qlkxs2a6wuuzyn4aqxhve4qxtxt)**: Manages a Reserve of 78.8M RUNE, used for block rewards and backstopping lending and savers features.
  - **[RUNEPool module](https://thorchain.network/runepool/)**: Phased in to allow RUNE liquidity provision across pools and to replace the reserve’s role in backstopping the savers feature. See the [Dashboard](https://thorchain.net/thorfi/runepool?tab=rune-pools) for more information.

- **Bifrost & Threshold Signatures:**

  - THORChain uses [Bifrost](https://docs.thorchain.org/technology/bifrost-tss-and-vaults) to facilitate native, L1 cross-chain swaps. It runs a fullnode RPC daemon for each network it connects to and utilizes [Threshold Signature Scheme](../bifrost/tss/go-tss/README.md) (TSS) technology for multi-encryption management of assets across blockchains.

- **Custom Governance Mechanisms:**
  - THORChain’s governance is distinct from the Cosmos SDK’s generic module. RUNE holders are not voting members; node operators receive votes. Most economic parameters require a 2/3 majority to change, while some operational parameters require 3 votes, which can be overridden by 4 votes.

## Token Transfers, Staking, and Rewards

### Token Transfer Mechanisms

RUNE tokens can be transferred via:

- Signed transactions (e.g., `MsgSend`).
- Reserve to liquidity pools and nodes (block rewards).
- Reserve to liquidity pools or vice versa (protocol-owned liquidity).
- RUNE holders to/from the reserve (buying and selling shares of POL).
- Minted/burned from the Reserve (lending).

### Reward Claiming

- **Reserve and RUNEPool**: For liquidity provision and protocol-owned liquidity transactions.
- **Lending Program**: Fixed capacity, not currently accepting new loans.

## Transaction Protocol Modifications

### Transaction Construction

- **Method**: Follows standard Cosmos SDK transaction construction and broadcasting.
  - Example:

```json
{
  "body": {
    "messages": [
      {
        "@type": "/types.MsgSend",
        "from_address": "thor1htrqlgcqc8lexctrx7c2kppq4vnphkatgaj932",
        "to_address": "thor1qvlul0ujfrq27ja7uxrp8r7my9juegz0ug3nsg",
        "amount": [
          {
            "denom": "rune",
            "amount": "649600000000"
          }
        ]
      }
    ],
    "memo": "",
    "timeout_height": "0",
    "extension_options": [],
    "non_critical_extension_options": []
  },
  "auth_info": {
    "signer_infos": [],
    "fee": {
      "amount": [],
      "gas_limit": "200000",
      "payer": "",
      "granter": ""
    }
  },
  "signatures": []
}
```

### Execution Time

- Approximately 6 seconds (default block time).

### Cryptographic Algorithms

- **Signing**: secp256k1 (same as Cosmos); TSS vaults support both ECDSA (secp256k1) and EDDSA (ed25519).

### Serialization Protocol

- **Transactions**: Protobuf and JSON.

### Hashing Algorithm

- **Transactions**: sha256.

### Address Creation

- **Addresses**: Derived using the bech32 specification.

### Replay Prevention

- **Mechanism**: Transactions are signed with a Sequence to prevent replay attacks.

### Unique Transaction Fields

- **Memo**: Arbitrary string (256 characters max for THORChain, other chains have different memo lengths).
- **Timeout height**: Expire transactions at a future block height.

### Libraries and Gas

- **[Libraries](../go.mod)**: Go libraries available for signing Cosmos-style transactions.
- **Gas**: Charged in RUNE, fixed rate currently; excess gas not refunded.

### Account Creation and Privacy

- **Accounts**: Implicitly derived from private keys; do not exist until tokens are received.
- **Privacy**: No privacy features.

### Balance Changes and Transaction Queries

- **Balance Changes**: Can occur via store migrations.
- **Transaction Queries**:
  - For deposits: Use `https://thornode.ninerealms.com/thorchain/block` and Midgard API.
  - For transaction status: Use standard endpoints or examples provided.

### Storage and Throughput

- **Storage Growth**: Approximately 90G pruned state; historical growth of 500G every 90 days.
- **Throughput**: Monolithic chain; state growth linear to # of validators and chains.

### Full Node and Smart Contracts

- **Local Mode**: Available through [mocknet](../README.md#start-standalone-full-stack) environment.
- **Smart Contracts**: No current smart contracts; CosmWasm integration planned but not necessary for RUNE transfers.

## Staking

**Does this blockchain support staking?**

THORChain does not have traditional “staking”. The only way RUNE can be used in the network is to provide liquidity (dual-sided LP or [RUNEPool](./concepts/rune-pool.md)) or to secure [TSS vaults](https://runescan.io/vaults) and verify transactions, which node operators must secure with a bond and be subject to slashing. Henceforth, references to “staking” are answered with respect to “bonding”.

**What asset is used for staking?**

RUNE. A minimum of 300,000 RUNE, sent by the [network variable](./mimir.md#node-management) `MinimumBondInRune`, is required to become a validator. Due to rewards being set by an algorithm that favors higher bonds. See the [THORNode dashboard](https://thorchain.net/nodes) for current bond details - generally the average bond amount is required to become and stay active.

**Can accounts delegate their tokens to be staked by a different validator?**

Accounts must choose which validator to stake to, and a [node operator](https://docs.thorchain.org/thornodes/overview) can only whitelist “[bond providers](https://docs.thorchain.org/thornodes/pooled-thornodes)”. Currently there is a max of 10 bond providers per node, set my the network [mimir](./mimir.md) setting `MAXBONDPROVIDERS`, however there is a campaign underway to increase this number to 100. This is different from most “distributed proof-of-stake” networks. It requires bonders to have a relationship with their bond providers, curtailing “public, branded validators”. Validators are anonymous entities and may conduct their own business development to find bond providers. Bond providers themselves have no stake in governance. There is a proposed feature for AutoBond, which will allow users to provide bond that is split evenly across validators without explicit bi-directional coordination. See [Pooled THORNodes]((https://docs.thorchain.org/thornodes/pooled-thornodes) for more information.

**How soon after calling the staking operation can a staker start accruing rewards?**

After the validator [churns in](https://docs.thorchain.org/thornodes/overview/node-operations#churning), rewards begin accruing based on system income from swap fees and block rewards. Validators churn their vaults every 3 days, proving liveness of funds and giving an opportunity for new validators to participate. Every churn, the lowest bond, highest slash, and oldest node churn out. Once a validator has churned out, bond providers can withdraw their principal + pro-rata share of rewards.

**What are the slashing conditions and penalties?**

Bond principal is [slashed](https://docs.thorchain.org/thornodes/overview/risks-costs-and-rewards#risks-to-bond) by more than the outbound amount if the rest of the network observes funds being sent out of a vault without a corresponding ledger entry (double spend or theft). Loss of principal is the primary driver of economic security. Validators must bond more than they secure, and stand to lose more than they gain from theft or bad behavior. Rewards are slashed for missed observations on L1 chains or missing/double signing a block.

**How long is the unbonding period?**

Bond is [available for withdrawal](https://docs.thorchain.org/thornodes/overview/node-operations#node-statuses) after a validator churns out.

**Do the tokens leave the custody of the delegator when this happens?**

Yes, they are held in the network’s bond module. Bonders giving up custody of their RUNE is the basis of economic security.

**Are staking rewards claimed on-chain, automatically or in response to a claim transaction, or must the validator send rewards back manually?**

Rewards are accrued to a bond provider’s amount, which is a separate ledger. They must be withdrawn by an explicit UNBOND transaction and can only be withdrawn when a validator is churned out. Otherwise, they continue to accrue in the absence of explicit UNBOND transactions.

**Are staking rewards automatically redelegated as they accumulate or must they be claimed and redelegated with a transaction?**

Yes, see above.

**How much staking rewards are distributed as a percentage of total issuance?**

It depends on the [Incentive Pendulum](https://docs.thorchain.org/how-it-works/incentive-pendulum). When network security is optimal (2 RUNE for every 1 RUNE of non-RUNE pooled assets, e.g., $50m RUNE bonded and $25m L1 pooled), the network security is considered optimal, and block rewards are split 33%/67% in favor of liquidity providers. When network security approaches 1:1 (danger of becoming underbonded), block rewards shift 100% to node operators to incentivize more bonded RUNE.

## Governance

**Does this blockchain support governance?**

Yes.

**How are votes conducted?**

Votes are conducted by transactions signed on-chain by validators.

**What protocol parameters can be changed through governance?**

Nodes can vote to change specific parameters, as enumerated here:

- [Constants](https://thornode.ninerealms.com/thorchain/constants)
- [Mimir](https://thornode.ninerealms.com/thorchain/mimir)

See the [Constants and Mimir](./mimir.md) page for details. At any given time, multiple votes are underway, and node operators often change their votes for a given constant as a response to changes in market conditions and desired performance. For example, at this time, a faction of nodes is campaigning to increase minimum L1 swap fees from 5bps to 15bps. The current vote is at 54.17% in favor of 15bps, and the swap fee will remain 5bps until the 15bps vote reaches 67%+.

**What is the mechanism by which change is enacted? A dedicated smart contract, a node client upgrade, etc.**

Nodes can campaign to change an existing parameter by simply starting a vote on-chain and relaying their rationale in a message signed by their validator address. Developers, nodes, and community members can propose changes to existing parameters OR creation of new parameters by following the Architecture Design Record (ADR) process, documented here: [ADR Documentation](https://gitlab.com/thorchain/thornode/-/tree/develop/docs/architecture?ref_type=heads)

**How are the measures to vote on published?**

There is generally an announcement in Discord, often linked to an [ADR (Architecture Design Review)](./architecture/README.md) which is detailed in a markdown file committed to the THORNode repository with corresponding discussion channels in Discord. Less controversial or complex changes may have only a description in Discord or a Gitlab issue.

**Does this blockchain support other kinds of participation that institutional investors would be interested in? Please describe it.**

[RUNEPool](./concepts/rune-pool.md) allows RUNE holders to participate in POL, though this is a complex financial product subject to impermanent loss. “Auto Bond” has been proposed but not yet voted on or implemented. It would make THORChain more similar to a distributed proof-of-stake network but allow any holder to add RUNE to the share of total RUNE bonded validators.

**Do you have any vesting schedules? If so:**

**Is the vesting on-chain / off (paper)-chain? Please describe.**

There are no formal staking agreements in place between the network and any of its participants. RUNE is fully distributed. Teams or holders of RUNE may have their own staking agreements with their own third parties. 5bps of system income will soon be paid to a developer fund to incentivize THORChain’s ongoing development. This fund is currently controlled by Nine Realms, a U.S.-based technology services firm, which maintains the THORChain software development life cycle on behalf of nodes. Nodes have the final say of anything that happens on the network. Nodes may campaign to change the beneficiary of the developer fund at any time if they feel their needs are not being met.
