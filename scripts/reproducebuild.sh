#!/bin/bash
# Given a specific commit, build the thornode binary and produce the hash.
set -euo pipefail

# Default to current head commit and mainnet build.
COMMIT=$(git rev-parse HEAD)
TAG="mainnet"

read -r -p "Commit [$COMMIT]: " COMMIT_
read -r -p "Build tag [$TAG]: " TAG_

COMMIT=${COMMIT_:-$COMMIT}
TAG=${TAG_:-$TAG}

TMP=$(mktemp -d)
cleanup() { rm "$TMP" -rf; }
trap cleanup EXIT

# Work out of a fresh clone of the repository so as to not
# need to clean/stash local working copy.
git clone https://gitlab.com/thorchain/thornode.git "$TMP"

pushd "$TMP"

git fetch origin "$COMMIT"
git checkout "$COMMIT"

git branch -d "$TAG" || true
git checkout -b "$TAG"

make docker-gitlab-build

HASH=$(docker run --rm --entrypoint /bin/sh -it registry.gitlab.com/thorchain/thornode:"$TAG" -c 'sha256sum /usr/bin/thornode')

popd

cat <<EOF

    On commit:        $COMMIT
    Using build tag:  $TAG

    Produced the following binaries:

    $HASH

EOF
