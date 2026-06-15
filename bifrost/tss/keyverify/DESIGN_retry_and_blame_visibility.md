# Keyverify: round-message recovery + blame visibility

**Status:** proposed (rev 2 — corrected after codebase review)
**Scope:** `bifrost/tss/keyverify/`, `bifrost/tss/go-tss/keyverify/ecdsa/`, and keyverify-gated additions to `bifrost/tss/go-tss/common/`
**Delivery:** both parts ship in **one MR**. All new code is `TEMP: keyverify` and is removed with the package (see `REMOVAL.md`) once keyverify succeeds network-wide.

## Background

Keyverify runs a 3-round ECDSA re-attestation against the full membership of every active ECDSA asgard vault the node belongs to. It fires on a shared window (`height % BlockInterval == 0`, `BlockInterval = 100`, scheduler.go), bounded by `DefaultKeyverifyRoundTimeout = 120s` per round and `DefaultPerVaultTimeout = 180s` per vault. At ~6.2s/block the window is ~620s, so per-vault attempts do not overlap.

### Observed failure mode

A live-network investigation found:

- **~0 successes** across all probed vaults; nearly all failures are `kind:timeout` with `non-participating peers: [...]`. Almost none are `kind:culprit`.
- On the two vaults where every member holds its keyshare, same-round cross-examination showed **40–48% of reporter pairs have one-way blame**: A never accepted B's messages while B accepted A's fine, with both nodes up. Mutual blame near zero; blame spread across most members.

This is chaotic, asymmetric, partial loss between **live** nodes. Keyverify requires all N members, so a single unrecovered drop fails the round.

### Transport facts (verified against the code)

These constrain the design and corrected rev 1:

1. **Sends are one-shot.** `communication.go:188-224` — `writeToStream` failure is logged and dropped. No retransmission anywhere.
2. **Rounds are mixed broadcast/unicast.** `KEYVERIFY1`, `KEYVERIFY2b`, `KEYVERIFY3` are broadcast; **`KEYVERIFY2a` is unicast** (`IsBroadcast: false`, tss-lib `ecdsa/keyverify/messages.go`). Unicast messages bypass the entire confirm/cache machinery — `processTSSMsg` routes them straight to `updateLocal` (tss.go:767-769).
3. **The sender has no receipt signal for broadcasts.** Receivers broadcast their hash-confirm to all peers _except the data owner_ (`receiverBroadcastHashToPeers`, tss.go:722-727), and `hashCheck` treats owner-associated confirms as a violation (`ErrHashFromOwner`, tss.go:433-438). The sender also never creates a `LocalCacheItem` for its own message. **Any design that has the sender react to confirms is a wire-protocol change requiring a fleet upgrade before it produces signal** — rejected for this temporary feature (this was rev 1's Part A).
4. **A receiver-side pull path already exists.** When a node holds confirms for a broadcast it never received (`LocalCacheItem.Msg == nil`), `processVerMsg` calls `requestShareFromPeer` (tss.go:676-677, 606), which sends a `TssControl` request to peers whose confirmed hash matches the majority; the response is applied via `processTSSMsg(forward=true)`. It fails in practice because every link in it is one-shot:
   - the request fires only when a _new_ confirm arrives, and needs `threshold-1 = ⌈2n/3⌉-1` matching confirms first (`getMsgHash`, tss.go:410);
   - the request and its response are sent once, unretried;
   - the confirms it depends on are themselves sent once.
5. **Confirm loss is triply fatal.** Confirms gate (a) local acceptance of messages that _did_ arrive (`hashCheck` needs `threshold` confirms, tss.go:425), (b) pull discovery (a node can't know it's missing a message without confirms), and (c) pull _serving_ — a responder serves requests from `roundMgr`, which is populated only in `applyShare` _after its own_ hash-check passes (tss_helper.go:340, tss.go:596). A peer that received M but hasn't accepted it answers "we do not have this message either".
6. **All recovery re-deliveries are already idempotent at the receiver.** Duplicate confirms are dropped per-peer (`checkDupAndUpdateVerMsg`, tss.go:327-334); duplicate shares (unicast or pulled) are dropped by `CheckMsgDuplication` (tss.go:291).

**Non-goal:** making keyverify _succeed_. That requires threshold-success (tolerate an absent minority), tracked separately. This MR targets (1) recovering transient drops between live nodes and (2) visibility into which node failed in which round.

---

## Part A — Round-message recovery (local-only, no protocol change)

### Goal

Recover the dominant failure — messages and confirms dropped between two **live** nodes — using only mechanisms each node can run unilaterally, so the fix is effective the moment a node upgrades, regardless of peer versions. There is a large unused time budget (a protocol that should finish in seconds is allowed 180s).

### Design: per-attempt recovery ticker

Each keyverify attempt runs a recovery ticker (owned by `TssKeyVerify`, operating on its per-attempt `TssCommon` — lifecycle and cleanup are free since `TssCommon` is constructed per attempt in `NewTssKeyVerify` and torn down with `commStopChan`). Every `KeyverifyRetryInterval`, up to `KeyverifyMaxRetries` per artifact:

**(a) Re-send own unicast messages (covers KEYVERIFY2a).**
The sender knows the recipients (`Routing.To`); re-send the identical signed wire bytes to each. This is the only blind re-send that is truly targeted (1 peer), and it is safe: duplicates are dropped at `CheckMsgDuplication` (tss.go:291). Without this, a 2a drop is unrecoverable and invisible — no confirms exist for unicast.
Requires retaining the sent `WrappedMessage` bytes per unicast message for the attempt's lifetime (today `ProcessOutCh` discards them after send, tss.go:537-550).

**(b) Re-broadcast own hash-confirms for received-but-unaccepted broadcasts.**
For each `LocalCacheItem` with `Msg != nil` still sitting in `unConfirmedMessages` (i.e. below the confirm threshold), re-broadcast our `BroadcastConfirmMessage` to all peers except the data owner — same recipient rule as today. Confirms are tiny (key + hash), and duplicates are dropped per-peer at the receiver. Per fact 5, this single action unblocks local acceptance, remote pull discovery, _and_ remote pull serving — it is the highest-leverage retransmission in the system.

**(c) Re-request missing broadcasts (strengthen the existing pull).**
For each `LocalCacheItem` with `Msg == nil` where a majority hash is already determinable (`getMsgHash` succeeds), re-invoke `requestShareFromPeer` from the ticker — today it fires only on confirm arrival and never again. Keep the `threshold-1` gate: it is the safety condition ensuring we request the majority message; (b) makes the gate reachable. The `TSSKeyVerifyVerMsg`/`TSSKeyVerifyMsg` cases already exist in the request dispatch (tss.go:638-643) and the verifier already subscribes `TSSControlMsg` (verifier.go:126).

**(d) Re-broadcast own broadcast messages — last resort, lowest value.**
If after `(b)`+`(c)` a round still stalls, the remaining case is "our broadcast was dropped at most peers, so the network can't even gossip its hash." Blind bounded re-broadcast of our own current-round message covers it. Caveats (acceptable for bounded retries, but implement the guard):

- a peer that already **accepted** M and re-receives it creates a _fresh_ cache item (the old one was removed at tss.go:601-603, recreation at tss.go:786-790) and re-broadcasts confirms to N-2 peers. Guard receiver-side: in `processTSSMsg`, if `roundMgr.Get(key) != nil` the message is already accepted — drop the duplicate. Local change, version-independent (un-upgraded peers just exhibit today's harmless-but-noisy behavior).

Stop conditions for all actions: the attempt ends (success/timeout/ctx) or `KeyverifyMaxRetries` is reached for that artifact. Re-sends are deliberately **not** round-gated: every re-delivery is receiver-deduplicated, the per-attempt volume is tiny (≤ N+2 sent messages × `MaxRetries`), and a peer stuck on an earlier round benefits from receiving that round's message even after the sender has advanced.

### Configuration

Add to `TssConfig` (alongside `KeyverifyRoundTimeout`, types.go:27):

- `KeyverifyRetryInterval` (default 5s)
- `KeyverifyMaxRetries` (default 3)

Worst case fits comfortably inside the 120s round budget: 3 retries × 5s = 15s of recovery activity per round.

### Scoping / gating

All ticker logic is driven from `TssKeyVerify` and gated to keyverify message types; keygen/keysign send paths are untouched. New `TssCommon` state (retained sent-message bytes, per-message retry counters) is inert unless the ticker runs. Keyverify has no on-chain consequence, so it remains the safe validation ground; extending to keysign is out of scope and would be a separate decision after Part B metrics show the recovery is healthy.

### What it does not fix

- Genuinely-down peers (bifrost crashed / 503): nothing reaches a dead process. These remain culprits — now cleanly separable in the data (Part B).
- The all-N requirement: one truly-absent member still fails the round. Threshold-success is the separate fix.
- A broadcast dropped at _every_ other peer simultaneously, beyond what (d)'s bounded retries recover.

---

## Part B — Blame visibility

### Goal (Part B)

Answer "**which nodes did not respond, in which round**" — reliably and centrally. Today: the round is computed at timeout (`GetBroadcastBlame(lastMsg.Type())`, tss_keyverify.go:219) and discarded (`TimeoutError` carries only `Culprits []string`, timeout_error.go:18); `FailureRecord` has no round field; and the data is only readable by polling each node's `/status/keyverify` — which 503s for exactly the nodes whose data matters most.

### Record changes

`FailureRecord` (markers.go:47) — **add fields, never change existing field types**:

```text
FailureRecord {
  PubKey       string      // unchanged
  Height       int64       // unchanged
  AttemptedAt  time.Time   // unchanged
  Kind         string      // unchanged
  Error        string      // unchanged
  Culprits     []string    // unchanged — kept verbatim for compatibility

  ReporterPubKey string    // NEW: the node emitting this record (scheduler's localPubKey)
  LocalRound     string    // NEW: the local party's last produced round (lastMsg.Type()), or "none"
  CulpritDetails []Culprit // NEW: structured per-culprit data, parallel to Culprits
}

Culprit {
  PubKey            string
  LastAcceptedRound string    // last round whose share from this peer was ACCEPTED (KEYVERIFY1/2a/2b/3), or "none"
  LastWireRound     string    // last round seen from this peer at the wire level (incl. received-but-unconfirmed), or "none"
  LastSeenAt        time.Time // when the last wire message from this peer arrived (zero if none)
  SharesAccepted    int       // accepted shares from this peer this attempt
  SharesExpected    int       // total shares expected from this peer (4 rounds)
}
```

Notes on each decision (corrections from rev 1):

- **`Culprits []string` must stay.** Rev 1 replaced it with `[]Culprit`; that is a JSON type change — old `.fail` files (`"culprits": ["thorpub..."]`) would fail to unmarshal entirely, and `List()` drops undecodable files (markers.go:215-218). It would also break any external consumer of `/status/keyverify`. Additive `CulpritDetails` gets the same forward-compat behavior as `Kind`: old files decode with the field empty.
- **No `SessionID`.** The ceremony msgID is `keyverify-<PubKey>-<Height>` (verifier.go:116) and the window logic (scheduler.go:136-151) makes `Height` identical across all members' records of the same round. `(PubKey, Height)` — both already in the record — is the cross-node join key. Adding `ReporterPubKey` is what actually enables assembling the directed who-couldn't-hear-whom graph.
- **`LastAcceptedRound` vs `LastWireRound` — the distinction is the diagnosis.** "Accepted" means the share passed hash-check (broadcast) or was applied (unicast); sourced from `blameMgr.acceptedShares` (manager.go:22,55), which covers both kinds. But broadcast acceptance lags wire receipt by the confirm threshold — under the measured confirm loss, accepted-only data would _undercount deliveries with exactly the bias we are trying to debug_. `LastWireRound`/`LastSeenAt` come from a new small per-peer map in `TssCommon` updated on every verified inbound wire message (`processTSSMsg` / `updateLocal`); `accepted < wire` for a peer says "their messages arrived but confirms didn't assemble", which is a different fix than "nothing arrived".
- **No `JoinedParty`.** Rev 1 proposed it, but keyverify has no join ceremony — the scheduler stagger-starts everyone on the shared window; there is no signal to back the field. `LastSeenAt == zero` ⇔ never engaged, which is what the field was for.
- **`LocalRound` on the record** distinguishes "local party never started" (the `TimeoutError{Reason: ...}` branch, tss_keyverify.go:209-211 — currently flattened into the error string) from "peers went silent at round X".

### Metrics changes

Add a `round` label to the culprit counter (metrics.go:24):

```text
tss_keyverify_culprit_total{pubkey, culprit_pubkey, round}
```

`round` = the blamed round (`lastMsg.Type()` at timeout). Cardinality impact is a ×5 multiplier (4 rounds + "none") on an already-bounded metric — fine.

Additionally, counters validating Part A (cheap, answer "is recovery working" centrally):

```text
tss_keyverify_retry_total{pubkey, action}    # action = unicast_resend | confirm_rebroadcast | share_rerequest | broadcast_resend
tss_keyverify_recovered_total{pubkey, round} # messages accepted after at least one recovery action
```

### Central emission

The per-node `/status/keyverify` endpoint stays for ad-hoc inspection but is the wrong primary channel (down/flapping nodes don't answer). Fleet-wide visibility:

- **Primary:** the `round`-labeled metrics above, scraped centrally.
- **Secondary:** log the full structured `FailureRecord` (with `ReporterPubKey`, `LocalRound`, `CulpritDetails`) as a single structured event on each failed attempt, for the log aggregator. The scheduler already logs `non_participating` (scheduler.go:255-259); extend that event rather than adding a new one.

### Plumbing

`TimeoutError` grows a `CulpritDetails []Culprit` field (alongside the existing `Culprits`), populated in `processVerify`'s timeout branch from `acceptedShares` + the new wire-receipt map; the scheduler's `extractTimeoutCulprits` closure grows a structured counterpart. The culprit (`*btss.Error`) path gets the same treatment where data exists.

---

## Validation

- **Recovery:** `tss_keyverify_retry_total` vs `tss_keyverify_recovered_total` shows whether retries land. Per-round culprit counts before/after: expect the asymmetric one-way share (peer blamed by only 1–2 reporters) to collapse; the genuinely-down share to remain. `LastWireRound > LastAcceptedRound` populations should shrink as (b) lands confirms.
- **Visibility:** join all reporters' records on `(PubKey, Height)` and confirm the directed reachability graph reconstructs; confirm per-round failure distribution is queryable from the metric alone.
- **Round 2a:** `recovered_total{round="KVRound2Message1"}` directly measures how often the unicast round was the dropped one — informs whether 2a needs more than blind re-send if threshold-success work follows.

## Risks

- **Amplification:** all re-sends are bounded (`MaxRetries`) and receiver-deduplicated (facts 6); the one second-order effect — accepted-duplicate broadcasts re-triggering confirm fan-out — is closed by the `roundMgr.Get(key) != nil` receiver guard in (d). Actions (a)–(c) have no fan-out beyond the dropped artifact itself.
- **Cross-version:** every action degrades gracefully against un-upgraded peers — duplicates dropped, requests answered by the existing handler, confirms deduped. No flag-day.
- **Shared-layer blast radius:** new `TssCommon` state is written only when the keyverify-owned ticker runs; keygen/keysign behavior is bit-identical. The receiver guard in (d) is the only change on a shared inbound path — it only drops messages that are provably already accepted (`roundMgr` hit).
- **Record compat:** strictly additive JSON; old `.fail` files and external consumers unaffected.
- **Removal:** ticker, retained-bytes state, wire-receipt map, new metrics, and record fields are all `TEMP: keyverify`-tagged and enumerated in `REMOVAL.md` for deletion with the package.
