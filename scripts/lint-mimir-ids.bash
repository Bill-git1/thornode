#!/bin/bash

set -euo pipefail

# This script compares mimir ids to the develop branch to assert ids are immutable.
echo "Asserting mimir ID immutability..."

git fetch https://gitlab.com/thorchain/thornode.git develop

# skip for develop
if git merge-base --is-ancestor "$(git rev-parse HEAD)" "$(git rev-parse FETCH_HEAD)"; then
  echo "Skipping mimir ID lint for commit in develop ($(git rev-parse FETCH_HEAD))."
  exit 0
fi

# extract mimir ids in both current branch and develop
go run tools/mimir-ids/main.go >/tmp/mimir-ids-current
git checkout FETCH_HEAD
git checkout - -- tools scripts
go run tools/mimir-ids/main.go >/tmp/mimir-ids-develop
git checkout -

# print the diff, but do not fail the script
diff -u --color=always /tmp/mimir-ids-develop /tmp/mimir-ids-current || true

# assert that develop is a prefix of current
size=$(wc -c </tmp/mimir-ids-develop)
if ! cmp -n "$size" /tmp/mimir-ids-develop /tmp/mimir-ids-current; then
  echo "Mimir IDs are immutable. Do not remove existing IDs or insert new ones before the end. New IDs must be appended."
  exit 1
fi
