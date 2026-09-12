#!/bin/sh
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"
shellcheck_bin=${SHELLCHECK_BIN:-shellcheck}
if ! command -v "$shellcheck_bin" >/dev/null 2>&1; then
    echo "ShellCheck is required to validate workflow run scripts; install it or set SHELLCHECK_BIN" >&2
    exit 1
fi
"$shellcheck_bin" scripts/build.sh scripts/compile.sh scripts/release.sh scripts/export-build.sh scripts/check-workflows.sh
if [ -n "${ACTIONLINT_BIN:-}" ]; then
    exec "$ACTIONLINT_BIN" -shellcheck "$shellcheck_bin" "$@"
fi
exec "${GO_BIN:-go}" run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 -shellcheck "$shellcheck_bin" "$@"
