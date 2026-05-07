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

    if [ "${AUTO_BOND:-false}" = "true" ]; then
      until curl -s "$PEER:$PORT_RPC" 1>/dev/null 2>&1; do
        echo "Waiting for peer: $PEER:$PORT_RPC"
        sleep 3
      done

      printf "%s\n" "$SIGNER_PASSWD" | thornode tx thorchain deposit 100000000000000 RUNE "bond:$NODE_ADDRESS" \
        --node "tcp://$PEER:${PORT_RPC:-26657}" --from "$SIGNER_NAME" --keyring-backend=file --chain-id "$CHAIN_ID" --yes
      sleep 8

      NODE_PUB_KEY=$(echo "$SIGNER_PASSWD" | thornode keys show thorchain --pubkey --keyring-backend=file | thornode pubkey)
      NODE_PUB_KEY_ED25519=$(printf "%s\n" "$SIGNER_SEED_PHRASE" | thornode ed25519)
      VALIDATOR=$(thornode tendermint show-validator | thornode pubkey --bech cons)

      until printf "%s\n%s\n" "$SIGNER_PASSWD" "$SIGNER_PASSWD" | thornode tx thorchain set-node-keys "$NODE_PUB_KEY" "$NODE_PUB_KEY_ED25519" "$VALIDATOR" \
        --node "tcp://$PEER:${PORT_RPC:-26657}" --from "$SIGNER_NAME" --keyring-backend=file --chain-id "$CHAIN_ID" --yes; do
        sleep 5
      done
      sleep 4

      NODE_IP_ADDRESS=${EXTERNAL_IP:=$(hostname -i)}
      until printf "%s\n%s\n" "$SIGNER_PASSWD" "$SIGNER_PASSWD" | thornode tx thorchain set-ip-address "$NODE_IP_ADDRESS" \
        --node "tcp://$PEER:${PORT_RPC:-26657}" --from "$SIGNER_NAME" --keyring-backend=file --chain-id "$CHAIN_ID" --yes; do
        sleep 5
      done
      sleep 4

      printf "%s\n%s\n" "$SIGNER_PASSWD" "$SIGNER_PASSWD" | thornode tx thorchain set-version \
        --node "tcp://$PEER:${PORT_RPC:-26657}" --from "$SIGNER_NAME" --keyring-backend=file --chain-id "$CHAIN_ID" --yes
    else
      echo "Send your bond to that address"
    fi
  fi
fi

# render tendermint and cosmos configuration files
thornode render-config

export SIGNER_NAME
export SIGNER_PASSWD
exec thornode start
