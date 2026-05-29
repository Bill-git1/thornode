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
   - `bifrost/tss/go-tss/common/tss.go` — keyverify branches in the 4 dispatch switches
   - `bifrost/tss/go-tss/common/tss_helper.go` — keyverify import + 4 `GetMsgRound` cases
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
- Failure attempts wrote a `<home>/keyverify-<pubkey>.fail` JSON with culprits.
- Exposed `GET /status/keyverify` on the bifrost health server.

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
