#!/bin/sh

set -e

BLOCK_TIME=${BLOCK_TIME:-1}
INIT_BLOCKS=400 # Keep aligned with waitForZecRPCReadinessWithClock.
BOOTSTRAP_TIMEOUT=${BOOTSTRAP_TIMEOUT:-480}
RPC_URL="http://127.0.0.1:18232"

zebrad --config /etc/zebrad.toml &
ZEBRAD_PID=$!
trap 'kill "$ZEBRAD_PID" 2>/dev/null || true' EXIT
trap 'exit 1' INT TERM

echo "waiting for zebrad RPC to be ready..."
deadline=$(($(date +%s) + BOOTSTRAP_TIMEOUT))
while kill -0 "$ZEBRAD_PID" 2>/dev/null; do
  if curl --max-time 5 -sf -X POST "$RPC_URL" \
    -H "Content-Type: application/json" \
    -d '{"method":"getblockcount","params":[],"id":1}' >/dev/null 2>&1; then
    break
  fi
  if [ "$(date +%s)" -ge "$deadline" ]; then
    echo "timed out waiting for zebrad RPC" >&2
    exit 1
  fi
  sleep 1
done
kill -0 "$ZEBRAD_PID"

# One miner and one block per request, including warm-up. A long initial batch
# can keep resetting Zebra's mempool while clients begin funding. Mine the first
# INIT_BLOCKS without a delay, then use the regular block interval. The simulator
# waits past this count before funding and never calls generate.
# 100 blocks for coinbase maturity + 20 inputs per user * 12 users = 340;
# 400 leaves headroom for funding all simulation users.
blocks_mined=0
while kill -0 "$ZEBRAD_PID" 2>/dev/null; do
  # Stop on a timeout: the request could still be mining. Do not overlap it
  # with another request. HTTP 200 with a JSON-RPC error must also stop mining.
  if ! response=$(curl --max-time 30 -sSf -X POST "$RPC_URL" \
    -H "Content-Type: application/json" \
    -d '{"method":"generate","params":[1],"id":1}'); then
    echo "single-block generation request failed; stopping mining" >&2
    exit 1
  fi
  if ! printf '%s' "$response" | grep -Eq '"result"[[:space:]]*:[[:space:]]*\[[[:space:]]*"[[:xdigit:]]{64}"[[:space:]]*\]'; then
    echo "single-block generation failed: $response" >&2
    exit 1
  fi
  blocks_mined=$((blocks_mined + 1))
  if [ "$blocks_mined" -ge "$INIT_BLOCKS" ]; then
    sleep "$BLOCK_TIME"
  fi
done

wait "$ZEBRAD_PID"
