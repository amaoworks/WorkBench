#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"
: "${VERSION:?set VERSION to a release tag, for example v1.0.0}"
if ! printf '%s\n' "$VERSION" | LC_ALL=C grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$'; then
    echo "VERSION must be vMAJOR.MINOR.PATCH with an optional prerelease suffix" >&2
    exit 1
fi
export VERSION
COMMIT=${COMMIT:-$(git rev-parse HEAD)}
BUILD_DATE=${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}
export COMMIT BUILD_DATE
release_dir="$repo_root/dist/release/$VERSION"
mkdir -p "$release_dir"
staging=$(mktemp -d)
trap 'rm -rf "$staging"' EXIT HUP INT TERM

cd "$repo_root/web"
npm ci
npm run build
cd "$repo_root"

for arch in amd64 arm64; do
    name="workbench_${VERSION}_linux_${arch}"
    mkdir -p "$staging/$name"
    GOOS=linux GOARCH="$arch" OUTPUT="$staging/$name/workbench" ./scripts/compile.sh
    cp README.md "$staging/$name/"
    cp -R doc deploy "$staging/$name/"
    tar -czf "$release_dir/$name.tar.gz" -C "$staging" "$name"
done
tar -czf "$release_dir/workbench_${VERSION}_deployment.tar.gz" compose.yaml .env.example deploy doc README.md
cd "$release_dir"
sha256sum ./*.tar.gz > SHA256SUMS
printf 'Release artifacts: %s\n' "$release_dir"
