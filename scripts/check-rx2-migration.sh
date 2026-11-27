#!/usr/bin/env bash
# Compare the local startup migration with the released resolver on disposable data.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
repo="$PWD"
tag="${ETHER_RX2_LEGACY_TAG:-v0.3.20}"
parent="${HOME}/.scratch/tmp"
mkdir -p "$parent"
scratch="$(mktemp -d "$parent/ether-rx2.XXXXXX")"
trap 'rm -rf "$scratch"' EXIT
mkdir "$scratch/release"
git archive "$tag" | tar -x -C "$scratch/release"
cp internal/app/rx2_matrix_test.go "$scratch/release/internal/app/"
cp internal/app/testdata/rx2/legacy_test.go "$scratch/release/internal/app/rx2_legacy_test.go"
cp internal/app/testdata/rx2/legacy_resolver.go "$scratch/release/internal/resolver/rx2_audit.go"
export ETHER_RX2_DIR="$scratch"
export ETHER_RX2_CASES="$repo/internal/app/testdata/rx2/cases.json"
(
  cd "$scratch/release"
  go test -p 2 ./internal/app -run '^TestRX2ReleasedBaseline$' -count=1
)
go test -p 2 ./internal/app -run '^TestRX2MigrationMatrix$' -count=1 -v
