#!/bin/bash

set -euo pipefail

# This script compares versioned functions to the develop branch to ensure no logic
# changes in historical versions that would cause consensus failure.

VERSION=$(awk -F. '{ print $2 }' version)
CI_MERGE_REQUEST_TITLE=${CI_MERGE_REQUEST_TITLE-}

git fetch https://gitlab.com/thorchain/thornode.git develop

# skip for develop
if git merge-base --is-ancestor "$(git rev-parse HEAD)" "$(git rev-parse FETCH_HEAD)"; then
  echo "Skipping lint for commit in develop ($(git rev-parse FETCH_HEAD))."
  exit 0
fi

echo -n "Checking unversioned tests for versioned managers... "
go run tools/current-test-versions/main.go --managers >/tmp/testing-old-versions
if [ -s /tmp/testing-old-versions ]; then
  echo "Detected use of versioned manager in unversioned test."
  if [[ $CI_MERGE_REQUEST_TITLE == *"#check-lint-warning"* ]]; then
    echo "Merge request is marked unsafe."
  else
    echo 'In the following locations, switch to the current manager, or add "#check-lint-warning" to the PR description.'
    cat /tmp/testing-old-versions
    exit 1
  fi
fi
echo "OK"

go run tools/versioned-functions/main.go --version="$VERSION" >/tmp/versioned-fns-current
git checkout FETCH_HEAD
git checkout - -- tools scripts
go run tools/versioned-functions/main.go --version="$VERSION" >/tmp/versioned-fns-develop
git checkout -

gofumpt -w /tmp/versioned-fns-develop /tmp/versioned-fns-current

if ! diff -u -F '^func' -I '^//' --color=always /tmp/versioned-fns-develop /tmp/versioned-fns-current; then
  echo "Detected change in versioned function."
  if [[ $CI_MERGE_REQUEST_TITLE == *"#check-lint-warning"* ]]; then
    echo "Merge request is marked unsafe."
  else
    echo 'Correct the change, add a new versioned function, or add "#check-lint-warning" to the PR description.'
    exit 1
  fi
fi
