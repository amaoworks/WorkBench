#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

cd "$repo_root/web"
npm ci
npm run build

cd "$repo_root"
exec ./scripts/compile.sh
