#!/bin/sh

SIGNER_NAME="${SIGNER_NAME:=thorchain}"
SIGNER_PASSWD="${SIGNER_PASSWD:=password}"
ZCASH_MASTER_ADDR="${ZCASH_MASTER_ADDR:=tmGn7qKhnpbnwZPsaPt9XXAQz1Q3HAUCrwh}"
BLOCK_TIME=${BLOCK_TIME:=1}
INIT_BLOCKS=${INIT_BLOCKS:=500}

zcashd \
  -regtest=1 \
  -txindex \
  -nuparams=4dec4df0:1 \
  -mineraddress="$ZCASH_MASTER_ADDR" \
  -minetolocalwallet=0 \
  -experimentalfeatures=1 \
  -lightwalletd=1 \
  -rpcuser="$SIGNER_NAME" \
  -rpcpassword="$SIGNER_PASSWD" \
  -rpcallowip=0.0.0.0/0 \
  -rpcbind=0.0.0.0 \
  -rpcbind="$(hostname)" &

# give time to zcashd to start
while true; do
  zcash-cli -regtest -rpcuser="$SIGNER_NAME" -rpcpassword="$SIGNER_PASSWD" generate "$INIT_BLOCKS" && break
  sleep 5
done

# mine a new block every BLOCK_TIME
while true; do
  zcash-cli -regtest -rpcuser="$SIGNER_NAME" -rpcpassword="$SIGNER_PASSWD" generate 1
  sleep "$BLOCK_TIME"
done
