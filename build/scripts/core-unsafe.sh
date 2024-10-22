#!/bin/sh

set -o pipefail

deploy_evm_contracts() {
  pip3 install retry
  for CHAIN in ETH AVAX BSC; do
    (
      # deploy contract and get address from output
      echo "Deploying $CHAIN contracts"
      if ! python3 scripts/evm/evm-tool.py --chain $CHAIN --rpc "$(eval echo "\$${CHAIN}_HOST")" --action deploy >/tmp/evm-tool-$CHAIN.log 2>&1; then
        cat /tmp/evm-tool-$CHAIN.log && exit 1
      fi
      cat /tmp/evm-tool-$CHAIN.log
      CONTRACT=$(grep </tmp/evm-tool-$CHAIN.log "Router Contract Address" | awk '{print $NF}')

      # add contract address to genesis
      echo "$CHAIN Contract Address: $CONTRACT"
      jq --arg CHAIN "$CHAIN" --arg CONTRACT "$CONTRACT" \
        '.app_state.thorchain.chain_contracts += [{"chain": $CHAIN, "router": $CONTRACT}]' \
        ~/.thornode/config/genesis.json >/tmp/genesis-$CHAIN.json
      mv /tmp/genesis-$CHAIN.json ~/.thornode/config/genesis.json
    ) &
  done
  wait
}

init_mocknet() {
  NODE_ADDRESS=$(echo "$SIGNER_PASSWD" | thornode keys show "$SIGNER_NAME" -a --keyring-backend file)

  if [ "$PEER" = "none" ]; then
    echo "Missing PEER"
    exit 1
  fi

  # wait for peer
  until curl -s "$PEER:$PORT_RPC" 1>/dev/null 2>&1; do
    echo "Waiting for peer: $PEER:$PORT_RPC"
    sleep 3
  done

  printf "%s\n" "$SIGNER_PASSWD" | thornode tx thorchain deposit 100000000000000 RUNE "bond:$NODE_ADDRESS" --node tcp://"$PEER":26657 --from "$SIGNER_NAME" --keyring-backend=file --chain-id "$CHAIN_ID" --yes

  # send bond

  sleep 10 # wait for thorchain to commit a block , otherwise it get the wrong sequence number

  NODE_PUB_KEY=$(echo "$SIGNER_PASSWD" | thornode keys show thorchain --pubkey --keyring-backend=file | thornode pubkey)
  VALIDATOR=$(thornode tendermint show-validator | thornode pubkey --bech cons)

  # set node keys
  until printf "%s\n" "$SIGNER_PASSWD" | thornode tx thorchain set-node-keys "$NODE_PUB_KEY" "$NODE_PUB_KEY_ED25519" "$VALIDATOR" --node tcp://"$PEER":26657 --from "$SIGNER_NAME" --keyring-backend=file --chain-id "$CHAIN_ID" --yes; do
    sleep 5
  done

  # add IP address
  sleep 10 # wait for thorchain to commit a block

  NODE_IP_ADDRESS=${EXTERNAL_IP:=$(curl -s http://whatismyip.akamai.com)}
  until printf "%s\n" "$SIGNER_PASSWD" | thornode tx thorchain set-ip-address "$NODE_IP_ADDRESS" --node tcp://"$PEER":26657 --from "$SIGNER_NAME" --keyring-backend=file --chain-id "$CHAIN_ID" --yes; do
    sleep 5
  done

  sleep 10 # wait for thorchain to commit a block
  # set node version
  until printf "%s\n" "$SIGNER_PASSWD" | thornode tx thorchain set-version --node tcp://"$PEER":26657 --from "$SIGNER_NAME" --keyring-backend=file --chain-id "$CHAIN_ID" --yes; do
    sleep 5
  done
}

# set external ip to localhost in mocknet
if [ "$NET" = "mocknet" ]; then
  EXTERNAL_IP="$(hostname -i)"
  export EXTERNAL_IP
fi
