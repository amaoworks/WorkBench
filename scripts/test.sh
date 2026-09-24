#!/bin/sh
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
go_bin=${GO_BIN:-go}
if [ -n "${SQLC_BIN:-}" ]; then
    sqlc_bin=$SQLC_BIN
elif [ -x "$repo_root/.tools/bin/sqlc" ]; then
    sqlc_bin="$repo_root/.tools/bin/sqlc"
else
    sqlc_bin=sqlc
fi

cd "$repo_root"
generated_check=$(mktemp -d)
trap 'rm -rf "$generated_check"' EXIT HUP INT TERM
snapshot_generated() {
    destination=$1
    for generated in internal/foundation/database/sqlc internal/modules/*/sqlc; do
        [ -d "$generated" ] || continue
        mkdir -p "$destination/$(dirname "$generated")"
        cp -R "$generated" "$destination/$generated"
    done
}
snapshot_generated "$generated_check/before"
"$sqlc_bin" generate
snapshot_generated "$generated_check/after"
if ! diff -ru "$generated_check/before" "$generated_check/after"; then
    echo "sqlc generated files were stale; review regenerated files and run tests again" >&2
    exit 1
fi
"$go_bin" test ./...
"$go_bin" test -tags dev ./internal/modules/investment -run '^TestDevChart'
node --test scripts/dev.test.mjs

cd "$repo_root/web"
npm run lint
GO_BIN="$go_bin" npm run test:contracts
npm run test:investment
npm run build
