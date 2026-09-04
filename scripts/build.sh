#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
go_bin=${GO_BIN:-go}

cd "$repo_root/web"
npm ci
npm run build

cd "$repo_root"
"$go_bin" build -trimpath -o workbench ./cmd/workbench

