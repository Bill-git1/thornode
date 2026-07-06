# XMR Integration Design (THORNode + Sidecar)

## Goal

This document explains how the XMR integration works in THORNode, with emphasis on the boundary between THORNode/Bifrost and the `xmr-frost-signer` sidecar, and why responsibilities are split the way they are.

## Components

- THORNode core: chain state, vault/churn policy, outbound scheduling, observation consensus.
- Bifrost Monero chain client: THORNode adapter for XMR (`bifrost/pkg/chainclients/monero`).
- go-tss FROST coordinator: peer coordination over libp2p (`bifrost/tss/go-tss/tss`).
- `xmr-frost-signer` sidecar: local Rust process for Monero/FROST cryptography, wallet scanning state, and daemon RPC.
- Monero daemon: canonical chain data and tx relay.

## Provenance

The Rust sidecar is consumed as a pre-built Docker image from `registry.gitlab.com/thorchain/devops/serai/xmr-frost-signer@sha256:49752e667970026bba9c8efe4119530668e5f1f53c6a87284e7106a6f89d41a5` by default on this branch, overridable via `XMR_FROST_SIGNER_IMAGE`.

- The THORNode-specific HTTP service, scanner wiring, and deployment glue live in-tree in this repository.
- The Serai-derived Rust cryptography/request crates are attributed and pinned in `Cargo.toml` to `https://github.com/serai-dex/serai` at rev `737dbcbaa78ab817cc1c435cb2b6c5d24d1c4391`.
- The sidecar keeps its own `LICENSE` file adjacent to source so AGPL obligations for the Rust service remain explicit.

## Boundary: THORNode/Bifrost vs Sidecar

### Transport and trust boundary

- Boundary transport is HTTP between Bifrost and the sidecar endpoint on each node.
- Bifrost side (`sidecar.Client`) enforces:
  - non-loopback endpoint must be HTTPS,
  - non-loopback endpoint must have auth (bearer or basic),
  - required endpoint config for XMR signer path.
- Sidecar requires configured auth at startup (bearer or basic) and enforces authenticated protected routes with per-IP/global rate limits.

### Boundary at a glance

```mermaid
flowchart LR
  A["THORNode / Bifrost"] -->|"HTTP API calls"| B["xmr-frost-signer"]
  A -->|"libp2p FROST round exchange + transcript verification"| C["peer THORNode signers"]
  B -->|"Monero RPC"| D["monerod"]
  B -->|"Encrypted local state (keys, outputs, idempotency)"| E["sidecar store"]
```

### API contract at the boundary

Bifrost calls sidecar endpoints for:

- DKG: `/v1/monero/dkg/round1`, `/round2`, `/round3`, `/finalize`
- Signing: `/v1/monero/sign/prepare`, `/partial`, `/combine`
- Broadcast: `/v1/monero/broadcast`
- Scan/read: `/v1/monero/scanner/sync`, `/transfers`, `/balance/{vault}`, `/outputs/{vault}`, `/addresses/{vault}`, chain query endpoints
- No standalone vault registration endpoint; vault mapping is committed by DKG finalize.

The authoritative contract is in:

- `bifrost/pkg/chainclients/monero/XMR_TSS_SIGNER_API.md`

## Delegation Matrix

### THORNode/Bifrost/go-tss owns

- Vault membership + signer set from THORChain.
- FROST party formation (`joinParty`) and libp2p message exchange.
- Deterministic participant indexing and round transcript verification.
- Sender-to-peer binding checks and blame attribution.
- Deterministic `msg_id`, DKG context, plan hash/eventuality verification.
- Keygen gating:
  - all signers must be online for DKG,
  - derived address must match expected vault address,
  - sidecar finalize must succeed.
- Observation/attestation to THORChain:
  - observed txs,
  - errata handling,
  - solvency + network fee reporting.
- Reorg orchestration at block-scanner level.

### Sidecar owns

- FROST cryptographic state machines for local rounds (DKG and signing).
- Persistent encrypted key material and vault registry.
- Local tracked output state machine: unspent/reserved/spent.
- Output reservation lifecycle and reconciliation.
- Idempotency storage and replay handling for sign/broadcast endpoints.
- Deterministic decoy selection + Monero tx construction.
- Broadcast submission to monerod.
- Scanner state per vault and block-level output discovery.
- Direct daemon RPC calls and bounded response parsing.

### Intentionally split

- **Consensus/coordination in THORNode**, **private key material + chain-specific crypto in sidecar**.
- Sidecar never performs cross-peer consensus; it only executes local round math.
- THORNode never stores Monero threshold private key material.

## Keygen Flow (DKG)

1. THORNode signer triggers custom keygen hook for XMR after TSS keygen context is ready.
2. Bifrost calls `TssServer.FrostKeygen(...)` with signer set and expected XMR vault address.
3. go-tss:
   - forms party over libp2p,
   - requires full signer participation for DKG,
   - executes round exchange over p2p,
   - calls local sidecar each round for local cryptographic step.
4. Sidecar DKG:
   - round1: generate commitments,
   - round2: verify commitments and produce encrypted shares,
   - round3: derive group key + address, but only stages material.
5. go-tss validates cross-peer consistency and expected address.
6. go-tss calls sidecar `dkg/finalize`.
7. Sidecar finalize atomically registers vault mapping and persists encrypted threshold share.

Design choice:

- Finalization is explicit to avoid persisting key material before network-level agreement and address checks complete.

## Signing and Broadcast Flow

1. THORNode schedules outbound; Bifrost Monero client validates tx intent:
   - featured+guaranteed destination,
   - single XMR coin,
   - sufficient unlocked balance,
   - scanner freshness guard (sidecar rejects stale scan state).
2. Bifrost resolves current vault signer membership and calls `TssServer.FrostKeySign`.
3. go-tss:
   - derives deterministic `msg_id`,
   - runs preprocess/share exchanges over libp2p,
   - uses transcript verification + blame on inconsistencies.
4. Sidecar signing:
   - `prepare`: load key share, build unsigned tx, select decoys deterministically, reserve chosen inputs,
   - `partial`: create local signature share,
   - `combine`: finalize tx, bind reservation to tx hash, persist outgoing attribution.
5. Bifrost validates eventuality tuple (`plan_hash`, `tx_hex_hash`, `eventuality_id`) before accepting signed payload.
6. Broadcast path calls sidecar `/broadcast`; sidecar relays to daemon and finalizes reservations for that `msg_id`.

Design choices:

- Output reservations are sidecar-local and CAS-protected to prevent concurrent spend races.
- `msg_id` idempotency is enforced at sidecar API level to make retries safe.

## Scanner and Observation Flow

1. Bifrost block scanner queries canonical block data through sidecar chain-query endpoints.
2. For each height, Bifrost calls `scanner/sync` with `(height, tip_height, block_hash, parent_hash, reorg_window)`.
3. Sidecar verifies canonical hash linkage from daemon, performs deterministic rollback/rescan if needed, updates persisted outputs/scan metadata, and returns transfers for that block.
4. Bifrost converts transfers into `TxInItem`, enriches memo attribution (including deferred reference memo lookups), applies observation filters, and attests to THORChain.

Design choices:

- Canonical hash handshake in `scanner/sync` prevents scanning against mismatched history.
- Reorg rollback metadata is persisted sidecar-side, while chain-level errata orchestration remains in Bifrost.

## Why This Boundary

1. Security and blast radius

- Keep Monero threshold key material in a dedicated process with encrypted storage and strict API surface.
- Keep consensus/blame logic in THORNode where signer identity and p2p trust model already exist.

2. Determinism and fail-closed behavior

- Deterministic IDs, transcript hashes, decoy context, and eventuality checks reduce ambiguity across signers.
- Any mismatch (membership, transcript, address, stale scan state, reservation mismatch) fails closed.

3. Operational isolation

- Sidecar can evolve Monero-specific cryptography/scanning without embedding all dependencies into THORNode runtime.
- THORNode keeps chain-agnostic scheduling/attestation responsibilities.

4. Licensing isolation

- Sidecar is intentionally isolated as its own AGPL component, with explicit source/distribution boundary.

## Important Operational Constraints

- XMR sidecar currently uses hard-cutover storage semantics:
  - no legacy runtime compatibility path,
  - no in-place migration,
  - clean sidecar data directory required for cutover upgrade.
- `XMR_FROST_CHAIN_NETWORK` must be explicit (`mocknet`, `chainnet`, `stagenet`, or `mainnet`).
- Sidecar protected routes require configured auth (`XMR_FROST_AUTH_BEARER` or basic auth vars).
- In mainnet mode, sidecar daemon endpoint hardening is stricter (TLS/auth requirements).
- In mainnet mode, `XMR_FROST_STORE_SECRET` must be strong (32-byte hex/base64 equivalent).
- `XMR_FROST_MAX_SIGN_SCAN_LAG_BLOCKS` controls fail-closed signing when scan state is stale (default `3`).

## Current `x/thorchain` Follow-Up Findings

These are working notes from the current XMR integration review and should be revisited before trimming the branch or upstreaming follow-up cleanup.

### Runtime-critical for current XMR behavior

- `x/thorchain/handler_ragnarok.go`: required so ragnarok matching derives the vault source address using the chain signing algorithm. XMR needs the Ed25519 path here.
- `x/thorchain/helpers.go`: required for the current Monero unknown-sender model. Refunds must not attempt an outbound to the synthetic XMR unknown-sender marker when no explicit refund address exists.
- `x/thorchain/helpers.go`: the unrefundable external-coin cleanup path now donates back into pool accounting instead of silently leaving balance stranded in the vault.
- `x/thorchain/manager_slasher_current.go`: required so failed-outbound recovery swaps use `vault.GetAddress(...)`, which picks the Ed25519 vault address for XMR.
- `x/thorchain/manager_slasher_current.go`: the XMR unknown-sender guard is important on failed swap recovery, otherwise reverse-swap recovery can target an undeliverable marker address before eventually falling back.

### Useful but not core to existing-chain XMR operation

- `x/thorchain/manager_network_current.go`: adding `common.XMRChain` to single-validator genesis setup matters for fresh bootstrap flows, not for an already-running chain upgrade.
- `x/thorchain/manager_network_current.go`: using `vault.GetAddress(...)` in over-solvency swap creation matters if that path is exercised for XMR-held assets.
- `x/thorchain/querier.go`: the Ed25519-chain skip condition is a query/API correctness fix for vault address display, not consensus-critical runtime logic.
- `x/thorchain/types/type_vault.go`: `Vaults.HasAddress(...)` now respects chain signing algorithm, but no current non-test caller was found during this review.

### Not required for XMR function

- `x/thorchain/alias.go`: test helper exposure only.
- `x/thorchain/handler_add_liquidity.go`: logging cleanup only.
- `x/thorchain/handler_deposit.go`: logging cleanup and small local logging refactor only.
- `x/thorchain/handler_observed_tx_helpers.go`: logging cleanup only.
- `x/thorchain/keeper/keeper_dummy.go`: test/logger string cleanup only.
- New `*_test.go` files in `x/thorchain`: coverage only, not runtime behavior.

### Outstanding cleanup note

- `x/thorchain/manager_network_current_test.go` currently references `nmgr.createSwapToReserve(...)`, but no such method exists on `NetworkMgr`. The production package builds, but the `x/thorchain` test package is not clean until that test is fixed or removed.

## Simulation Cache and Rerun Notes

These notes are here so future sessions do not relearn the same mocknet build/cache behavior.

### macOS prerequisite

- THORNode simulation targets expect GNU `make`, `awk`, `find`, `sed`, `grep`, and coreutils on macOS.
- A working PATH prefix for local runs is:

```sh
PATH="/opt/homebrew/opt/make/libexec/gnubin:/opt/homebrew/opt/gawk/libexec/gnubin:/opt/homebrew/opt/findutils/libexec/gnubin:/opt/homebrew/opt/gnu-sed/libexec/gnubin:/opt/homebrew/opt/grep/libexec/gnubin:/opt/homebrew/opt/coreutils/libexec/gnubin:$PATH"
```

### Single-node simulation

- For background/non-interactive runs, set `CI=1` so `docker run` does not inject `-it`.
- The first successful `make test-simulation` run is expensive because it rebuilds mocknet images and may need to pull large Rust/Golang base images.
- To warm the cache once:

```sh
PATH="/opt/homebrew/opt/make/libexec/gnubin:/opt/homebrew/opt/gawk/libexec/gnubin:/opt/homebrew/opt/findutils/libexec/gnubin:/opt/homebrew/opt/gnu-sed/libexec/gnubin:/opt/homebrew/opt/grep/libexec/gnubin:/opt/homebrew/opt/coreutils/libexec/gnubin:$PATH" \
CI=1 make build-mocknet build-test-simulation
```

- After that, the cheaper rerun path is:

```sh
PATH="/opt/homebrew/opt/make/libexec/gnubin:/opt/homebrew/opt/gawk/libexec/gnubin:/opt/homebrew/opt/findutils/libexec/gnubin:/opt/homebrew/opt/gnu-sed/libexec/gnubin:/opt/homebrew/opt/grep/libexec/gnubin:/opt/homebrew/opt/coreutils/libexec/gnubin:$PATH" \
CI=1 make reset-mocknet

PATH="/opt/homebrew/opt/make/libexec/gnubin:/opt/homebrew/opt/gawk/libexec/gnubin:/opt/homebrew/opt/findutils/libexec/gnubin:/opt/homebrew/opt/gnu-sed/libexec/gnubin:/opt/homebrew/opt/grep/libexec/gnubin:/opt/homebrew/opt/coreutils/libexec/gnubin:$PATH" \
CI=1 make test-simulation-no-reset > /tmp/test-simulation.log 2>&1 &
```

- Watch command:

```sh
tail -f /tmp/test-simulation.log
```

### Cluster simulation

- `make test-simulation-cluster` currently rebuilds and resets the cluster every time.
- There is no parallel `test-simulation-cluster-no-reset` target yet, so repeated cluster reruns are still heavier than single-node reruns.
- To warm the cluster cache once:

```sh
PATH="/opt/homebrew/opt/make/libexec/gnubin:/opt/homebrew/opt/gawk/libexec/gnubin:/opt/homebrew/opt/findutils/libexec/gnubin:/opt/homebrew/opt/gnu-sed/libexec/gnubin:/opt/homebrew/opt/grep/libexec/gnubin:/opt/homebrew/opt/coreutils/libexec/gnubin:$PATH" \
CI=1 make build-mocknet-cluster build-test-simulation
```

- Standard cluster run:

```sh
PATH="/opt/homebrew/opt/make/libexec/gnubin:/opt/homebrew/opt/gawk/libexec/gnubin:/opt/homebrew/opt/findutils/libexec/gnubin:/opt/homebrew/opt/gnu-sed/libexec/gnubin:/opt/homebrew/opt/grep/libexec/gnubin:/opt/homebrew/opt/coreutils/libexec/gnubin:$PATH" \
CI=1 make test-simulation-cluster > /tmp/test-simulation-cluster.log 2>&1 &
```

- Watch command:

```sh
tail -f /tmp/test-simulation-cluster.log
```

- Status check for either run:

```sh
ps -p <make-pid> -o pid,ppid,stat,etime,command
```

## Primary Code Anchors

- Bifrost Monero client: `bifrost/pkg/chainclients/monero/client.go`
- Sidecar HTTP client: `bifrost/pkg/chainclients/monero/frost_signer.go`
- go-tss DKG/sign coordinators:
  - `bifrost/tss/go-tss/tss/frost_keygen.go`
  - `bifrost/tss/go-tss/tss/frost_keysign.go`
- Sidecar: consumed as a pre-built Docker image from `registry.gitlab.com/thorchain/devops/serai/xmr-frost-signer@sha256:49752e667970026bba9c8efe4119530668e5f1f53c6a87284e7106a6f89d41a5` by default on this branch, overridable via `XMR_FROST_SIGNER_IMAGE`

## Monero User Flows — Privacy Chain Considerations

### Unknown Sender Model

Monero's ring signatures make it impossible to identify the real sender of a transaction from chain data alone. When Bifrost observes an XMR inbound, the sender is recorded as a deterministic marker address (`xmrunknown1...`) derived from the transaction hash and destination address. This marker signals to THORChain that the real sender is unknowable.

Consequence: **refunds cannot go back to the sender automatically** (unlike BTC/ETH where the sender is visible on-chain). Users must provide an explicit refund address in their memo.

### Swap Memo — Refund Address

The standard swap memo format supports an optional refund address after the destination, separated by `/`:

```text
=:ASSET:DEST_ADDR/REFUND_ADDR:LIMIT:AFFILIATE:FEE
```

For XMR inbound swaps, the refund address is **required**. Without it, a failed swap has no valid address to return funds to (the `xmrunknown` marker is not a real address).

Example — swap XMR to BTC with refund address:

```text
=:BTC.BTC:bc1q.../4refundXmrAddr...:0
```

If the swap fails, funds are returned to `4refundXmrAddr...`. If no refund address is provided and the swap fails, the funds are donated to the pool (unrecoverable).

### Dual-Sided Liquidity (Add Liquidity)

For dual-sided LP with XMR, the order of operations matters for security:

**Recommended: add XMR first, then RUNE.**

1. Send XMR to the vault with memo `+:XMR.XMR:<your_thor_address>`
2. Wait for THORChain to observe the inbound
3. Send RUNE from `<your_thor_address>` with memo `+:XMR.XMR:<your_xmr_address>`

The RUNE side is authenticated by the THOR address signature — only the private key holder can send it. The XMR side is **not authenticated** (unknown sender). If RUNE is added first, an attacker who monitors pending asymmetric LPs could race to send XMR to the vault with a memo referencing their own THOR address, attempting to claim the match.

By adding XMR first, the pending LP waits for the authenticated RUNE side, which cannot be front-run.

**Anti-pattern: add RUNE first, then XMR.**

This exposes the RUNE deposit to a front-running attack where an attacker sends XMR with a memo referencing their own THOR address to claim the match. The attacker's THOR transaction would then complete the dual LP, binding the victim's RUNE to the attacker's address.

### Withdraw Liquidity

Withdraw works normally. The withdraw memo is sent as a RUNE transaction (authenticated), and the XMR portion is returned to the LP's recorded XMR address.
