#!/bin/sh

set -eu

arch="${1:?usage: build-macos-release.sh <amd64|arm64>}"
case "$arch" in
  amd64|arm64) ;;
  *) echo "unsupported macOS architecture: $arch" >&2; exit 2 ;;
esac

identity="${V_LOCAL_CLI_CODESIGN_IDENTITY:?V_LOCAL_CLI_CODESIGN_IDENTITY is required}"
root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
output="$root/build/darwin-$arch/v-local-cli"

mkdir -p "$(dirname "$output")"
(
  cd "$root"
  CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" go build -trimpath -ldflags '-s -w' -o "$output" ./cmd/v-local-cli
)
chmod 700 "$output"
codesign --force --options runtime --timestamp --sign "$identity" "$output"
codesign --verify --strict --verbose=2 "$output"
printf '%s\n' "$output"
