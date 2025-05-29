#!/usr/bin/env bash

# This script wraps execution of trunk when run in CI.

set -euo pipefail

SCRIPT_DIR="$(dirname "$0")"
BASE_BRANCH="origin/develop"
FLAGS="-j8 --ci"

if [ -n "${CI_MERGE_REQUEST_ID-}" ]; then
  # if go modules or trunk settings changed, also run with --all on merge requests
  if ! git diff --exit-code "$BASE_BRANCH" -- go.mod go.sum .trunk >/dev/null; then
    FLAGS="$FLAGS --all"
  # if there is a trunk-ignore comment change, also run with --all on merge requests
  elif git diff --unified=0 --no-prefix "$BASE_BRANCH" | sed '/^@@/d' | grep -q 'trunk-ignore'; then
    FLAGS="$FLAGS --all"
  # if this is a merge train, run with --all
  elif [ "${CI_MERGE_REQUEST_EVENT_TYPE-}" = "merge_train" ]; then
    FLAGS="$FLAGS --all"
  else
    FLAGS="$FLAGS --upstream $BASE_BRANCH"
  fi
else
  FLAGS="$FLAGS --all"
fi

# run trunk
echo "Running: $SCRIPT_DIR/trunk check $FLAGS"
# trunk-ignore(shellcheck/SC2086): expanding $FLAGS as flags
"$SCRIPT_DIR"/trunk check $FLAGS
