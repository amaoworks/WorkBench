#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
go_bin=${GO_BIN:-go}
if [ -n "${SQLC_BIN:-}" ]; then
    sqlc_bin=$SQLC_BIN
elif [ -x "$repo_root/.tools/bin/sqlc" ]; then
    sqlc_bin="$repo_root/.tools/bin/sqlc"
else
    sqlc_bin=sqlc
fi

cd "$repo_root"
"$sqlc_bin" generate
if ! git diff --exit-code -- internal/foundation/database/sqlc internal/modules/todo/sqlc; then
    echo "sqlc generated files are stale; run: $sqlc_bin generate" >&2
    exit 1
fi
"$go_bin" test ./...

cd "$repo_root/web"
npm run lint
npm run build
