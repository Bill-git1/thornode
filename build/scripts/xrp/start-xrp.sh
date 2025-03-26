#!/bin/bash

# Start xrpl
exec /opt/ripple/bin/rippled --conf /docker/scripts/xrp/rippled.cfg --standalone &
sleep 5

# Create block
rippled ledger_accept

# Fund master account
rippled submit snoPBrXtMeMyMHUVTgbuqAfg1SUTb '{"TransactionType": "Payment", "Account": "rHb9CJAWyB4rj91VRWn96DkukG4bwdtyTh", "Destination": "r3hmW9oETcVqFWdN8jpCXgeK9km4uqNv5N", "Amount": "100000000000000", "NetworkID": 1234}'

# Repeat block creation
while true; do
  rippled ledger_accept
  sleep 2
done
