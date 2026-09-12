#!/bin/sh
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"
go_bin=${GO_BIN:-go}
output=${OUTPUT:-"$repo_root/workbench"}
version=${VERSION:-dev}
commit=${COMMIT:-$(git rev-parse HEAD 2>/dev/null || printf unknown)}
build_date=${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}
mkdir -p "$(dirname "$output")"

# The UI must have been built before this step. SQLite is pure Go; tzdata is
# embedded by the entrypoint so both Linux packages and scratch images have it.
CGO_ENABLED=0 "$go_bin" build -trimpath -buildvcs=false \
    -ldflags "-s -w -X main.version=$version -X main.commit=$commit -X main.buildDate=$build_date" \
    -o "$output" ./cmd/workbench
