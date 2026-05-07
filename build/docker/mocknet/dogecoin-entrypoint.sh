#!/bin/sh

set -e

SIGNER_NAME="${SIGNER_NAME:=thorchain}"
SIGNER_PASSWD="${SIGNER_PASSWD:=password}"
MASTER_ADDR="${DOGE_MASTER_ADDR:=mtzUk1zTJzTdyC8Pz6PPPyCHTEL5RLVyDJ}"
BLOCK_TIME="${BLOCK_TIME:=1}"

# DOGE startup creates a burst of wallet RPCs during simulation user creation.
# Raise the daemon work queue above the default so queued importaddress calls do
# not fail with "Work queue depth exceeded" under local mocknet load.
dogecoind \
  -regtest \
  -txindex \
  -rpcworkqueue=256 \
  -rpcuser="$SIGNER_NAME" \
  -rpcpassword="$SIGNER_PASSWD" \
  -rpcallowip=0.0.0.0/0 \
  -rpcbind=127.0.0.1 \
  -rpcbind="$(hostname)" &

# give time to dogecoind to start
while true; do
  dogecoin-cli -regtest -rpcuser="$SIGNER_NAME" -rpcpassword="$SIGNER_PASSWD" generatetoaddress 1000 "$MASTER_ADDR" && break
  sleep 5
done

# mine a new block every BLOCK_TIME
while true; do
  dogecoin-cli -regtest -rpcuser="$SIGNER_NAME" -rpcpassword="$SIGNER_PASSWD" generatetoaddress 1 "$MASTER_ADDR"
  sleep "$BLOCK_TIME"
done
