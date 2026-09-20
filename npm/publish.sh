#!/bin/bash
# Build and publish the npm packages for a released version.
#
# Usage:  ./npm/publish.sh 0.2.0 [--dry-run]
#
# Takes the binaries from the GitHub release of that version rather than
# building its own, so what npm serves is the same file as the direct download
# and the same file the checksums cover.
set -e

VERSION="${1:?usage: publish.sh <version> [--dry-run]}"
DRY="${2:-}"
REPO="${LLMSH_REPO:-Priy6anshu/llmsh}"
SCOPE="@llmskillhub"
WRAPPER="llmskillhub"

HERE="$(cd "$(dirname "$0")" && pwd)"
WORK="$HERE/.work"
rm -rf "$WORK"; mkdir -p "$WORK"

# npm's os/cpu names, not Go's.
set -- "darwin arm64 darwin arm64" \
       "darwin amd64 darwin x64" \
       "linux amd64 linux x64" \
       "linux arm64 linux arm64" \
       "windows amd64 win32 x64"

deps=""
for spec in "$@"; do
  set -- $spec
  goos="$1"; goarch="$2"; npmos="$3"; npmcpu="$4"
  pkg="$SCOPE/llmsh-$npmos-$npmcpu"
  dir="$WORK/llmsh-$npmos-$npmcpu"
  ext=""; [ "$goos" = "windows" ] && ext=".exe"

  mkdir -p "$dir/bin"
  curl -fsSL -o "$dir/bin/llmsh$ext" \
    "https://github.com/$REPO/releases/download/v$VERSION/llmsh_${VERSION}_${goos}_${goarch}$ext"
  chmod +x "$dir/bin/llmsh$ext"

  cat > "$dir/package.json" <<EOF
{
  "name": "$pkg",
  "version": "$VERSION",
  "description": "llmsh binary for $npmos $npmcpu",
  "repository": "github:$REPO",
  "license": "MIT",
  "os": ["$npmos"],
  "cpu": ["$npmcpu"],
  "files": ["bin"]
}
EOF
  deps="$deps    \"$pkg\": \"$VERSION\",\n"
  printf '  %-34s %s\n' "$pkg" "$(du -h "$dir/bin/llmsh$ext" | cut -f1)"
done

# The wrapper. optionalDependencies, so npm installs the one platform package
# that matches and skips the rest without failing.
mkdir -p "$WORK/wrapper/bin"
cp "$HERE/run.js" "$WORK/wrapper/bin/llmsh.js"
cp "$HERE/../README.md" "$WORK/wrapper/README.md" 2>/dev/null || true
cat > "$WORK/wrapper/package.json" <<EOF
{
  "name": "$WRAPPER",
  "version": "$VERSION",
  "description": "Publish and install versioned skills for AI agents",
  "repository": "github:$REPO",
  "license": "MIT",
  "keywords": ["ai", "agent", "skills", "cli", "llm"],
  "bin": { "llmsh": "bin/llmsh.js" },
  "files": ["bin"],
  "optionalDependencies": {
$(printf "$deps" | sed '$ s/,$//')
  }
}
EOF

echo
for d in "$WORK"/llmsh-* "$WORK/wrapper"; do
  ( cd "$d" && npm publish --access public $DRY )
done
