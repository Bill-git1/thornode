#!/bin/bash
set -euo pipefail

config="${TRON_NODE_TYPE}.conf"
extra_args=()

case "${TRON_NODE_TYPE}" in
mocknet-fullnode)
  heap_size="${TRON_HEAP_SIZE:-1536m}"
  if [[ -n ${TRON_SEED_NODE-} ]]; then
    sed -i "s/localhost:18888/${TRON_SEED_NODE}/" "$HOME/${config}"
    # Seed discovery finds the producer; active keeps the single-peer mocknet
    # connection dialed so the fullnode tip does not go stale mid-simulation.
    awk -v peer="${TRON_SEED_NODE}" '
      /^  active = \[/ {
        print "  active = ["
        print "    \"" peer "\""
        print "  ]"
        in_active = 1
        next
      }
      in_active && /^  \]/ {
        in_active = 0
        next
      }
      !in_active { print }
    ' "$HOME/${config}" >"$HOME/${config}.tmp"
    mv "$HOME/${config}.tmp" "$HOME/${config}"
  fi
  # Single-witness mocknet can receive a just-produced block before the
  # fullnode wall clock reaches the block timestamp. Keep Bifrost on the
  # fullnode while avoiding a BAD_BLOCK peer ban in local simulation.
  sed -i 's/needSyncCheck = true/needSyncCheck = false/' "$HOME/${config}"
  ;;
mocknet-supernode)
  heap_size="${TRON_HEAP_SIZE:-1024m}"
  extra_args=(--witness)
  # Mocknet uses a single local witness, so the default participation floor
  # eventually stalls block production with LOW_PARTICIPATION.
  sed -i 's/minParticipationRate = 15/minParticipationRate = 0/' "$HOME/${config}"
  ;;
testnet-nile)
  heap_size="${TRON_HEAP_SIZE:-16g}"
  ;;
mainnet)
  heap_size="${TRON_HEAP_SIZE:-24g}"
  ;;
*)
  echo "TRON_NODE_TYPE: '${TRON_NODE_TYPE}' not supported" >&2
  exit 1
  ;;
esac

exec /usr/bin/java -Xmx"${heap_size}" -XX:+HeapDumpOnOutOfMemoryError \
  -Dlogback.configurationFile=/scripts/logback.xml \
  -jar FullNode.jar -c "${config}" "${extra_args[@]}"
