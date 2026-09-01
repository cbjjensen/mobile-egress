#!/bin/sh
set -eu

fail() {
    printf 'bootstrap-macos-toolchain: %s\n' "$*" >&2
    exit 1
}

[ "$(/usr/bin/uname -s)" = "Darwin" ] || fail 'macOS is required'
[ "$(/usr/bin/uname -m)" = "arm64" ] || fail 'Apple Silicon is required'

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
REPOSITORY_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd -P)
LOCK_FILE="$REPOSITORY_ROOT/windows-client/macos/toolchain.lock"
BUILD_ROOT=${MOBILE_EGRESS_MAC_BUILD_ROOT:-"$HOME/Library/Caches/com.cbjjensen.mobile-egress.build"}

case "$BUILD_ROOT" in
    /*) ;;
    *) fail 'MOBILE_EGRESS_MAC_BUILD_ROOT must be absolute' ;;
esac
[ "$BUILD_ROOT" != "/" ] || fail 'build root cannot be /'
[ -f "$LOCK_FILE" ] && [ ! -L "$LOCK_FILE" ] || fail 'tracked toolchain lock is missing or is a symlink'

EXPECTED_LOCK='tool|version|kind|url|sha256|bytes
go|1.26.7|darwin-arm64-tar.gz|https://go.dev/dl/go1.26.7.darwin-arm64.tar.gz|020a1e8224811be75163e920bc77e0926a1390a6aeea19bdcf23f74b9d749f6d|64772572
node|24.20.0|darwin-arm64-tar.gz|https://nodejs.org/download/release/v24.20.0/node-v24.20.0-darwin-arm64.tar.gz|40e5607e5ecb3db9192723776da2d75d966260fc74a7a9e731c1bd67dda96bc8|52813331
wails|2.14.0|go-module-zip|https://proxy.golang.org/github.com/wailsapp/wails/v2/@v/v2.14.0.zip|be2413e0c23f65305adc6c9a102c38f79be79361ba6b64c4d5e8ca87cad39b49|6633703'
[ "$(/bin/cat "$LOCK_FILE")" = "$EXPECTED_LOCK" ] || fail 'toolchain lock does not match the reviewed pins'

for tool in /usr/bin/curl /usr/bin/shasum /usr/bin/tar /usr/bin/unzip /usr/bin/awk /usr/bin/wc /usr/bin/mktemp; do
    [ -x "$tool" ] || fail "required system tool is unavailable: $tool"
done

/bin/mkdir -p "$BUILD_ROOT/downloads" "$BUILD_ROOT/toolchains/go" "$BUILD_ROOT/toolchains/node" "$BUILD_ROOT/toolchains/wails" "$BUILD_ROOT/gopath" "$BUILD_ROOT/gomodcache" "$BUILD_ROOT/gocache" "$BUILD_ROOT/work"
/bin/chmod 700 "$BUILD_ROOT" "$BUILD_ROOT/downloads" "$BUILD_ROOT/toolchains" "$BUILD_ROOT/work"

download_verified() {
    name=$1
    url=$2
    sha=$3
    bytes=$4
    archive="$BUILD_ROOT/downloads/$name"
    if [ -f "$archive" ]; then
        actual_bytes=$(/usr/bin/wc -c < "$archive" | /usr/bin/awk '{print $1}')
        actual_sha=$(/usr/bin/shasum -a 256 "$archive" | /usr/bin/awk '{print $1}')
        if [ "$actual_bytes" = "$bytes" ] && [ "$actual_sha" = "$sha" ]; then
            printf '%s\n' "$archive"
            return
        fi
        fail "cached $name does not match its pinned size and SHA-256"
    fi

    partial=$(/usr/bin/mktemp "$BUILD_ROOT/downloads/$name.partial.XXXXXX")
    effective="$partial.url"
    trap '/bin/rm -f "$partial" "$effective"' EXIT HUP INT TERM
    /usr/bin/curl --fail --location --proto '=https' --proto-redir '=https' --tlsv1.2 --silent --show-error --output "$partial" --write-out '%{url_effective}' "$url" > "$effective"
    actual_bytes=$(/usr/bin/wc -c < "$partial" | /usr/bin/awk '{print $1}')
    actual_sha=$(/usr/bin/shasum -a 256 "$partial" | /usr/bin/awk '{print $1}')
    [ "$actual_bytes" = "$bytes" ] || fail "$name size mismatch"
    [ "$actual_sha" = "$sha" ] || fail "$name SHA-256 mismatch"
    final_url=$(/bin/cat "$effective")
    case "$name:$final_url" in
        go1.26.7.darwin-arm64.tar.gz:https://go.dev/dl/go1.26.7.darwin-arm64.tar.gz|go1.26.7.darwin-arm64.tar.gz:https://dl.google.com/go/go1.26.7.darwin-arm64.tar.gz) ;;
        node-v24.20.0-darwin-arm64.tar.gz:https://nodejs.org/download/release/v24.20.0/node-v24.20.0-darwin-arm64.tar.gz) ;;
        wails-v2.14.0.zip:https://proxy.golang.org/github.com/wailsapp/wails/v2/@v/v2.14.0.zip) ;;
        *) fail "$name redirected to an unapproved URL: $final_url" ;;
    esac
    /bin/mv "$partial" "$archive"
    /bin/rm -f "$effective"
    trap - EXIT HUP INT TERM
    printf '%s\n' "$archive"
}

check_member_list() {
    list=$1
    /usr/bin/awk -F/ 'BEGIN { ok=1 } /^\// { ok=0 } { for (i=1; i<=NF; i++) if ($i == "..") ok=0 } END { exit ok ? 0 : 1 }' "$list" || fail 'archive contains an unsafe member path'
}

GO_FINAL="$BUILD_ROOT/toolchains/go/1.26.7"
if [ ! -x "$GO_FINAL/bin/go" ]; then
    archive=$(download_verified 'go1.26.7.darwin-arm64.tar.gz' 'https://go.dev/dl/go1.26.7.darwin-arm64.tar.gz' '020a1e8224811be75163e920bc77e0926a1390a6aeea19bdcf23f74b9d749f6d' '64772572')
    work=$(/usr/bin/mktemp -d "$BUILD_ROOT/work/go.XXXXXX")
    members="$work/members"
    /usr/bin/tar -tzf "$archive" > "$members"
    check_member_list "$members"
    /usr/bin/tar -xzf "$archive" -C "$work"
    [ -x "$work/go/bin/go" ] || fail 'Go archive layout is unexpected'
    [ "$($work/go/bin/go version)" = 'go version go1.26.7 darwin/arm64' ] || fail 'Go binary version is unexpected'
    /bin/mv "$work/go" "$GO_FINAL"
    /bin/rm -rf "$work"
fi
[ "$($GO_FINAL/bin/go version)" = 'go version go1.26.7 darwin/arm64' ] || fail 'installed Go does not match the lock'

NODE_FINAL="$BUILD_ROOT/toolchains/node/24.20.0"
if [ ! -x "$NODE_FINAL/bin/node" ]; then
    archive=$(download_verified 'node-v24.20.0-darwin-arm64.tar.gz' 'https://nodejs.org/download/release/v24.20.0/node-v24.20.0-darwin-arm64.tar.gz' '40e5607e5ecb3db9192723776da2d75d966260fc74a7a9e731c1bd67dda96bc8' '52813331')
    work=$(/usr/bin/mktemp -d "$BUILD_ROOT/work/node.XXXXXX")
    members="$work/members"
    /usr/bin/tar -tzf "$archive" > "$members"
    check_member_list "$members"
    /usr/bin/tar -xzf "$archive" -C "$work"
    source="$work/node-v24.20.0-darwin-arm64"
    [ -x "$source/bin/node" ] || fail 'Node archive layout is unexpected'
    [ "$($source/bin/node --version)" = 'v24.20.0' ] || fail 'Node binary version is unexpected'
    /bin/mv "$source" "$NODE_FINAL"
    /bin/rm -rf "$work"
fi
[ "$($NODE_FINAL/bin/node --version)" = 'v24.20.0' ] || fail 'installed Node does not match the lock'

WAILS_FINAL="$BUILD_ROOT/toolchains/wails/2.14.0"
if [ ! -x "$WAILS_FINAL/bin/wails" ]; then
    archive=$(download_verified 'wails-v2.14.0.zip' 'https://proxy.golang.org/github.com/wailsapp/wails/v2/@v/v2.14.0.zip' 'be2413e0c23f65305adc6c9a102c38f79be79361ba6b64c4d5e8ca87cad39b49' '6633703')
    work=$(/usr/bin/mktemp -d "$BUILD_ROOT/work/wails.XXXXXX")
    members="$work/members"
    /usr/bin/unzip -Z1 "$archive" > "$members"
    check_member_list "$members"
    /usr/bin/unzip -q "$archive" -d "$work"
    source="$work/github.com/wailsapp/wails/v2@v2.14.0"
    [ -f "$source/go.mod" ] || fail 'Wails module archive layout is unexpected'
    /bin/mkdir -p "$work/install/bin"
    (
        cd "$source"
        GOPROXY='https://proxy.golang.org' GOSUMDB='sum.golang.org' GOPRIVATE='' GONOSUMDB='' \
        GOPATH="$BUILD_ROOT/gopath" GOMODCACHE="$BUILD_ROOT/gomodcache" GOCACHE="$BUILD_ROOT/gocache" \
        "$GO_FINAL/bin/go" build -trimpath -o "$work/install/bin/wails" ./cmd/wails
    )
    [ "$($work/install/bin/wails version)" = 'v2.14.0' ] || fail 'built Wails version is unexpected'
    /bin/mv "$work/install" "$WAILS_FINAL"
    /bin/rm -rf "$work"
fi
[ "$($WAILS_FINAL/bin/wails version)" = 'v2.14.0' ] || fail 'installed Wails does not match the lock'

printf 'Pinned macOS toolchain is ready under %s\n' "$BUILD_ROOT"
