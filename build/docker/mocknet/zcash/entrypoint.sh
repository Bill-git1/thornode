#!/bin/sh

set -e

BLOCK_TIME=${BLOCK_TIME:-1}
RPC_URL="http://127.0.0.1:18232"
# Pre-mine enough blocks up front that the master holds more MATURE coinbases than
# the seed funding will ever spend, independent of how fast the sim reaches funding.
# Transparent coinbases need 100 confirmations, and ZEC seed funding spends ~20*(#users)
# coinbases (chainSeedTxs=20, up to 12 user mnemonics). Required mature supply therefore
# = 100 (maturity) + 20*12 (worst-case fan-out demand) = 340; 400 leaves headroom so a
# fast boot can never reach funding before enough coinbases have matured. This one RPC
# `generate` burst is effectively instant in Regtest (no PoW).
INIT_BLOCKS=${INIT_BLOCKS:=400}

# Start zebrad in the background with config
zebrad --config /etc/zebrad.toml &
ZEBRAD_PID=$!

# Wait for the RPC server to be ready
echo "waiting for zebrad RPC to be ready..."
while true; do
  if curl -sf -X POST "$RPC_URL" \
    -H "Content-Type: application/json" \
    -d '{"method":"getblockcount","params":[],"id":1}' >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
echo "zebrad RPC is ready"

# Generate initial blocks
echo "generating initial blocks..."
curl -sf -X POST "$RPC_URL" \
  -H "Content-Type: application/json" \
  -d '{"method":"generate","params":['"${INIT_BLOCKS}"'],"id":1}'
echo ""
echo "initial blocks generated"

# Mine a new block every BLOCK_TIME seconds
while kill -0 "$ZEBRAD_PID" 2>/dev/null; do
  curl -sf -X POST "$RPC_URL" \
    -H "Content-Type: application/json" \
    -d '{"method":"generate","params":[1],"id":1}' >/dev/null 2>&1 || true
  sleep "$BLOCK_TIME"
done

# If zebrad exits, exit the container
wait "$ZEBRAD_PID"
