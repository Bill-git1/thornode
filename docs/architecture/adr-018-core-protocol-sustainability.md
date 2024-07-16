# ADR 18: Core Protocol Sustainability

## Changelog

- 26/05/2024: Created
- 15/07/20224: Updated to remove PoL mechanics, added holding address

## Status

Proposed

## Context

THORChain is a complex protocol that requires a full-time protocol engineering, security and maintenance team. This ADR discusses a long-term sustainable funding proposal.

## Background

Protocol engineering in past was funded and managed by "OG" who handed over operationally to "9R" in the period beginning 2021.
The OG team provided an outlay of capital and vested incentives to NineRealms and THORSec for a 3-year period beginning late June 2021 and concluding June 2024.
It has been extremely important that "OG team" hand over the protocol to a community-led team as part of "Planned Obsolescence" as a proof of decentralisation.

9R is an engineering and maintenance collective to manage the business development, maintenance and feature rollout of THORChain.
THORSec is an embedded security team that manage the Security of the Protocol as well.

9R (Core 2.0) and THORSec is currently "unfunded" and do not have long-term incentives in play.

## Objectives

Long term incentives need to be aligned:

1. World-class talent acquisition for engineering & security
2. Sustainable "dev fund" to pay for core protocol upgrades, with Node Operator Oversight
3. Incentive Pendulum immune (not biased to either Nodes or LPs, rather tied to the system in total)

## Proposal

To achieve the above objectives:

### DevFund

```text
DevFundSystemIncomeBPS = 500 // 5%
```

5% of System Income (prior to being split to Nodes and LPs) should be paid into a certain address. This address should be nominated in the ADR and can be upgraded by susbsequent ADRs, including changing the amount. This achieves "Node Operator Oversight".

The receiving address should be fully-custodied by the "Core Protocol Team" of the day, with full discretion as to how this is spent. This is currently 9R.

If fiduciary duties are not being met (slowing protocol development), then the NO community can pause funding via Mimir or change the receipt address on subsequent ADR.

## Economics

At current price of RUNE ($4.00), around $150k is made by the system every day (fees + rewards) (up to $300k on high-volume days). At 5%, thus

```text
(150000*0.05)*365 = ~$3m/year (base case)
```

If RUNE prices doubles, and system volume doubles then the accumulated funding would be $12m/year.

This would be enough funding to pay for Core Protocol Maintenance needs, as well as provide ample alignment to grow System Income.

## Decision

TBD

## Consequences

The System Income will start allocating 5% System Income to a Core Protocol Funding wallet. Custody of this wallet will be handed to Core 2.0 for full discretion on spending, but with community oversight.
Separately, and not part of this ADR, Treasury will boost this wallet with 1m RUNE to be a Genesis Incentive Boost to Core 2.0. This will form the first 12-18months of Incentives to Core 2.0.
Nodes have an ability to user mimir to 1) pause, lower, increase the Dev Fund; or use ADR to 2) change the destination wallet.

For reference, the holding address will be this: thor1jw4ujum9a7wxwfuy9j7233aezm3yapc3s379gv
