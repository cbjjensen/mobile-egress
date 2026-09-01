#!/bin/sh
set -eu

fail() {
    printf 'build-macos: %s\n' "$*" >&2
    exit 1
}

RELEASE_VERSION=''
NODE_MANIFEST=''
SOURCE_COMMIT=''
while [ "$#" -gt 0 ]; do
    case "$1" in
        --release-version) [ "$#" -ge 2 ] || fail 'missing release version'; RELEASE_VERSION=$2; shift 2 ;;
        --node-manifest) [ "$#" -ge 2 ] || fail 'missing node manifest'; NODE_MANIFEST=$2; shift 2 ;;
        --source-commit) [ "$#" -ge 2 ] || fail 'missing source commit'; SOURCE_COMMIT=$2; shift 2 ;;
        *) fail "unknown argument: $1" ;;
    esac
done

printf '%s' "$RELEASE_VERSION" | /usr/bin/grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' || fail 'release version must be SemVer without a v prefix'
printf '%s' "$SOURCE_COMMIT" | /usr/bin/grep -Eq '^[0-9a-f]{40}$' || fail 'source commit must be exactly 40 lowercase hex characters'
case "$NODE_MANIFEST" in
    /*) ;;
    *) fail 'node manifest path must be absolute' ;;
esac
[ -f "$NODE_MANIFEST" ] && [ ! -L "$NODE_MANIFEST" ] || fail 'node manifest must be a regular non-symlink file'
[ "$(/usr/bin/wc -c < "$NODE_MANIFEST" | /usr/bin/awk '{print $1}')" -le 65536 ] || fail 'node manifest exceeds 64 KiB'
[ "$(/usr/bin/uname -s)" = 'Darwin' ] || fail 'macOS is required'
[ "$(/usr/bin/uname -m)" = 'arm64' ] || fail 'Apple Silicon is required'

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
REPOSITORY_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd -P)
WINDOWS_CLIENT_ROOT="$REPOSITORY_ROOT/windows-client"
BUILD_ROOT=${MOBILE_EGRESS_MAC_BUILD_ROOT:-"$HOME/Library/Caches/com.cbjjensen.mobile-egress.build"}
[ "$(/usr/bin/git -C "$REPOSITORY_ROOT" rev-parse HEAD)" = "$SOURCE_COMMIT" ] || fail 'Mac checkout does not match the requested source commit'
[ -z "$(/usr/bin/git -C "$REPOSITORY_ROOT" status --porcelain --untracked-files=normal)" ] || fail 'Mac checkout must be clean before a release build'

/bin/sh "$SCRIPT_DIR/bootstrap-macos-toolchain.sh"
GO_BIN="$BUILD_ROOT/toolchains/go/1.26.7/bin/go"
NODE_BIN="$BUILD_ROOT/toolchains/node/24.20.0/bin"
WAILS_BIN="$BUILD_ROOT/toolchains/wails/2.14.0/bin/wails"
export PATH="$NODE_BIN:$BUILD_ROOT/toolchains/wails/2.14.0/bin:$BUILD_ROOT/toolchains/go/1.26.7/bin:/usr/bin:/bin:/usr/sbin:/sbin"
export GOPATH="$BUILD_ROOT/gopath"
export GOMODCACHE="$BUILD_ROOT/gomodcache"
export GOCACHE="$BUILD_ROOT/gocache"
export npm_config_cache="$BUILD_ROOT/npm-cache"
export GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 MACOSX_DEPLOYMENT_TARGET=13.0
export CGO_CFLAGS='-mmacosx-version-min=13.0'
export CGO_CXXFLAGS='-mmacosx-version-min=13.0'
export CGO_LDFLAGS='-mmacosx-version-min=13.0'

/usr/bin/xcode-select -p >/dev/null 2>&1 || fail 'Xcode Command Line Tools are required; install them separately'
BUILD_VERSION=${RELEASE_VERSION%%-*}
DARWIN_BUILD="$WINDOWS_CLIENT_ROOT/build/darwin"
/bin/mkdir -p "$DARWIN_BUILD"
/usr/bin/sed -e "s/@@RELEASE_VERSION@@/$RELEASE_VERSION/g" -e "s/@@BUILD_VERSION@@/$BUILD_VERSION/g" "$WINDOWS_CLIENT_ROOT/macos/Info.plist.tmpl" > "$DARWIN_BUILD/Info.plist"
/usr/bin/plutil -lint "$DARWIN_BUILD/Info.plist" >/dev/null
/bin/cp "$REPOSITORY_ROOT/assets/branding/zfnf-logo.png" "$WINDOWS_CLIENT_ROOT/build/appicon.png"

(
    cd "$WINDOWS_CLIENT_ROOT/frontend"
    "$NODE_BIN/npm" ci
    "$NODE_BIN/npm" run build
)

MANIFEST_BASE64=$(/usr/bin/base64 < "$NODE_MANIFEST" | /usr/bin/tr -d '\n')
CONTROLLER_LDFLAGS="-X mobile-egress/windows-client/internal/desktop.embeddedReleaseManifestBase64=$MANIFEST_BASE64 -X mobile-egress/windows-client/internal/desktop.controllerVersion=$RELEASE_VERSION"
(
    cd "$WINDOWS_CLIENT_ROOT"
    "$WAILS_BIN" build -clean -platform darwin/arm64 -trimpath -s -m -nosyncgomod -compiler "$GO_BIN" -ldflags "$CONTROLLER_LDFLAGS"
)

WAILS_APP="$WINDOWS_CLIENT_ROOT/build/bin/mobile-egress-windows.app"
STAGED_APP="$WINDOWS_CLIENT_ROOT/build/bin/ZFNF Mobile Egress.app"
[ -d "$WAILS_APP" ] || fail "Wails did not produce $WAILS_APP"
[ ! -e "$STAGED_APP" ] || fail "staged app already exists: $STAGED_APP"
/bin/mv "$WAILS_APP" "$STAGED_APP"
/bin/cp "$WINDOWS_CLIENT_ROOT/macos/appicon.icns" "$STAGED_APP/Contents/Resources/iconfile.icns"
/bin/mkdir -p "$STAGED_APP/Contents/Library/LaunchDaemons"
/bin/cp "$WINDOWS_CLIENT_ROOT/macos/com.cbjjensen.mobile-egress.relay.plist" "$STAGED_APP/Contents/Library/LaunchDaemons/com.cbjjensen.mobile-egress.relay.plist"

(
    cd "$REPOSITORY_ROOT"
    "$GO_BIN" build -trimpath -ldflags "-X main.version=$RELEASE_VERSION" -o "$STAGED_APP/Contents/Resources/mobile-egress-relay" ./relay/cmd/relay
)
/bin/chmod 755 "$STAGED_APP/Contents/MacOS/mobile-egress-windows" "$STAGED_APP/Contents/Resources/mobile-egress-relay"
/bin/chmod 644 "$STAGED_APP/Contents/Info.plist" "$STAGED_APP/Contents/Resources/iconfile.icns" "$STAGED_APP/Contents/Library/LaunchDaemons/com.cbjjensen.mobile-egress.relay.plist"

for binary in "$STAGED_APP/Contents/MacOS/mobile-egress-windows" "$STAGED_APP/Contents/Resources/mobile-egress-relay"; do
    [ "$(/usr/bin/lipo -archs "$binary")" = 'arm64' ] || fail "binary is not arm64-only: $binary"
    /usr/bin/xcrun vtool -show-build "$binary" | /usr/bin/grep -Eq 'minos[[:space:]]+13\.0([[:space:]]|$)' || fail "binary minimum macOS is not 13.0: $binary"
done
[ "$("$STAGED_APP/Contents/MacOS/mobile-egress-windows" --version)" = "$RELEASE_VERSION" ] || fail 'controller binary did not report the requested version'
[ "$("$STAGED_APP/Contents/Resources/mobile-egress-relay" --version)" = "$RELEASE_VERSION" ] || fail 'relay binary did not report the requested version'

[ "$(/usr/bin/plutil -extract CFBundleIdentifier raw -o - "$STAGED_APP/Contents/Info.plist")" = 'com.cbjjensen.mobile-egress.controller' ] || fail 'controller bundle ID is incorrect'
[ "$(/usr/bin/plutil -extract LSMinimumSystemVersion raw -o - "$STAGED_APP/Contents/Info.plist")" = '13.0' ] || fail 'minimum macOS is incorrect'
printf '%s\n' "$STAGED_APP"
