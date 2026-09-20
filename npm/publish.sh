#!/bin/bash
# Build and publish the npm packages for a released version.
#
# Usage:  ./npm/publish.sh <version> [--dry-run] [--provenance]
#
# The binaries come from the GitHub release rather than being rebuilt here, so
# what npm serves is byte-for-byte what the direct download and install.sh
# serve, and the published SHA256SUMS covers all three. Each download is
# checked against that file before it is packaged: fetching a binary and
# shipping it unverified would put a hole in the middle of a chain whose whole
# point is that there is not one.
#
# One script for CI and for a person, so the thing that runs unattended is the
# thing that was run by hand first.
set -euo pipefail

VERSION="${1:?usage: publish.sh <version> [--dry-run] [--provenance]}"
shift || true
NPM_ARGS=()
for a in "$@"; do
  case "$a" in
    --dry-run)    NPM_ARGS+=(--dry-run) ;;
    --provenance) NPM_ARGS+=(--provenance) ;;
    *) echo "publish.sh: unknown option $a" >&2; exit 2 ;;
  esac
done

REPO="${LLMSH_REPO:-Priy6anshu/llmsh}"
SCOPE="${LLMSH_NPM_SCOPE:-@llmskillhub}"
WRAPPER="${LLMSH_NPM_NAME:-llmskillhub}"
BASE="https://github.com/$REPO/releases/download/v$VERSION"

HERE="$(cd "$(dirname "$0")" && pwd)"
WORK="$HERE/.work"
rm -rf "$WORK"; mkdir -p "$WORK"

sha() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
  else shasum -a 256 "$1" | awk '{print $1}'; fi
}

echo "llmsh $VERSION -> npm  ${NPM_ARGS[*]:-}"

# Say which version is missing, and which ones are not.
#
# This packages a release rather than building one, so a version that was
# never released has nothing to package. Left to curl that is "error: 404"
# against a URL nobody has in their head, and the answer -- a typo in a version
# box -- is two commands away. It is the likeliest thing to go wrong here, so
# it gets the clearest failure.
if ! curl -fsSL -o "$WORK/SHA256SUMS" "$BASE/SHA256SUMS"; then
  echo "publish.sh: no release v$VERSION in $REPO, or it has no SHA256SUMS." >&2
  echo "  Releases that do exist:" >&2
  curl -fsSL "https://api.github.com/repos/$REPO/releases" 2>/dev/null |
    sed -n 's/.*"tag_name": *"\([^"]*\)".*/    \1/p' >&2 || echo "    (could not list)" >&2
  echo "  Publish one of those, or cut the release first." >&2
  exit 1
fi

# go/npm platform names differ; both are needed, so both are written down.
PLATFORMS=(
  "darwin arm64 darwin arm64"
  "darwin amd64 darwin x64"
  "linux  amd64 linux  x64"
  "linux  arm64 linux  arm64"
  "windows amd64 win32 x64"
)

deps=""
for spec in "${PLATFORMS[@]}"; do
  read -r goos goarch npmos npmcpu <<<"$spec"
  ext=""; [ "$goos" = "windows" ] && ext=".exe"
  asset="llmsh_${VERSION}_${goos}_${goarch}${ext}"
  pkg="$SCOPE/llmsh-$npmos-$npmcpu"
  dir="$WORK/llmsh-$npmos-$npmcpu"

  mkdir -p "$dir/bin"
  curl -fsSL -o "$dir/bin/llmsh$ext" "$BASE/$asset"

  want=$(grep " $asset\$" "$WORK/SHA256SUMS" | awk '{print $1}' || true)
  got=$(sha "$dir/bin/llmsh$ext")
  if [ -z "$want" ]; then
    echo "publish.sh: $asset is not in SHA256SUMS; refusing" >&2; exit 1
  fi
  if [ "$want" != "$got" ]; then
    echo "publish.sh: checksum mismatch for $asset; refusing" >&2
    echo "  expected $want" >&2
    echo "  got      $got" >&2
    exit 1
  fi
  chmod +x "$dir/bin/llmsh$ext"

  cat > "$dir/package.json" <<EOF
{
  "name": "$pkg",
  "version": "$VERSION",
  "description": "llmsh binary for $npmos $npmcpu",
  "repository": { "type": "git", "url": "git+https://github.com/$REPO.git" },
  "license": "MIT",
  "os": ["$npmos"],
  "cpu": ["$npmcpu"],
  "files": ["bin"]
}
EOF
  deps+="    \"$pkg\": \"$VERSION\",
"
  printf '  %-34s %s  ✓\n' "$pkg" "$(du -h "$dir/bin/llmsh$ext" | cut -f1)"
done

# The wrapper. optionalDependencies so npm installs the one platform package
# whose os and cpu match and skips the rest without failing the install.
mkdir -p "$WORK/wrapper/bin"
cp "$HERE/run.js" "$WORK/wrapper/bin/llmsh.js"
cp "$HERE/../README.md" "$WORK/wrapper/README.md"
cp "$HERE/../LICENSE" "$WORK/wrapper/LICENSE"
cat > "$WORK/wrapper/package.json" <<EOF
{
  "name": "$WRAPPER",
  "version": "$VERSION",
  "description": "Publish and install versioned skills for AI agents",
  "repository": { "type": "git", "url": "git+https://github.com/$REPO.git" },
  "homepage": "https://llmskillhub.com",
  "license": "MIT",
  "keywords": ["ai", "agent", "skills", "cli", "llm", "skillhub"],
  "bin": { "llmsh": "bin/llmsh.js" },
  "files": ["bin", "README.md", "LICENSE"],
  "optionalDependencies": {
$(printf '%s' "$deps" | sed '$ s/,$//')
  }
}
EOF

# Platform packages first. The wrapper depends on them by exact version, and a
# wrapper on the registry whose dependencies are not there yet is an install
# that fails for everyone who is quick.
echo
for d in "$WORK"/llmsh-*; do
  ( cd "$d" && npm publish --access public "${NPM_ARGS[@]:-}" )
done
( cd "$WORK/wrapper" && npm publish --access public "${NPM_ARGS[@]:-}" )
echo
echo "published $WRAPPER@$VERSION"
