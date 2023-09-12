#!/usr/bin/env bash

# prompt for gitlab merge request id
read -rp "Enter Gitlab Merge Request ID: " MR

git branch -D mr-"$MR"
git fetch origin merge-requests/"$MR"/head:mr-"$MR" && git checkout mr-"$MR"
git push -f --no-verify
git checkout "@{-1}"

echo
echo "Navigate to https://gitlab.com/thorchain/thornode/-/pipelines/new and run the pipeline for branch: mr-$MR"
