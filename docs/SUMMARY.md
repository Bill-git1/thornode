# Summary

- [Introduction](README.md)

  - [FAQs](technical-faq.md)

- [Swap Guide](swap-guide/quickstart-guide.md)

  - [Memoless Swaps](swap-guide/memoless-swaps.md)
  - [Advanced Swap Queue](swap-guide/advanced-swap-queue.md)
  - [Fees and Wait Times](swap-guide/fees-and-wait-times.md)

- [Affiliate Guide](affiliate-guide/thorname-guide.md)

  - [Affiliate Guide](affiliate-guide/affiliate-fee-guide.md)

- [Using THORChain](concepts/connecting-to-thorchain.md)

  - [Querying THORChain](concepts/querying-thorchain.md)
  - [Sending Transactions](concepts/sending-transactions.md)
  - [Transaction Memos](concepts/memos.md)
  - [Swap Queue Monitoring](concepts/swap-queue-monitoring.md)
  - [Quote Examples](examples/tutorials.md)
  - [Asset Notation](concepts/asset-notation.md)
  - [Memo Length Reduction](concepts/memo-length-reduction.md)

- [Feature Guide](concepts/feature-guide.md)

  - [TCY Guide](concepts/tcy.md)
  - [Streaming Swaps](swap-guide/streaming-swaps.md)
  - [Trade Accounts](concepts/trade-accounts.md)
  - [Secured Assets](concepts/secured-assets.md)
  - [RUNE Pool](concepts/rune-pool.md)
  - [Swapper Clout](concepts/swapper-clout.md)

- [Bifrost](bifrost/how-bifrost-works.md)

  - [Vault Behaviors](bifrost/vault-behaviors.md)
  - [Oracles](bifrost/oracle.md)
  - [TSS](bifrost/tss.md)

- [Chain Clients](chain-clients/README.md)

  - [UTXO](chain-clients/utxo.md)
  - [EVM Chains](chain-clients/evm.md)
  - [BFT Chains](chain-clients/bft.md)
  - [ERC20 Tokens](chain-clients/token-lists.md)

- [Adding New Chains](new-chains/readme.md)

  - [Evaluating New Chains](new-chains/evaluating-new-chains.md)
  - [New Process Chains](new-chains/new-chain-process.md)
  - [Chain Client Implementation Guide](new-chains/implementation-guide.md)

- [Protocol Mechanics](concepts/protocol-mechanics.md)

  - [Economic Model](concepts/economic-model.md)
  - [Incentive Pendulum](concepts/incentive-pendulum.md)
  - [Network Halts](concepts/network-halts.md)
  - [Constants and Mimirs](mimir.md)
  - [Fees](concepts/fees.md)
  - [Delays](concepts/delays.md)

- [Internals & Math](concepts/code-libraries.md)

  - [Math](concepts/math.md)

- [Aggregators](aggregators/aggregator-overview.md)

  - [Memos](aggregators/memos.md)
  - [EVM Implementation](aggregators/evm-implementation.md)

- [CLI](cli/overview.md)

  - [Multisig](cli/multisig.md)
  - [Offline Ledger Support](cli/offline-ledger-support.md)

- [THORNode](release.md)

  - [EVM Whitelist Procedure](evm_whitelist_procedure.md)
  - [Upgrade Router](upgrade_router.md)
  - [Architecture Decision Records (ADR)](architecture/README.md)
    - [ADR Creation Process](architecture/PROCESS.md)
    - [ADR {ADR-NUMBER}: {TITLE}](architecture/TEMPLATE.md)
    - [ADR 001: ThorChat](architecture/adr-001-thorchat.md)
    - [ADR 002: REMOVE YGG VAULTS](architecture/adr-002-removeyggvaults.md)
    - [ADR 003: FLOORED OUTBOUND FEE](architecture/adr-003-flooredoutboundfee.md)
    - [ADR 004: Keyshare Backups](architecture/adr-004-keyshare-backups.md)
    - [ADR 005: Deprecate Impermanent Loss Protection](architecture/adr-005-deprecate-ilp.md)
    - [ADR 006: Enable POL](architecture/adr-006-enable-pol.md)
    - [ADR 007: Increase Fund Migration and Churn Interval](architecture/adr-007-increase-fund-migration-and-churn-interval.md)
    - [ADR 008: Implement a Dynamic Outbound Fee Multiplier (DOFM)](architecture/adr-008-implement-dynamic-outbound-fee-multiplier.md)
    - [ADR 009: Reserve Income and Fee Overhaul](architecture/adr-009-reserve-income-fee-overhaul.md)
    - [ADR 010: Introduction of Streaming Swaps](architecture/adr-010-streaming-swaps.md)
    - [ADR 011: THORFi Lending Feature](architecture/adr-011-lending.md)
    - [ADR 012: Scale Lending](architecture/adr-012-scale-lending.md)
    - [ADR 013: Synth Backstop](architecture/adr-013-synth-backstop.md)
    - [ADR 014: Reduce Saver Yield Synth Target to Match POL Target](architecture/adr-014-reduce-saver-yield-target.md)
    - [ADR 015: Refund Synth Positions After Ragnarok](architecture/adr-015-refund-synth-positions-after-ragnarok.md)
    - [ADR 016: Affiliate Rev Share](architecture/adr-016-aff-rev-share.md)
    - [ADR 017: Burn System Income Lever](architecture/adr-017-burn-system-income-lever.md)
    - [ADR 018: Core Protocol Sustainability](architecture/adr-018-core-protocol-sustainability.md)
    - [ADR 019: AutoBond](architecture/adr-019-auto-bond.md)
    - [ADR 021: Marketing Fund Allocation](architecture/ADR-021-marketing-fund-allocation.md)

- [Archived](archived/archived.md)

  - [TypeScript (Web)](examples/typescript-web/README.md)

    - [Overview](examples/typescript-web/overview.md)
    - [Query Package](examples/typescript-web/query-package.md)
    - [AMM Package](examples/typescript-web/amm-package.md)
    - [Client Packages](examples/typescript-web/client-packages.md)
    - [Packages Breakdown](examples/typescript-web/packages-breakdown.md)
    - [Coding Guide](examples/typescript-web/coding-guide.md)

  - [Saving Guide](archived/saving-guide/quickstart-guide.md)
    - [Fees and Wait Times](archived/saving-guide/fees-and-wait-times.md)
  - [Lending](archived/lending/quick-start-guide.md)
