#!/bin/bash

set -euo pipefail

NET=$1

# only mainnet and stagenet are supported
if [ "${NET}" != "mainnet" ] && [ "${NET}" != "stagenet" ]; then
  echo "Unsupported network: ${NET}. Only 'mainnet' and 'stagenet' are supported."
  exit 1
fi

if [ "${NET}" = "mainnet" ]; then
  CHAIN_ID=thorchain-1
  SNAPSHOTS_URL=https://snapshots.ninerealms.com
else
  CHAIN_ID=thorchain-stagenet-2
  SNAPSHOTS_URL=https://stagenet-snapshots.ninerealms.com
  EXTRA_ARGS="-e THOR_SEED_NODES_ENDPOINT=https://stagenet-thornode.ninerealms.com/thorchain/nodes"
fi

EXTRA_ARGS="${EXTRA_ARGS-}"
NET_DIR=tmp/sync-test/${NET}

# download snapshot
if [ ! -d "${NET_DIR}/data" ]; then
  mkdir -p "${NET_DIR}/data"
  LATEST_SNAPSHOT_KEY=$(
    docker run --rm --entrypoint sh minio/minio:latest -c "
    mc config host add minio ${SNAPSHOTS_URL} '' '' >/dev/null;
    mc ls minio/snapshots/thornode --json" | tail -n1 | jq -r .key
  )
  aria2c --split=16 --max-concurrent-downloads=16 --max-connection-per-server=16 \
    --continue --min-split-size=100M --out="${NET_DIR}/${LATEST_SNAPSHOT_KEY}" \
    "${SNAPSHOTS_URL}/snapshots/thornode/${LATEST_SNAPSHOT_KEY}"
  tar xf "${NET_DIR}/${LATEST_SNAPSHOT_KEY}" -C "${NET_DIR}/data"
fi

# build and start thornode
BUILDTAG=${NET} BRANCH=${NET} make docker-gitlab-build
# trunk-ignore(shellcheck/SC2086)
docker run --rm ${EXTRA_ARGS} \
  -v "$(pwd)/${NET_DIR}/data:/root/.thornode" \
  -e "CHAIN_ID=${CHAIN_ID}" \
  -e "NET=${NET}" \
  -p 1317:1317 \
  -p 27147:27147 \
  "registry.gitlab.com/thorchain/thornode:${NET}"
