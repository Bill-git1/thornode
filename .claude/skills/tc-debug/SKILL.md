---
name: tc-debug
description: Debug THORChain network issues - transactions, vaults, addresses, or described problems. Investigates unobserved inbounds, stuck outbounds, signing failures, vault insolvency, and more using THORNode API, Loki logs, and on-chain data.
allowed-tools: WebFetch, Bash, Read, Write, Edit, AskUserQuestion, Grep, Glob, Agent
---

# THORChain Network Debugger

**Improvement policy**: When the user corrects behavior or gives feedback about this skill, always update THIS file (SKILL.md) directly — never save feedback memories. This skill is a shared artifact; improvements belong here so all users benefit.

## Invocation

```text
/tc-debug <transaction_hash | vault_pubkey | address | description>
```

The input is classified automatically:

- **Transaction hash**: 64-char hex (with or without 0x prefix)
- **Vault pubkey**: `thorpub...` or `tthorpub...`
- **THORChain address**: `thor1...` or `tthor1...`
- **Chain address**: BTC (`bc1...`, `1...`, `3...`), EVM (`0x...` 40-char), LTC, DOGE, BCH, GAIA, SOL
- **Description**: Any free text describing the issue

## Endpoints

| Service        | URL                                                   |
| -------------- | ----------------------------------------------------- |
| THORNode API   | `https://gateway.liquify.com/chain/thorchain_api`     |
| Tendermint RPC | `https://gateway.liquify.com/chain/thorchain_rpc`     |
| Midgard API    | `https://gateway.liquify.com/chain/thorchain_midgard` |

**Endpoint fallback**: If a Liquify endpoint is down or returning errors, fall back to these:

- THORNode API: `https://thornode.thorchain.network`
- Tendermint RPC: `https://rpc.thorchain.network`
- Midgard API: `https://midgard.thorchain.network`

**Network fallback**: Default to mainnet (above URLs). If a transaction/address is not found, prompt user or try stagenet:

- Stagenet THORNode: `https://stagenet-thornode.ninerealms.com`
- Stagenet Midgard: `https://stagenet-midgard.ninerealms.com`

## Scripts

In this skill's directory (`.claude/skills/tc-debug/`):

| Script                        | Purpose                                                     |
| ----------------------------- | ----------------------------------------------------------- |
| `classify_input.py <input>`   | Classify input type (tx, vault, address, description)       |
| `discover_loki.py [addr]`     | Discover & cache Loki job labels → `/tmp/tc_loki_jobs.json` |
| `parse_tx_status.py [height]` | Parse tx/status response → diagnostic summary (stdin)       |

Also reuse scripts from the health skill (`.claude/skills/health/`):

| Script                                       | Purpose                                          |
| -------------------------------------------- | ------------------------------------------------ |
| `parse_nodes.py <M.m> <height>`              | Node analysis → pubkey map + IPs to `/tmp/`      |
| `parse_vaults.py <height>`                   | Vault status, balances, retirement duration      |
| `extract_vault_balances.py <chain> [status]` | Extract per-chain balances for solvency pipeline |
| `check_solvency.py <chain>`                  | Compare vault vs L1 balances at 1e8 precision    |

## Conventions

- **SANITIZATION (MANDATORY)**: Never expose namespace names, pod names, container names, job labels, or any Kubernetes-internal identifiers in output. Use generic labels: "thornode logs", "bifrost logs".
- **Case sensitivity (MANDATORY)**: Always use `(?i)` case-insensitive flag in LogQL for chain names, event keywords, error terms.
- **Log limit caveat**: `--limit=N` caps lines returned, not total events. Label counts as "of sample" with sample size.
- **Nodes**: `<operator_last4>:<address_last4>` — never bare pubkeys. Resolve via `/tmp/tc_pubkey_map.json`.
- **Vaults**: `...{last 4 of pub_key}`.
- **Amounts**: Full 1e8 precision for THORChain native amounts. Divide `rune_price_in_tor` by 1e8 for USD.

## Procedure

### 0. Prerequisites

**Step 0a — Classify input**:

```bash
python3 .claude/skills/tc-debug/classify_input.py "<user_input>"
```

Read the output JSON to determine `type` and `chain_hint`. This determines which debugging path to follow.

**Step 0b — Check logcli + Loki**:

1. Check `which logcli`. If not found, inform the user: "logcli is required for log analysis. Install it from https://grafana.com/docs/loki/latest/tools/logcli/ and ensure it's in your PATH."
2. Check Loki connectivity: `logcli --addr=http://localhost:3100 labels --since=1h --quiet 2>&1`. If connection refused, ask user via AskUserQuestion: "Loki is not reachable at localhost:3100. Do you have a port-forward to your cluster's Loki? If it's on a different port, please specify."
3. Discover job labels:

```bash
python3 .claude/skills/tc-debug/discover_loki.py
```

Review the output. If no thornode or bifrost jobs found, inform user and proceed with API-only debugging.

**Step 0c — Network version + code reference**:

1. Fetch `/thorchain/version` (WebFetch) → record `current` as `CURRENT_VERSION` and note `next_since_height`.
2. Fetch `/thorchain/lastblock` (WebFetch) → record `CURRENT_HEIGHT` (thorchain height).
3. **Determine `ISSUE_VERSION`** — the version active when the issue occurred, which may differ from `CURRENT_VERSION` if the network has upgraded since:
   - For transaction debugging: compare the tx's `consensus_height` or `finalised_height` against `next_since_height`. If the tx height predates it, the issue occurred under a previous version.
   - To find the previous version: `git tag -l "v*" --sort=-version:refname` and correlate release tags chronologically.
   - For non-tx debugging or when the tx height matches `CURRENT_VERSION`, set `ISSUE_VERSION = CURRENT_VERSION`.
4. Set up code reference at the `ISSUE_VERSION` tag:

```bash
TAG=$(git tag -l "v*" | grep "<ISSUE_VERSION>" | head -1)
```

Use `git show $TAG:<filepath>` to read files at that version without modifying the working tree.

**Last resort**: If the tag doesn't exist, use current branch code and note the version mismatch in the report.

**Step 0d — Node + vault context** (run if investigating vaults or need vault membership):

```bash
curl -s 'https://gateway.liquify.com/chain/thorchain_api/thorchain/nodes' \
  | python3 .claude/skills/health/parse_nodes.py "$MAJOR_MINOR" "$HEIGHT"

curl -s 'https://gateway.liquify.com/chain/thorchain_api/thorchain/vaults/asgard' > /tmp/tc_vaults.json
python3 .claude/skills/health/parse_vaults.py "$HEIGHT" < /tmp/tc_vaults.json
```

---

### 1. Transaction Debugging

Use when input type is `tx_hash`.

#### 1a. Fetch transaction status

```text
WebFetch: https://gateway.liquify.com/chain/thorchain_api/thorchain/tx/status/<TXID>
```

Pipe through the parser:

```bash
curl -s 'https://gateway.liquify.com/chain/thorchain_api/thorchain/tx/status/<TXID>' \
  | python3 .claude/skills/tc-debug/parse_tx_status.py "$HEIGHT"
```

#### 1b. Fetch transaction details

```text
WebFetch: https://gateway.liquify.com/chain/thorchain_api/thorchain/tx/details/<TXID>
```

Key fields:

- `observed_tx.status`: `"done"`, `"reverted"`, or empty (incomplete)
- `observed_tx.out_hashes`: Hashes of outbound txs (for cross-referencing)
- `observed_tx.signers`: Which nodes signed (compare to expected set)
- `outbound_height`: **True** scheduled height (NOT the retry height from queue)
- `keysign_metric`: TSS signing performance data

#### 1c. Check for refund and fetch reason

If the tx/status `planned_out_txs` contains a `"refund": true` entry, or the tx/details `actions` memo starts with `"REFUND:"`, the transaction was refunded rather than processed as intended.

**Always fetch the refund reason from Midgard**:

```text
WebFetch: https://gateway.liquify.com/chain/thorchain_midgard/v2/actions?txid=<TXID>
```

Look for `metadata.refund.reason` in the response.

**Always include the refund reason prominently in the output** — both in the investigation narrative and the structured summary:

```markdown
> **Refund Reason**: "<reason from Midgard>"
> Original intent: <memo action type> (parsed from memo)
```

The refund reason often directly identifies the root cause:

- `"observed inbound tx to an inactive vault"` → vault was inactive at observation time, check vault status timeline
- `"emit asset 0 less than price limit"` → limit price not met, check pool price vs limit
- `"fail swap, not enough fee"` → dust amount, too small to cover fees
- `"trading is halted"` / `"pool is suspended"` → check mimir halts

#### 1d. Diagnose by phase

Based on the `parse_tx_status.py` output phase:

**Phase: `inbound_observation` (Unobserved Inbound)**

The transaction hasn't been observed by THORChain. Investigate:

1. **Verify the tx exists on the source chain**: Use appropriate block explorer to confirm the L1 tx is confirmed.
2. **Check inbound_addresses**: `WebFetch: .../thorchain/inbound_addresses` — verify the destination address matches an active vault and the chain isn't halted.
3. **Check the memo format**: Bad memos can cause observation drops.
4. **Search bifrost observer logs**:

```logql
{<bifrost_job_selector>} |~ "(?i)<TXID_or_prefix>"
{<bifrost_job_selector>} |~ "(?i)observe" |~ "(?i)<CHAIN>" | json | line_format "{{.message}}" |~ "(?i)(drop|skip|ignore|reject|err|fail)"
```

5. **Check if chain scanner is lagging**: Compare `/thorchain/lastblock/<CHAIN>` external height to actual chain tip.
6. **Check for halted chain**: `WebFetch: .../thorchain/mimir` — look for `Halt<CHAIN>*` or `SolvencyHalt<CHAIN>*` mimirs.

**Phase: `confirmation_counting` (Waiting for Confirmations)**

Normal state — external chain needs more confirmations. Report the remaining time and move on unless it's been stuck for unusually long (compare expected chain block time to remaining seconds).

**Phase: `swap_pending` (Stuck in Swap Queue)**

The swap is queued but not executing:

1. **Check swap queue**: `WebFetch: .../thorchain/queue/swap/details/<TXID>` — get queue_type and position.
2. **Check for pool halts**: `WebFetch: .../thorchain/mimir` — look for `HaltTrading` or pool-specific halts.
3. **Search thornode logs**:

```logql
{<thornode_job_selector>} |~ "(?i)<TXID_prefix>" |~ "(?i)(swap|queue|limit|stream)"
{<thornode_job_selector>} |~ "(?i)swap" |~ "(?i)(fail|err|skip|reject|refund)"
```

4. **For streaming swaps**: Check if the interval/quantity makes sense. A streaming swap with many remaining swaps at a wide interval can take hours — this may be normal.

**Phase: `outbound_delay` (In Delay Period)**

Normal state — THORChain security delay. Report remaining time. Only investigate if the delay seems abnormally long (check `OutboundTransactionFee` and tx value).

**Phase: `outbound_signing` (Outbound Not Yet Signed)**

**Important**: An outbound that was scheduled only a few blocks ago (<50 blocks / ~5 min) is NOT stuck — signing and broadcasting takes time. Only escalate if it's been >50 blocks since `scheduled_outbound_height`. Also check if the vault is retiring (churning) — outbounds from retiring vaults can be slower due to TSS coordination.

This is the most common truly "stuck" state when age is significant. The outbound was scheduled but never signed/broadcast:

1. **Get outbound details**: From tx/details, note `outbound_height` (true schedule time), `observed_vault_pubkey` (signing vault).
2. **Check signing vault status**: `WebFetch: .../thorchain/vault/<PUBKEY>` — check `status`. If `InactiveVault` or `RetiringVault`, run the **Inactive/Retiring Vault Balance Check** (section 2e) immediately. This is a critical diagnostic — outbounds assigned to non-active vaults are a common cause of stuck transactions.
3. **Check scheduled queue**: `WebFetch: .../thorchain/queue/scheduled` — is the outbound present?
4. **Check keysign queue**: `WebFetch: .../thorchain/keysign/<outbound_height>` — is there a keysign record?
5. **Check for chain signing halt**: `WebFetch: .../thorchain/mimir` — look for `HaltSigning<CHAIN>=1` or `HaltChainGlobal`.
6. **Search thornode logs for signing**:

```logql
{<thornode_job_selector>} |~ "(?i)<TXID_prefix>"
{<thornode_job_selector>} |~ "(?i)sign" |~ "(?i)<CHAIN>" |~ "(?i)(fail|err|timeout|blame)"
```

7. **Search ALL bifrost logs for signing/broadcast**:

```logql
{<bifrost_job_selector>} |~ "(?i)<TXID_prefix>"
{<bifrost_job_selector>} |~ "(?i)(sign|broadcast)" |~ "(?i)<CHAIN>" |~ "(?i)(fail|err|timeout|reject)"
```

Search all bifrost instances first. If too noisy, narrow to bifrost instances that are members of the signing vault (cross-reference vault membership from step 0d with `/tmp/tc_pubkey_map.json`).

8. **Check keysign metrics/blame**: `WebFetch: .../thorchain/metrics` — look for blame entries on the signing vault.
9. **Expand search if stuck**: Look at chain client logs around the time of the scheduled outbound:

```logql
{<bifrost_job_selector>} |~ "(?i)<CHAIN>" | json | line_format "{{.message}}" |~ "(?i)(gas|nonce|balance|insufficient|reject)"
```

**Phase: `completed` (Outbound Sent)**

If the user reports it as stuck but status shows completed:

1. **Verify on L1**: Check the outbound tx hash on the destination chain's block explorer.
2. **Check for failed L1 tx**: The outbound may have been broadcast but reverted on-chain.
3. **Check tx/details status**: If `status: "reverted"`, the outbound failed and was refunded.

#### 1e. Non-swap transaction types

Not all transactions are swaps. Parse the memo to determine the action type and adjust investigation:

| Memo prefix                 | Action                         | Key difference                                  |
| --------------------------- | ------------------------------ | ----------------------------------------------- |
| `SWAP:` or `=` or `s:`      | Swap                           | Full pipeline: observe → swap → outbound        |
| `ADD:` or `+` or `a:`       | Add liquidity                  | No outbound — completes at inbound finalisation |
| `WITHDRAW:` or `-` or `wd:` | Withdraw liquidity             | Outbound sends LP's share back                  |
| `DONATE:` or `d:`           | Donate to pool                 | No outbound                                     |
| `BOND:` or `UNBOND:`        | Node bond/unbond               | Bond has no outbound; unbond sends RUNE         |
| `LEAVE:`                    | Node leave                     | Sends bond back                                 |
| `MIGRATE:`                  | Vault migration                | Internal — skip in stuck outbound analysis      |
| `NOOP:`                     | No-op (gas)                    | Observed only, no action                        |
| `REFUND:`                   | Refund                         | Should have an outbound returning funds         |
| `$+` or `$-`                | Trade account deposit/withdraw | Deposit has no outbound; withdraw does          |
| `LOAN+:` or `LOAN-:`        | Loan open/close                | Open has outbound; close may not                |

For add/donate/bond with no expected outbound: if the stage shows completed at `inbound_finalised` and there are no planned outbounds, this is **normal** — not stuck.

#### 1f. Cross-reference outbound hashes

For every outbound hash found in `out_txs` or `out_hashes`:

1. Look up on the destination chain's block explorer to verify it landed.
2. If the outbound hash is empty/missing but the phase is past signing → the broadcast may have failed silently.

#### 1g. Not found on mainnet

If `/thorchain/tx/status/<TXID>` returns empty/404 on mainnet:

1. Try Midgard: `WebFetch: .../v2/actions?txid=<TXID>`
2. Try stagenet endpoints.
3. Ask user if they can confirm which network/chain the tx is from.

---

### 2. Vault Debugging

Use when input type is `vault_pubkey` or when transaction debugging reveals a vault issue.

#### 2a. Fetch vault details

```text
WebFetch: https://gateway.liquify.com/chain/thorchain_api/thorchain/vault/<PUBKEY>
```

Key fields: `type` (asgard/yggdrasil), `status` (active/retiring/inactive), `coins` (THORChain accounting balances), `membership` (node pubkeys), `chains`, `inbound_tx_count`, `pending_tx_block_heights`.

#### 2b. Compare balances to L1

For each chain the vault holds assets on:

```bash
# Get vault balances from THORNode
python3 .claude/skills/health/extract_vault_balances.py <CHAIN> < /tmp/tc_vaults.json

# Compare to L1 on-chain balances
python3 .claude/skills/health/extract_vault_balances.py <CHAIN> < /tmp/tc_vaults.json \
  | python3 .claude/skills/health/check_solvency.py <CHAIN>
```

Report discrepancies at full 1e8 precision. Common causes:

- Gas spent on failed outbounds (not accounted for in THORChain)
- Dust attacks (unsolicited deposits)
- Double-spend or reorg on source chain
- Bug in gas accounting logic

#### 2c. Check vault-related logs

```logql
# Thornode logs mentioning the vault
{<thornode_job_selector>} |~ "(?i)<PUBKEY_last8>"

# Bifrost logs for vault members
{<bifrost_job_selector>} |~ "(?i)<PUBKEY_last8>"

# Look for solvency checker findings
{<thornode_job_selector>} |~ "(?i)solvency" |~ "(?i)<CHAIN>"
```

#### 2d. Check pending transactions

If `pending_tx_block_heights` is non-empty, the vault has pending work:

```text
WebFetch: .../thorchain/keysign/<pending_height>/<PUBKEY>
```

Check if keysigns are stuck or progressing.

#### 2e. Inactive/Retiring vault balance check (MANDATORY)

**This check is MANDATORY any time a vault involved in the investigation has status `InactiveVault` or `RetiringVault`.** It applies both when debugging vaults directly (section 2) and when transaction debugging (section 1d, outbound_signing phase) reveals the signing vault is non-active.

**For `RetiringVault`**:

1. Check how long it's been retiring (compare `status_since` to current height — >1000 blocks is slow, >5000 blocks is stuck).
2. Check remaining `coins` — what assets still need to migrate.
3. Check for signing halts on chains with remaining balances.
4. Check for stuck MIGRATE outbounds in the scheduled queue.

**For all non-active vaults (both `InactiveVault` and `RetiringVault`)**:

1. **Check THORNode accounting balance**: From the vault's `coins` array, identify ALL assets with non-zero balances.
2. **Check L1 on-chain balances**: For each chain where the vault has a non-zero THORNode balance, verify the actual on-chain balance at the vault's chain-specific address:

```bash
# If vault data is in /tmp/tc_vaults.json (from step 0d):
python3 .claude/skills/health/extract_vault_balances.py <CHAIN> inactive < /tmp/tc_vaults.json \
  | python3 .claude/skills/health/check_solvency.py <CHAIN>

# Or fetch the vault's chain address from the vault response and query L1 directly
```

3. **Flag stuck funds prominently**: If either the THORNode accounting balance or the L1 on-chain balance is significant (non-dust) for an inactive vault, flag it in the report:

```markdown
> **STUCK FUNDS ON INACTIVE VAULT**
> Vault `...{last4}` (status: InactiveVault since height {N}) holds significant balance:
>
> - THORNode accounting: {amount} {asset}
> - L1 on-chain ({chain address}): {amount} {asset}
>   These funds are stranded — the vault's TSS committee can no longer sign outbounds.
```

4. **Check recovery status**: Look at the slasher's recovery mechanism:
   - Fetch mimir for `MaxRetiredVaultRecoveryAttempts` (default: 100) and `SigningTransactionPeriod` (default: 300).
   - Calculate: `attempts = (current_height - tx_finalised_height) / signing_period`.
   - If `attempts >= max_recovery_attempts`: recovery has been **exhausted** — the outbound is abandoned. Note this explicitly.
   - If still within recovery window: the slasher should be rescheduling. Check if the outbound is in the scheduled queue.

**Why this matters**: Funds on inactive vaults are effectively locked. The vault's TSS members may have churned out, making signing impossible. Even if the vault has the balance, the outbound can't be fulfilled without either (a) the inactive vault successfully signing, or (b) a code fix to reroute the outbound to an active vault.

---

### 3. Address Debugging

Use when input type is `thor_address` or `chain_address`.

#### 3a. Determine address role

**For THORChain addresses (`thor1...`)**:

1. Check if it's a vault: `WebFetch: .../thorchain/vaults/asgard` — search for the address in vault `addresses` map.
2. Check if it's a node: `WebFetch: .../thorchain/node/<address>` — if found, it's a node operator address.
3. If neither, it's a user address.

**For chain-specific addresses**:

1. Check if it matches any vault address: search `/thorchain/vaults/asgard` response for matching addresses per chain.
2. If it's a vault address → proceed to Vault Debugging (section 2).
3. If it's a user address → search for transactions involving this address.

#### 3b. Find transactions for user addresses

Use Midgard to find transactions:

```text
WebFetch: https://gateway.liquify.com/chain/thorchain_midgard/v2/actions?address=<ADDRESS>&limit=10
```

Review the returned actions. For any that look problematic (pending, incomplete), extract the tx hash and proceed to Transaction Debugging (section 1).

#### 3c. Find transactions for chain addresses without Midgard

If Midgard doesn't return results, search Loki:

```logql
{<bifrost_job_selector>} |~ "(?i)<ADDRESS_or_last12>"
{<thornode_job_selector>} |~ "(?i)<ADDRESS_or_last12>"
```

---

### 4. Description Debugging

Use when input type is `description`. The user has described a problem rather than providing a specific identifier.

#### 4a. Parse the description

Extract key information:

- **Chain mentioned**: Which blockchain is involved?
- **Action type**: Swap, add liquidity, withdraw, send, etc.
- **Symptom**: Stuck, failed, missing, wrong amount, etc.
- **Time frame**: When did this happen?

#### 4b. Search relevant code

Using the `ISSUE_VERSION` tag (from step 0c), read relevant handler/manager code:

- Swap issues: `x/thorchain/handler_swap*.go`, `x/thorchain/manager_swap*.go`
- Observation issues: `bifrost/observer/observe.go`, `bifrost/observer/attestation*.go`
- Signing issues: `bifrost/signer/sign.go`
- Outbound issues: `x/thorchain/manager_txout*.go`
- Gas issues: `x/thorchain/manager_gas*.go`
- Vault issues: `x/thorchain/manager_vault*.go`

Identify what log messages would be emitted for the described scenario.

#### 4c. Search logs based on code analysis

Build targeted LogQL queries from the log messages found in code:

```logql
# Example: searching for specific error messages found in code
{<thornode_job_selector>} |~ "(?i)<error_message_pattern>"
{<bifrost_job_selector>} |~ "(?i)<error_message_pattern>"
```

#### 4d. Check network state

Depending on the description:

- **Halted chains**: `/thorchain/mimir` + `/thorchain/inbound_addresses`
- **Queue issues**: `/thorchain/queue`, `/thorchain/queue/outbound`, `/thorchain/queue/scheduled`
- **Version issues**: `/thorchain/version` + node version spread
- **Churn issues**: Vault analysis from step 0d

---

### 5. GitLab Correlation

Once you've identified a likely root cause:

#### 5a. Check if already fixed on develop

```bash
# Compare ISSUE_VERSION tag to develop
git log <ISSUE_VERSION_tag>..develop --oneline | grep -i "<keywords>"
git log <ISSUE_VERSION_tag>..develop --oneline | grep -i "<chain>"
```

If a fix commit exists, check whether it's also in `CURRENT_VERSION` (already deployed) or only on `develop` (ships with next release).

#### 5b. Check open issues and MRs

```bash
# Search open issues
glab issue list -s opened -P 30 | grep -i "<keywords>"

# Search open MRs
glab mr list -s opened -P 30 | grep -i "<keywords>"

# Search recently merged MRs
glab mr list --merged -P 15 | grep -i "<keywords>"
```

Report any related issues/MRs with their IDs (use `#N` for issues, `!N` for MRs).

---

### 6. Loki Query Reference

#### Job selection

Always discover jobs dynamically (step 0b). Pick **one** thornode job to avoid duplicate log lines. Use all available bifrost jobs unless filtering to specific vault members. If no bifrost jobs were discovered, skip all bifrost log queries.

#### logcli flags

Always use these flags for clean output:

- `--quiet` — suppress progress/stats output
- `--output=raw` — for human-readable log lines (default for most queries)
- `--limit=N` — cap results (default 500, use 2000+ for counting/blame)
- `--since=Nh` — time range (24h for recent tx, 72h for vault/general issues)

Example full command:

```bash
logcli --addr=http://localhost:3100 query --quiet --output=raw --limit=500 --since=24h \
  '{job="<job>"} |~ "(?i)<pattern>"'
```

#### Standard queries

**Transaction lookup (all hashes)**:

```logql
{<job_selector>} |~ "(?i)<HASH_or_first16>"
```

**Chain-specific errors**:

```logql
{<bifrost_job_selector>} |~ "(?i)<CHAIN>" |~ "(?i)(error|fail|timeout|panic|reject)"
```

**Signing events**:

```logql
{<bifrost_job_selector>} |~ "(?i)(keysign|sign.*tx|broadcast)" |~ "(?i)<CHAIN>"
```

**Observer events**:

```logql
{<bifrost_job_selector>} |~ "(?i)observe" |~ "(?i)<CHAIN>"
```

**Solvency alerts**:

```logql
{<thornode_job_selector>} |~ "(?i)solvency"
```

**Expanding search (when txid lookups yield nothing)**:
When direct txid lookups don't find relevant logs, expand to time-based chain client logs:

1. Determine the approximate THORChain block time of the event.
2. Convert to wall-clock time: `(CURRENT_HEIGHT - event_height) * 6` seconds ago.
3. Search chain client logs in that time window:

```bash
logcli --addr=http://localhost:3100 query --quiet --output=raw --limit=1000 \
  --from="<rfc3339_start>" --to="<rfc3339_end>" \
  '{<bifrost_job_selector>} |~ "(?i)<CHAIN>"'
```

#### Time range guidance

- Transaction debugging: Start with `--since=24h`, expand to `--since=72h` if not found.
- Vault issues: `--since=72h` or wider — insolvency can build up over days.
- Description-based: Match the user's reported timeframe, default to `--since=72h`.

---

## Output Format

Produce a **hybrid investigation report**: narrative investigation flow showing what was checked and found, with a structured summary at the end.

### Investigation narrative

Walk through the debugging steps taken, showing key findings at each step. Use code blocks for important API responses and log excerpts. Redact all Kubernetes identifiers.

### Structured Summary

```markdown
## Debug Summary

**Input**: `<user_input>` | **Type**: <tx/vault/address/description>
**Issue Version**: <ISSUE_VERSION> | **Current Version**: <CURRENT_VERSION> | **Block Height**: <height>

### Diagnosis

| Finding           | Details                                                           |
| ----------------- | ----------------------------------------------------------------- |
| Phase             | <current phase of the tx/issue>                                   |
| Refund Reason     | <reason from Midgard, if applicable — ALWAYS include for refunds> |
| Root Cause        | <identified or suspected cause>                                   |
| Affected Chain(s) | <chain(s)>                                                        |
| Stuck Duration    | <how long, if applicable>                                         |

### Key Evidence

| Source          | Finding                   |
| --------------- | ------------------------- |
| THORNode API    | <what the API showed>     |
| Loki (thornode) | <relevant log findings>   |
| Loki (bifrost)  | <relevant log findings>   |
| On-chain        | <L1 verification results> |

### Recommendation

<What should be done — is there a fix on develop? An open issue? Does a mimir need to be set? Does a node need to restart?>

### Related GitLab

| ID  | Title | Status | Relevance |
| --- | ----- | ------ | --------- |

Source labels must be generic: "thornode logs", "bifrost logs", etc. Never expose job names, namespaces, pod names, or any Kubernetes-internal identifiers.
```

## Chain-Specific Block Explorers

When L1 verification is needed, use these public APIs:

| Chain | Explorer API                                                                                 |
| ----- | -------------------------------------------------------------------------------------------- |
| BTC   | `https://blockchair.com/bitcoin/transaction/<hash>` or `https://mempool.space/api/tx/<hash>` |
| ETH   | `https://api.etherscan.io/api?module=proxy&action=eth_getTransactionByHash&txhash=0x<hash>`  |
| AVAX  | `https://api.snowtrace.io/api?module=proxy&action=eth_getTransactionByHash&txhash=0x<hash>`  |
| BSC   | `https://api.bscscan.com/api?module=proxy&action=eth_getTransactionByHash&txhash=0x<hash>`   |
| BASE  | `https://api.basescan.org/api?module=proxy&action=eth_getTransactionByHash&txhash=0x<hash>`  |
| DOGE  | `https://blockchair.com/dogecoin/transaction/<hash>`                                         |
| LTC   | `https://blockchair.com/litecoin/transaction/<hash>`                                         |
| BCH   | `https://blockchair.com/bitcoin-cash/transaction/<hash>`                                     |
| GAIA  | `https://cosmos.directory/cosmoshub/tx/<hash>`                                               |
| SOL   | `https://public-api.solscan.io/transaction/<hash>`                                           |

For vault balance lookups, prefer the solvency pipeline from the health skill scripts.
