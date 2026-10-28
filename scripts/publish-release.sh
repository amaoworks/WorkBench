#!/usr/bin/env bash
set -euo pipefail

cd "${1:?set the release packages directory}"
sha256sum --check SHA256SUMS
printf 'Released from: main\nCommit: %s\n\nContainer: %s:%s\n\nDigest: %s@%s\n\nSee the deployment guide in the attached packages.\n' \
    "$GITHUB_SHA" "$IMAGE" "$VERSION" "$IMAGE" "$DIGEST" > "$RUNNER_TEMP/release-notes.md"

latest=
extra=(--latest=false)
if [[ "$VERSION" == *-* ]]; then
    extra+=(--prerelease)
else
    versions=$(gh api --paginate "repos/$GH_REPO/releases?per_page=100" \
        --jq '.[] | select(.draft == false and .prerelease == false) | .tag_name')
    latest=$(printf '%s\n' "$VERSION" "$versions" | grep -E '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' | LC_ALL=C sort -V | tail -n 1)
    if [[ "$latest" == "$VERSION" ]]; then extra=(--latest); fi
fi

gh release create "$VERSION" ./*.tar.gz ./SHA256SUMS \
    --verify-tag --title "$VERSION" --notes-file "$RUNNER_TEMP/release-notes.md" \
    --generate-notes "${extra[@]}"
if [[ "$latest" == "$VERSION" ]]; then
    docker buildx imagetools create --tag "$IMAGE:latest" "$IMAGE@$DIGEST"
fi
