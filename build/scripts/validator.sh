#!/bin/bash

set -o pipefail

. "$(dirname "$0")/core.sh"

if [ "$NET" = "mocknet" ]; then
  echo "Loading unsafe init for mocknet..."
  . "$(dirname "$0")/core-unsafe.sh"
fi

PEER="${PEER:=none}"          # the hostname of a seed node set as tendermint persistent peer
PEER_API="${PEER_API:=$PEER}" # the hostname of a seed node API if different

if [ ! -f ~/.thornode/config/genesis.json ]; then
  echo "Setting THORNode as Validator node"

  create_thor_user "$SIGNER_NAME" "$SIGNER_PASSWD" "$SIGNER_SEED_PHRASE"

  init_chain
  rm -rf ~/.thornode/config/genesis.json # set in thornode render-config

  if [ "$NET" = "mocknet" ]; then
    init_mocknet
  else
    NODE_ADDRESS=$(echo "$SIGNER_PASSWD" | thornode keys show "$SIGNER_NAME" -a --keyring-backend file)
    echo "Your THORNode address: $NODE_ADDRESS"
    echo "Send your bond to that address"
  fi
fi

# render tendermint and cosmos configuration files
thornode render-config

export SIGNER_NAME
export SIGNER_PASSWD
exec thornode start
