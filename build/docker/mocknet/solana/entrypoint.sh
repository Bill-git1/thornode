#!/bin/bash

set -e

# Start test validator
solana-test-validator --ledger /tmp/test-ledger --reset --limit-ledger-size --url http://localhost:8899 &

# Check if the validator is running
echo "Waiting for the Solana validator to be fully up..."
RETRIES=10
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
solana airdrop "$AIRDROP_AMOUNT" "$AIRDROP_ADDRESS"

rm $TEMP_KEYPAIR

echo "Solana test validator setup complete and airdrop successful."

wait
