#!/bin/bash

set -o pipefail

wait_for_evm_rpc() {
  local chain="$1"
  local rpc="$2"
  local require_accounts="$3"

  echo "Waiting for $chain RPC: $rpc"
  python3 - "$chain" "$rpc" "$require_accounts" <<'PY'
import sys
import time

import requests

chain, rpc, require_accounts = sys.argv[1], sys.argv[2], sys.argv[3] == "1"
deadline = time.time() + 60
last_err = "rpc not ready"
session = requests.Session()

while time.time() < deadline:
    try:
        response = session.post(
            rpc,
            json={"jsonrpc": "2.0", "method": "net_version", "params": [], "id": 1},
            timeout=3,
        )
        response.raise_for_status()
        data = response.json()
        if "result" not in data:
            raise RuntimeError(f"missing net_version result: {data}")

        if require_accounts:
            response = session.post(
                rpc,
                json={"jsonrpc": "2.0", "method": "eth_accounts", "params": [], "id": 1},
                timeout=3,
            )
            response.raise_for_status()
            data = response.json()
            accounts = data.get("result") or []
            if not accounts:
                raise RuntimeError("eth_accounts is empty")

        print(f"{chain} RPC ready", flush=True)
        sys.exit(0)
    except Exception as err:
        last_err = err
        time.sleep(1)

print(f"{chain} RPC not ready after 60s: {last_err}", file=sys.stderr, flush=True)
sys.exit(1)
PY
}

deploy_evm_contracts() {
  for CHAIN in ETH AVAX BASE POL; do
    RPC_VAR="${CHAIN}_HOST"
    RPC_URL="$(eval echo "\$${RPC_VAR}")"
    REQUIRE_ACCOUNTS=0
    if [[ $CHAIN == "ETH" || $CHAIN == "BASE" || $CHAIN == "BSC" ]]; then
      REQUIRE_ACCOUNTS=1
    fi

    wait_for_evm_rpc "$CHAIN" "$RPC_URL" "$REQUIRE_ACCOUNTS" || return 1

    # deploy contract and get address from output
    echo "Deploying $CHAIN contracts"
    if ! PYTHONUNBUFFERED=1 python3 scripts/evm/evm-tool.py --chain "$CHAIN" --rpc "$RPC_URL" --action deploy >/tmp/evm-tool-$CHAIN.log 2>&1; then
      cat /tmp/evm-tool-$CHAIN.log
      return 1
    fi
    cat /tmp/evm-tool-$CHAIN.log
    CONTRACT=$(grep </tmp/evm-tool-$CHAIN.log "Router Contract Address" | awk '{print $NF}')
    if [ -z "$CONTRACT" ]; then
      echo "Failed to determine $CHAIN router contract address"
      return 1
    fi

    # add contract address to genesis
    echo "$CHAIN Contract Address: $CONTRACT"

    jq --arg CHAIN "$CHAIN" --arg CONTRACT "$CONTRACT" \
      '.app_state.thorchain.chain_contracts += [{"chain": $CHAIN, "router": $CONTRACT}]' \
      ~/.thornode/config/genesis.json >/tmp/genesis-$CHAIN.json
    mv /tmp/genesis-$CHAIN.json ~/.thornode/config/genesis.json
  done
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

  sleep 2 # wait for thorchain to commit a block , otherwise it get the wrong sequence number

  NODE_PUB_KEY=$(echo "$SIGNER_PASSWD" | thornode keys show thorchain --pubkey --keyring-backend=file | thornode pubkey)
  VALIDATOR=$(thornode tendermint show-validator | thornode pubkey --bech cons)

  # set node keys
  until printf "%s\n" "$SIGNER_PASSWD" | thornode tx thorchain set-node-keys "$NODE_PUB_KEY" "$NODE_PUB_KEY_ED25519" "$VALIDATOR" --node tcp://"$PEER":26657 --from "$SIGNER_NAME" --keyring-backend=file --chain-id "$CHAIN_ID" --yes; do
    sleep 5
  done

  # add IP address
  sleep 2 # wait for thorchain to commit a block

  NODE_IP_ADDRESS=${EXTERNAL_IP:=$(curl -s http://whatismyip.akamai.com)}
  until printf "%s\n" "$SIGNER_PASSWD" | thornode tx thorchain set-ip-address "$NODE_IP_ADDRESS" --node tcp://"$PEER":26657 --from "$SIGNER_NAME" --keyring-backend=file --chain-id "$CHAIN_ID" --yes; do
    sleep 5
  done

  sleep 2 # wait for thorchain to commit a block
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
