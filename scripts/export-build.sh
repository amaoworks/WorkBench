#!/bin/sh
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"
: "${IMAGE:?set IMAGE to the local image to export}"
: "${VERSION:?set VERSION to the build version}"
: "${ARCH:?set ARCH to amd64 or arm64}"
case "$ARCH" in amd64|arm64) ;; *) echo "unsupported architecture: $ARCH" >&2; exit 1 ;; esac
case "$VERSION" in ''|*[!a-zA-Z0-9._-]*) echo "invalid build version" >&2; exit 1 ;; esac
image_arch=$(docker image inspect "$IMAGE" --format '{{.Architecture}}')
if [ "$image_arch" != "$ARCH" ]; then
    echo "image architecture $image_arch does not match $ARCH" >&2
    exit 1
fi
output="$repo_root/dist/build"
mkdir -p "$output"
staging=$(mktemp -d)
container=
cleanup() {
    if [ -n "$container" ]; then docker rm "$container" >/dev/null; fi
    rm -rf "$staging"
}
trap cleanup EXIT HUP INT TERM

# Copy the exact static binary already tested in the image. Creating a stopped
# container here does not initialize a workspace or require runtime credentials.
name="workbench_${VERSION}_linux_${ARCH}"
mkdir -p "$staging/$name"
container=$(docker create "$IMAGE")
docker cp "$container:/workbench" "$staging/$name/workbench"
chmod 0755 "$staging/$name/workbench"
cp README.md "$staging/$name/"
cp -R doc deploy "$staging/$name/"
tar -czf "$output/$name.tar.gz" -C "$staging" "$name"

# Keep docker save separate from gzip so a failed export cannot be masked by a
# successful compressor in POSIX sh.
docker save --output "$staging/image.tar" "$IMAGE"
gzip -c "$staging/image.tar" > "$output/${name}_docker.tar.gz"
tar -czf "$output/workbench_${VERSION}_deployment.tar.gz" compose.yaml .env.example deploy doc README.md
cd "$output"
sha256sum "$name.tar.gz" "${name}_docker.tar.gz" "workbench_${VERSION}_deployment.tar.gz" > SHA256SUMS
printf 'Build artifacts: %s\nDocker image: %s\n' "$output" "$IMAGE"
