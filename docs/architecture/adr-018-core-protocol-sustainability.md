# ADR 18: Core Protocol Sustainability

## Changelog

- 26/05/2024: Created

## Status

Proposed

## Context

THORChain is a complex protocol that requires a full-time protocol engineering, security and maintenance team. This ADR discusses a long-term sustainable funding proposal.

## Background

Protocol engineering in past was funded and managed by "OG" who handed over operationally to "9R" in the period beginning 2021. The history of this treasury:

1. Funded in 2018 with $600k raised ($300k re-funded)
2. Complimented in 2019 with $1.5m raised
3. Augmented in 2019-2023 with around $10m in OTC sales of RUNE to fund 4 years of development.

The treasury is now comprised:

- $20m liquid assets
- 7m static RUNE
- 4m in Node Bonds delegated with 100% fees to explorers, data providers and more
- $40m in LP positions locked in the protocol (16% of total liquidity)

9R self-selected from the community and received some vested incentives which was complete in June 2024.
Ecosystem grants are paid out from Liquid and Static assets, totally around $100k/mo ($1-2m per year). This also pays for audits, developer incentives and more.

9R (Core 2.0) is currently "unfunded" and do not have long-term incentives in play.

## Objectives

Long term incentives need to be aligned:

1. World-class talent acquisition for engineering & security
2. Sustainable "dev fund" to pay for core protocol upgrades, with Node Operator Oversight
3. Incentive Pendulum immune (not biased to either Nodes or LPs, rather tied to the system in total)

Right now Core 2.0 do not receive any income from the system since Node Bonds pay to community teams, and the LP position funds into itself with no beneficial owner.

The LP position is currently earning ~16% of LP Income (around 10% of total System Income), however being accumulated directly into the protocol itself and not actually being paid out to contributors, despite being notionally owned by "treasury".

This LP position earning could be siphoned off to pay a Dev Fund, but is biased to the Liquidity Side and would be technically complex.

Instead, the LP position can be donated as PoL and then a flat fee on System Income can be entertained.

## Proposal

To achieve the above objectives:

### DevFund

```text
DevFundSystemIncomeBPS = 500 // 5%
```

5% of System Income (prior to being split to Nodes and LPs) should be paid into a certain address. This address should be nominated in the ADR and can be upgraded by susbsequent ADRs, including changing the amount. This achieves "Node Operator Oversight".

The receiving address should be fully-custodied by the "Core Protocol Team" of the day, with full discretion as to how this is spent. This is currently 9R.

If fiduciary duties are not being met (slowing protocol development), then the NO community can pause funding via Mimir or change the receipt address on subsequent ADR.

### PoL

To migrate current Treasury LP into PoL, then back into the RESERVE (when RUNEPool comes online), it is sufficient to simply convert it to PoL.
When RUNEPool comes online, then this position will be migrated to RUNEPool ownership, thus move around 10m RUNE back into the RESERVE. This will boost System income by 12.5%, offsetting the proposed 5% DevFund.

```text
// Zero out Treasury LP
https://runescan.io/address/thor1egxvam70a86jafa8gcg3kqfmfax3s0m2g3m754 -> Zero'd out, apply to PoL
https://runescan.io/address/thor1wfe7hsuvup27lx04p5al4zlcnx6elsnyft7dzm -> Zero'd out, apply to PoL
```

By doing via PoL-RUNEPool mechanics, there would be no on-chain price disruption to the market as this position is converted back into RUNE.

## Economics

At current price of RUNE ($4.20), around $150k is made by the system every day (fees + rewards) (up to $300k on high-volume days). At 5%, thus

```text
(150000*0.05)*365 = ~$3m/year (base case)
```

If RUNE prices doubles, and system volume doubles then the accumulated funding would be $12m/year.

This would be enough funding to pay for Core Protocol Maintenance needs, as well as provide ample alignment to grow System Income.

## Decision

TBD

## Consequences

The Treasury LP will be converted to PoL, then when RUNEPool Comes Online, will be refunded back to the RESERVE seamlessly.
The System Income will notionally be increased by 12.5% (estimated), this should be a spike in incentives to Nodes, LPS, Savers and RUNEPoolers.
The System Income will then start allocating 5% System Income to a Core Protocol Funding wallet. Custody of this wallet will be handed to Core 2.0 for full discretion on spending, but with community oversight.
Separately, and not part of this ADR, Treasury will boost this wallet with 1m RUNE to be a Genesis Incentive Boost to Core 2.0. This will form the first 12-18months of Incentives to Core 2.0.
Nodes have an ability to user mimir to 1) pause, lower, increase the Dev Fund; or use ADR to 2) change the destination wallet.
