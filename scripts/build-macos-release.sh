#!/bin/sh

set -eu

arch="${1:?usage: build-macos-release.sh <amd64|arm64>}"
case "$arch" in
  amd64|arm64) ;;
  *) echo "unsupported macOS architecture: $arch" >&2; exit 2 ;;
esac

identity="${V_LOCAL_CLI_CODESIGN_IDENTITY:?V_LOCAL_CLI_CODESIGN_IDENTITY is required}"
team_id="${V_LOCAL_CLI_RELEASE_TEAM_ID:?V_LOCAL_CLI_RELEASE_TEAM_ID is required}"
case "$team_id" in
  *[!0-9A-Za-z]*|"") echo "invalid Developer ID team identity: $team_id" >&2; exit 2 ;;
esac
root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
output="$root/build/darwin-$arch/v-local-cli"

mkdir -p "$(dirname "$output")"
(
  cd "$root"
  ldflags="-s -w -X github.com/zanescope/v-local-cli/internal/provider.buildMode=release"
  ldflags="$ldflags -X github.com/zanescope/v-local-cli/internal/provider.releaseTeamID=$team_id"
  CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" go build -trimpath -ldflags "$ldflags" -o "$output" ./cmd/v-local-cli
)
chmod 700 "$output"
codesign --force --identifier com.zanescope.v-local-cli --options runtime --timestamp --sign "$identity" "$output"
codesign --verify --strict --verbose=2 "$output"

# 复核签名后的实际 Team ID 与编译期注入的一致。注入的值若与签名者无关，运行时会把
# 合法的 Provider 判为不可信；Go 链接器对写错的 -X 目标是静默忽略的，只有这一步能
# 发现绑定失效。
actual_team_id="$(codesign --display --verbose=4 "$output" 2>&1 | awk -F= '/^TeamIdentifier=/{print $2; exit}')"
if [ "$actual_team_id" != "$team_id" ]; then
  echo "signed CLI Team ID '$actual_team_id' does not match the compiled-in release identity '$team_id'" >&2
  exit 1
fi
printf '%s\n' "$output"
