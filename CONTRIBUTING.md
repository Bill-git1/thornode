# Contributing to the Rust-Sidecar Line

This repository is the canonical Rust-sidecar THORNode line.

## Branch Model

- Default implementation branch: `rust-sidecar`
- Upstream sync branch: `develop`
- Historical context branch: `rust-to-go`

Feature and fix work for this repository should target `rust-sidecar`.

Use `develop` only to sync changes from `thorchain/thornode` into this repository. Do not target `develop` for normal feature delivery in this repo.

## XMR Architecture Boundary

The supported Monero/XMR architecture in this repository keeps the `xmr-frost-signer` Rust sidecar as the production boundary for:

- FROST DKG and signing rounds
- Monero transaction construction and decoy selection
- Sidecar-local encrypted state, reservations, and idempotency

Changes that affect the sidecar boundary should keep the contract and operational notes in sync with:

- `docs/xmr-addition-design.md`
- `docs/xmr-implementation-map.mmd`
- `bifrost/pkg/chainclients/monero/XMR_TSS_SIGNER_API.md`

## Review Expectations

- Rust-sidecar changes should include targeted `xmr-frost-signer` regression coverage when behavior changes.
- Go-side Monero changes should preserve the Rust-sidecar runtime boundary in this repo.
- Cross-repo all-Go work belongs in `boonew/thornode-go`, not on `rust-sidecar`.

## Merge Requests

- Target branch: `rust-sidecar`
- If your branch exists only to sync from upstream, state that explicitly in the merge request.
- If you touch XMR behavior, note whether the change affects:
  - sidecar API contract
  - sidecar storage semantics
  - Bifrost/go-tss orchestration
