---
name: health
description: Diagnose THORChain network health using public thornode endpoints. Checks halts, stuck outbounds, churn status, vault solvency, bifrost lag, node versions, and more. Use when asked about network status, health, or diagnosing issues.
allowed-tools: WebFetch, Bash, Read, Write, Edit, AskUserQuestion, Grep, Glob
---

# THORChain Network Health Check

**Improvement policy**: When the user corrects behavior or gives feedback about this skill, always update THIS file (SKILL.md) directly — never save feedback memories. This skill is a shared artifact; improvements belong here so all users benefit.

## Endpoints

| Service        | URL                                               |
| -------------- | ------------------------------------------------- |
| THORNode API   | `https://gateway.liquify.com/chain/thorchain_api` |
| Tendermint RPC | `https://gateway.liquify.com/chain/thorchain_rpc` |

## Scripts

In this skill's directory. Use for large JSON — never WebFetch for `/thorchain/nodes` or `/thorchain/vaults/asgard`.

| Script                                       | Purpose                                          |
| -------------------------------------------- | ------------------------------------------------ |
| `parse_nodes.py <M.m> <height>`              | Node analysis → pubkey map + IPs to `/tmp/`      |
| `parse_vaults.py <height>`                   | Vault status, balances, retirement duration      |
| `extract_vault_balances.py <chain> [status]` | Extract per-chain balances for solvency pipeline |
| `check_solvency.py <chain>`                  | Compare vault vs L1 balances at 1e8 precision    |
| `check_bifrost.py [count] [timeout]`         | Scanner lag from bifrost `/status/scanner`       |

All read stdin (piped from `curl`) except `check_bifrost.py` which reads `/tmp/tc_active_ips.json`.

## Conventions

- **Nodes**: `<operator_last4>:<address_last4>` — never bare pubkeys. Resolve via `/tmp/tc_pubkey_map.json`.
- **Vaults**: `...{last 4 of pub_key}`.
- **Mimirs**: Value is block height = set at that block (compute duration). Value `1` = node mimir. Never say "admin mimir".
- **Filtering**: Current major.minor only (old = dead). Jails only if active. Standby only if bond ≥ active min.
- **Keysign blame**: Resolve → `op:addr`, group by operator. Flag >15% share or multi-node operators.
- **Tables**: Pipe-delimited markdown. Solvency at full 1e8. Cause/fix columns reference GitLab (`#N`, `!N`).
- **Omit**: Loans, fee multiplier, LP deposit pauses, active node IP status.

## Procedure

### 0. Loki

Check for `logcli`. If found, ask user via AskUserQuestion. If yes: verify localhost:3100, pick **one** validator job (avoid duplicates). Query 72h+.

**SANITIZATION (MANDATORY)**: Never expose namespace names, pod names, container names, job labels, or any Kubernetes-internal identifiers in output. When referencing a Loki source in the report, use a generic label like "thornode logs" or "bifrost logs" — never the actual job string. Strip or redact these from any raw log excerpts before including them. This applies to ALL output: tables, summaries, inline references, and error messages.

**Case sensitivity (MANDATORY)**: Always use `(?i)` case-insensitive flag when searching for chain names (e.g. `(?i)sol`, `(?i)base`), mimir key names, and event keywords (`halt`, `upgrade`, `router`, `contract`). Log lines can emit these in mixed case depending on the code path, and a case-sensitive match will silently miss events.

**Log limit caveat (MANDATORY)**: `--limit=N` caps lines returned, not total events in the time range. Never treat the limit as the population size. When reporting counts or percentages derived from sampled log data, always label them as "of sample" and state the sample size. Use limits of ≥2000 for blame/event counting to reduce sampling bias.

### 1. Height + Version

`/thorchain/lastblock` → `CURRENT_HEIGHT`. `/thorchain/version` (WebFetch) → `MAJOR.MINOR`.

### 2. Halts

`/thorchain/mimir` + `/thorchain/inbound_addresses` (WebFetch). List ALL mimirs per halted chain.

**Mimir change log (MANDATORY for every halted chain)**:

1. Fetch `/thorchain/mimir/nodes_all` (WebFetch) and search for ALL keys mentioning each halted chain (not just `ADD-CHAIN-*`). Look for `MimirUpgradeContract*`, `Halt*`, `StopSolvencyCheck*`, and any chain-scoped keys. The `nodes_all` response is large — search thoroughly, as keys may appear deep in the results.
2. **Per-key lookup (MANDATORY)**: For each halted chain, fetch these individual mimir key endpoints to see votes, values, and whether consensus is accumulating:
   - `/thorchain/mimir/key/Halt{CHAIN}Trading`
   - `/thorchain/mimir/key/HaltSigning{CHAIN}`
   - `/thorchain/mimir/key/StopSolvencyCheck{CHAIN}`
   - `/thorchain/mimir/key/MimirUpgradeContract{CHAIN}` (key format from `router_upgrade_controller.go`)
     This is essential — `nodes_all` alone can miss keys due to response size.
3. If Loki is available, run **two** queries before classifying the halt:

   **a) Broad mimir change sweep (MANDATORY — run before any chain-specific analysis)**:
   Query all mimir changes over the past 72h+ without chain filtering first. This catches router upgrade keys, contract address mimirs, and other related changes that narrow queries miss:

   ```logql
   {job="<thornode_job>"} |~ "(?i)mimir" | json | line_format "{{.message}} {{.module}}" |~ "(?i)(set|vote|update|upgrade|contract|router)"
   ```

   Or as a simpler fallback:

   ```logql
   {job="<thornode_job>"} |~ "(?i)(MimirUpgrade|RouterUpgrade|HaltTrading|HaltSigning|StopSolvency)" --since=72h --limit=500
   ```

   Review ALL results before writing the chain classification — router upgrade mimirs may appear with slightly different key names or log phrasing.

   **b) Chain-specific query** (after reviewing the broad sweep):

   ```logql
   {job="<thornode_job>"} |~ "(?i)(mimir|halt|upgrade|router)" |~ "(?i)<CHAIN>" --since=72h --limit=200
   ```

   **Always use `(?i)` (case-insensitive) for chain names and mimir keywords** — log output can mix upper/lowercase depending on the code path that emitted the event. A case-sensitive query will silently miss events.

4. **Classify the halt**:

   - **Planned router upgrade**: `Halt{CHAIN}Trading=1` + `StopSolvencyCheck{CHAIN}=1` + `MimirUpgradeContract{CHAIN}` votes present → standard router upgrade playbook. Do not misclassify as an incident.
   - **Incident**: Halt values are block heights (set by admin/automated), or no UpgradeContract mimir votes exist anywhere.
   - If still unclear, check `/thorchain/mimir/key/{MIMIR_KEY}` to see who voted and when.

   **IMPORTANT — API vs logs for upgrade detection**: The `/thorchain/mimir/key/MimirUpgradeContract{CHAIN}` endpoint only reflects the **consensus** value (returns `-1` until enough nodes vote). Individual node votes appear in the Loki logs BEFORE consensus is reached. Always check the logs (step 3b above) for `MIMIRUPGRADECONTRACTBASE` / `MIMIRUPGRADECONTRACTSOL` etc. — if votes are in progress but below threshold, it is still a planned router upgrade, not an incident. The API alone will mislead you into thinking no upgrade mimir exists.

Search GitLab: `glab issue list -s opened -P 30 | grep -i <CHAIN>`, `glab mr list --merged -P 15 | grep -i <CHAIN>`.

### 3. Outbound Queue

`/thorchain/queue` (WebFetch) + `/thorchain/queue/outbound` (WebFetch).

**Exclude MIGRATE outbounds**: Items with a `memo` starting with `MIGRATE:` are normal churn migration transactions, NOT stuck outbounds. Do not include them in the stuck outbounds section. If migration is blocked (e.g. by a signing halt), note it in the **Stuck Churn** section instead.

**True age lookup (MANDATORY for EVERY non-MIGRATE queued item)**: The `height` field on queued outbound items is the **latest retry height**, NOT when the outbound was originally scheduled. For EVERY non-MIGRATE outbound item, look up `/thorchain/tx/details/{in_hash}` and read the `outbound_height` field to determine the true age. Compare `outbound_height` to `CURRENT_HEIGHT` — if the difference is more than ~100 blocks, the outbound is stuck. Never dismiss an outbound as "in-flight" or "recent" based solely on the queue `height` field.

**Bifrost log investigation (MANDATORY for stuck outbounds)**: For every stuck outbound, search ALL available bifrost Loki jobs for errors related to the chain and/or the specific transaction:

```logql
{job="<bifrost_job>"} |~ "(?i)<CHAIN>.*(error|fail|timeout|reject)"
{job="<bifrost_job>"} |~ "<in_hash_prefix>"
```

Search multiple bifrost jobs if available to cross-reference. Include findings in the report.

### 4. Chain Sync

`last_signed_out` (THORChain height) vs `CURRENT_HEIGHT`. `last_observed_in` is external chain height — not comparable.

### 5. Nodes

```bash
curl -s '.../thorchain/nodes' | python3 .claude/skills/health/parse_nodes.py "$M_m" "$H"
```

### 6. Bifrost Scanner

```bash
python3 .claude/skills/health/check_bifrost.py 100 10
```

Most nodes expose `/status/scanner` on port 6040 (typically 60-80%+). The script skips the `/p2pid` pre-check to avoid rate limiting and goes straight to `/status/scanner`. Correlate outliers with slash points and blame.

**Nginx cache caveat**: Each bifrost's `/status/scanner` response is cached independently for ~1 minute by its local nginx. This means scanner "lag" of up to ~1 minute worth of blocks per chain is normal and expected (e.g. ~10 blocks for ETH, ~1 block for BTC). Only flag lag that significantly exceeds this per-chain threshold. When reporting, note the cache effect so readers don't mistake normal cache staleness for real lag.

**Diagnosing lag cause**: The scanner status reports both `chain_height` (what the node's external daemon thinks is the tip) and `scanner_height` (what bifrost has processed). When lag is concerning, compare these across nodes to distinguish two different problems:

- **Scanner lag** (scanner_height behind chain_height on the same node): bifrost is falling behind processing blocks from its own daemon. This is a bifrost performance issue.
- **Daemon lag** (chain_height itself is behind other nodes' chain_height): the node's external chain daemon (e.g. geth, solana-validator) is not synced. This is an infrastructure/node-operator issue, not a bifrost bug.
  Report which type of lag is occurring so the cause is clear.

### 7. Churn

```bash
curl -s '.../thorchain/vaults/asgard' > /tmp/tc_vaults.json
python3 .claude/skills/health/parse_vaults.py "$H" < /tmp/tc_vaults.json
```

Retiring >1000 blocks = stuck. If signing halted for chain with remaining assets → churn blocked.

### 8. Solvency

When stuck churn or outbounds. Full 1e8.

```bash
python3 .claude/skills/health/extract_vault_balances.py CHAIN STATUS < /tmp/tc_vaults.json \
  | python3 .claude/skills/health/check_solvency.py CHAIN
```

### 9. Economics

`/thorchain/network` (WebFetch). Security ratio from `parse_nodes.py` bond total (more accurate).

**RUNE price**: `rune_price_in_tor` from `/thorchain/network` is in **1e8 base units** (like all THORChain amounts). Divide by 1e8 to get the USD price. Example: `rune_price_in_tor: 41001823` → $0.41, NOT $4.10.

### 10. Keysign Blame (Loki)

Single thornode job, 72h+, resolve via `/tmp/tc_pubkey_map.json`. Group by node and operator. Never include the job label or any Kubernetes identifiers in output.

**Log limit caveat (MANDATORY)**: The `--limit` flag caps how many log lines are returned, not how many events actually occurred. Never use the limit value as the denominator for percentages or say "N of M total" when M comes from a limit. When computing blame percentages, count the total blamed-pubkey mentions extracted from the returned sample, and label all percentages as "of sample" or "of visible events" — not as absolute network-wide totals. Use a high enough limit (≥2000) to get a representative sample, and note the sample size in the report.

### 11. Releases

```bash
glab release list -P 5
glab release view <tag>
git log <tag>..develop --oneline
```

Compare latest release vs node adoption. Flag if a release fixes current issues but lacks supermajority.

## Output

Only include sections with findings. Healthy = say so briefly.

```markdown
## THORChain Network Health

**Block:** {H} | **Status:** 🟢/🟡/🔴

### Halted Chains

| Chain | Type | Mimir Keys | Set At | Duration | Likely Cause | Likely Fix |

### Stuck Churn

| Vault | Since | Duration | Remaining | Blocker | Likely Fix |

### Stuck Outbounds

| Chain | Items | First Scheduled | Age | Likely Cause | Likely Fix |

### Vault Solvency

| Vault | THORChain (1e8) | L1 (native) | L1→1e8 | Diff (1e8) | Status |

### Bifrost Scanner Lag (N reporting)

| Chain | Nodes | Min | Med | Max | Worst Node |

### Keysign Blame (72h)

| Node (op:addr) | Blames | % Total | Primary Vault |

### Nodes

| Metric | Value |

### Releases

| Release | Released | Content | Adoption |

### Summary

| Metric | Value |

### Related GitLab

| ID | Title | Status | Relevance |

### Loki

| Source | Finding |

Source labels must be generic: "thornode logs", "bifrost logs", etc. Never expose job names, namespaces, pod names, or any Kubernetes-internal identifiers.
```
