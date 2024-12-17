#!/bin/bash

set -o pipefail

. "$(dirname "$0")/core.sh"

if [ "$NET" = "mocknet" ]; then
  echo "Loading unsafe init for mocknet..."
  . "$(dirname "$0")/core-unsafe.sh"
fi

########################################################################################
# Genesis Init
########################################################################################

genesis_init() {
  init_chain
  create_thor_user "$SIGNER_NAME" "$SIGNER_PASSWD" "$SIGNER_SEED_PHRASE"

  VALIDATOR=$(thornode tendermint show-validator | thornode pubkey --bech cons)
  NODE_ADDRESS=$(echo "$SIGNER_PASSWD" | thornode keys show thorchain -a --keyring-backend file)
  NODE_PUB_KEY=$(echo "$SIGNER_PASSWD" | thornode keys show thorchain -p --keyring-backend file | thornode pubkey)
  VERSION=$(fetch_version)

  NODE_IP_ADDRESS=${EXTERNAL_IP:=$(curl -s http://whatismyip.akamai.com)}
  add_node_account "$NODE_ADDRESS" "$VALIDATOR" "$NODE_PUB_KEY" "$VERSION" "$NODE_ADDRESS" "$NODE_PUB_KEY_ED25519" "$NODE_IP_ADDRESS"
  add_account "$NODE_ADDRESS" rune 100000000000

  # disable default bank transfer, and opt to use our own custom one
  disable_bank_send

  # for mocknet, add initial balances
  echo "Using NET $NET"
  if [ "$NET" = "mocknet" ]; then
    echo "Setting up accounts"

    # smoke test accounts
    add_account tthor1z63f3mzwv3g75az80xwmhrawdqcjpaekk0kd54 rune 5000000000000
    add_account tthor1wz78qmrkplrdhy37tw0tnvn0tkm5pqd6zdp257 rune 25000000000100
    add_account tthor18f55frcvknxvcpx2vvpfedvw4l8eutuhku0uj6 rune 25000000000100
    add_account tthor1xwusttz86hqfuk5z7amcgqsg7vp6g8zhsp5lu2 rune 5090000000000

    # local cluster accounts (2M RUNE)
    add_account tthor1uuds8pd92qnnq0udw0rpg0szpgcslc9p8lluej rune 200000000000000 # cat
    add_account tthor1zf3gsk7edzwl9syyefvfhle37cjtql35h6k85m rune 200000000000000 # dog
    add_account tthor13wrmhnh2qe98rjse30pl7u6jxszjjwl4f6yycr rune 200000000000000 # fox
    add_account tthor1qk8c8sfrmfm0tkncs0zxeutc8v5mx3pjj07k4u rune 200000000000000 # pig

    # simulation master
    add_account tthor1f4l5dlqhaujgkxxqmug4stfvmvt58vx2tspx4g rune 100000000000000 # master

    # mint to reserve for mocknet
    reserve 22000000000000000

    set_bond_module # set bond module balance for invariant

    # deploy evm contracts
    deploy_evm_contracts
  fi

  if [ "$NET" = "stagenet" ]; then
    if [ -z ${FAUCET+x} ]; then
      echo "env variable 'FAUCET' is not defined: should be a sthor address"
      exit 1
    fi
    add_account "$FAUCET" rune 40000000000000000

    reserve 5000000000000000

    set_bond_module # set bond module balance for invariant
  fi

  if [ -n "${ETH_CONTRACT+x}" ]; then
    echo "ETH Contract Address: $ETH_CONTRACT"
    set_eth_contract "$ETH_CONTRACT"
  fi
  if [ -n "${AVAX_CONTRACT+x}" ]; then
    echo "AVAX Contract Address: $AVAX_CONTRACT"
    set_avax_contract "$AVAX_CONTRACT"
  fi
  if [ -n "${BSC_CONTRACT+x}" ]; then
    echo "BSC Contract Address: $BSC_CONTRACT"
    set_bsc_contract "$BSC_CONTRACT"
  fi
  if [ -n "${BASE_CONTRACT+x}" ]; then
    echo "BASE Contract Address: $BASE_CONTRACT"
    set_base_contract "$BASE_CONTRACT"
  fi

  echo "Genesis content"
  cat ~/.thornode/config/genesis.json
  thornode genesis validate --trace
}

########################################################################################
# Main
########################################################################################

# genesis on first init if we are the genesis node
if [ ! -f ~/.thornode/config/genesis.json ]; then
  genesis_init
fi

# render tendermint and cosmos configuration files
thornode render-config

export SIGNER_NAME
export SIGNER_PASSWD
exec thornode start
