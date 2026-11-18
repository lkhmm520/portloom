#!/usr/bin/env bash
# Exercise the on-disk interlock with the exact v0.4.6 Server source and go.sum.
set -euo pipefail
cd "$(dirname "$0")/.."
legacy=9ca233b7fe9f098548bbe0585294c24527ddd923 # v0.4.6
if [[ $(git rev-parse --verify 'v0.4.6^{commit}') != "$legacy" ]]; then
  printf '%s\n' 'Need the exact v0.4.6 tag; fetch it before running this test.' >&2
  exit 1
fi
scratch=$(mktemp -d)
trap 'rm -rf -- "$scratch"' EXIT
mkdir "$scratch/source"
git archive "$legacy" | tar -x -C "$scratch/source"
(cd "$scratch/source" && go build -mod=readonly -ldflags '-X main.version=0.4.6' -o "$scratch/server-v0.4.6" ./cmd/server)
PORTLOOM_TEST_V046_SERVER="$scratch/server-v0.4.6" \
  go test ./internal/store -count=1 -v -run '^TestPublicationDowngrade'
