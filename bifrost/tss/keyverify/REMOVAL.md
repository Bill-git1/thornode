# Removing the temporary keyverify feature

This package and its companion `bifrost/tss/go-tss/keyverify/ecdsa/` were
added as a temporary, periodic re-attestation of ECDSA keyshares during the
tss-lib v3 rollout (originally MR !34, merged to master and tagged v3.0.0-rc4).
To rip it out:

1. `rm -rf bifrost/tss/keyverify/`
2. `rm -rf bifrost/tss/go-tss/keyverify/`
3. `grep -rn "// TEMP: keyverify" .` — delete every flagged line. Expected
   sites:
   - `bifrost/p2p/messages/p2p_message.go` — `TSSKeyVerifyMsg` and `TSSKeyVerifyVerMsg` consts + `String()` cases
   - `bifrost/p2p/messages/p2p_message_test.go` — table entries
   - `bifrost/p2p/messages/TssPhaseMessages.go` — `KEYVERIFY1`, `KEYVERIFY2a`, `KEYVERIFY2b`, `KEYVERIFY3` constants
   - `bifrost/tss/go-tss/common/tss.go` — keyverify branches in the 4 dispatch switches, the recovery hooks (sent-message retention in `sendBulkMsg`, wire-receipt + duplicate-drop guard in `processTSSMsg`, recovered-note in `applyShare`), and the `kvRecovery` struct field
   - `bifrost/tss/go-tss/common/keyverify_recovery.go` + `keyverify_recovery_test.go` — delete whole files (recovery ticker state/actions)
   - `bifrost/tss/go-tss/common/local_cache_item.go` — `snapshotMsgAndConfirms`, plus the keyverify-gated `Msg`-write locking in `tss.go` it pairs with (`applyShare` not-majority branch, `processTSSMsg` confirm-first branch) — revert both sites to the plain writes
   - `bifrost/tss/go-tss/common/types.go` — `KeyverifyRetryInterval`, `KeyverifyMaxRetries` config fields
   - `bifrost/tss/go-tss/common/tss_helper.go` — keyverify import + 4 `GetMsgRound` cases
   - `bifrost/tss/go-tss/blame/manager.go` — `GetAcceptedSharesSnapshot`
   - `config/config.go` + `config/default.yaml` — `keyverify_*` knobs (timeouts, retry interval, max retries)
   - `bifrost/tss/go-tss/tss/server.go` — `GetP2PComm`, `GetBroadcastChan`, `GetLocalNodePubKey` methods
   - `bifrost/tss/go-tss/tss/tss.go` — concrete impls of the above
   - `bifrost/tss/go-tss/cmd/tss/mock_tss_server.go` — mock stubs
   - `cmd/bifrost/health.go` — import, struct field, constructor param, route
   - `cmd/bifrost/health_test.go` — mock stubs + bumped constructor args
   - `cmd/bifrost/main.go` — import, MarkerStore construction, scheduler block, modified NewHealthServer call
4. Revert the `go.mod` pin to whatever tss-lib version is current. Run
   `go mod tidy && go mod vendor`.
5. `go build ./... && go test ./...`

## What this feature did

- Polled thornode height every 6 seconds.
- On any multiple-of-100 block, ran the tss-lib `keyverify` 4-round ECDSA
  subprotocol against every active ECDSA asgard vault this node was a member
  of, until each vault produced a `<home>/keyverify-<pubkey>.ok` marker.
- Failure attempts wrote a `<home>/keyverify-<pubkey>.fail` JSON with culprits
  (plus reporter pubkey, local round, and per-culprit round/receipt detail).
- Exposed `GET /status/keyverify` on the bifrost health server.
- Ran a per-attempt recovery ticker re-sending dropped round messages,
  hash-confirms, and share re-requests (metrics `tss_keyverify_retry_total`,
  `tss_keyverify_recovered_total`; see DESIGN_retry_and_blame_visibility.md).

## On-disk artifacts left behind after removal

Operators may want to clean up `<DefaultNodeHome>/keyverify-*.ok` and
`keyverify-*.fail` files. They are inert once this code is gone.

## Related cleanups to consider at the same time

- `bifrost/tss/go-tss/common/curve_lock.go` — the `WithCurve` mutex
  serializes unmarshal phases of every concurrent keysign. If contention
  shows up in profiles, the fast path is to detect whether the keyshare
  carries an embedded `Curve` field on every ECPoint (true for v3 fixtures,
  false for legacy) and skip the lock when present. This optimization can be
  evaluated when the v3 rollout settles and the keyverify feature is removed.
