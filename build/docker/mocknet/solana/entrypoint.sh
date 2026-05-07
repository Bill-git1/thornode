#!/bin/bash

set -uo pipefail

LEDGER_DIR="${LEDGER_DIR:-/tmp/test-ledger}"
LEDGER_SHRED_LIMIT="${LEDGER_SHRED_LIMIT:-500}"
VALIDATOR_RESTART_DELAY="${VALIDATOR_RESTART_DELAY:-5}"

start_validator() {
  while true; do
    solana-test-validator \
      --quiet \
      --ledger "$LEDGER_DIR" \
      --limit-ledger-size "$LEDGER_SHRED_LIMIT" \
      --url http://localhost:8899 &

    validator_pid=$!
    wait "$validator_pid"
    exit_code=$?
    echo "Solana test validator exited with code $exit_code; restarting in ${VALIDATOR_RESTART_DELAY}s..."
    sleep "$VALIDATOR_RESTART_DELAY"
  done
}

start_validator &
validator_wrapper_pid=$!

shutdown() {
  kill "$validator_wrapper_pid" 2>/dev/null || true
  wait "$validator_wrapper_pid" 2>/dev/null || true
}
trap shutdown EXIT SIGINT SIGTERM

# Check if the validator is running
echo "Waiting for the Solana validator to be fully up..."
RETRIES=30
while ! solana cluster-version; do
  if [ $RETRIES -le 0 ]; then
    echo "Validator failed to start after multiple attempts."
    exit 1
  fi
  echo "Validator not ready yet. Retrying in 5 seconds..."
  RETRIES=$((RETRIES - 1))
  sleep 5
done
echo "Validator is ready and reachable."

# Set local URL for Solana CLI
solana config set --url http://localhost:"$RPC_PORT"

# Generate a new keypair. This wallet isn't used, but needed to enable the airdrop command.
TEMP_KEYPAIR="/tmp/solana-keypair.json"
if [ ! -f $TEMP_KEYPAIR ]; then
  solana-keygen new --outfile $TEMP_KEYPAIR --no-passphrase
fi

# Set the generated keypair as the default wallet (CLI needs this to interact with the network)
solana config set --keypair $TEMP_KEYPAIR

# Display current Solana CLI configuration for verification
solana config get

# Perform airdrop to specified address with configurable amount
echo "Airdropping $AIRDROP_AMOUNT SOL to address $AIRDROP_ADDRESS..."
RETRIES=12
until solana airdrop "$AIRDROP_AMOUNT" "$AIRDROP_ADDRESS"; do
  if [ $RETRIES -le 0 ]; then
    echo "Airdrop failed after multiple attempts."
    exit 1
  fi
  echo "Airdrop failed. Retrying in 5 seconds..."
  RETRIES=$((RETRIES - 1))
  sleep 5
done

rm "$TEMP_KEYPAIR"

echo "Solana test validator setup complete and airdrop successful."

wait "$validator_wrapper_pid"
