#!/bin/sh
set -eu
fail() { printf 'build-client-macos: %s\n' "$*" >&2; exit 1; }
RELEASE_VERSION=''
SOURCE_COMMIT=''
STAGE=''
while [ "$#" -gt 0 ]; do
    case "$1" in
        --release-version) RELEASE_VERSION=${2-}; shift 2;;
        --source-commit) SOURCE_COMMIT=${2-}; shift 2;;
        --stage-dir) STAGE=${2-}; shift 2;;
        *) fail "unknown argument: $1";;
    esac
done
printf '%s' "$RELEASE_VERSION" | /usr/bin/grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' || fail 'invalid release version'
printf '%s' "$SOURCE_COMMIT" | /usr/bin/grep -Eq '^[0-9a-f]{40}$' || fail 'invalid source commit'
[ "$(/usr/bin/uname -s)" = Darwin ] && [ "$(/usr/bin/uname -m)" = arm64 ] || fail 'Apple Silicon macOS is required'
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
REPO=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd -P)
[ "$(/usr/bin/git -C "$REPO" rev-parse HEAD)" = "$SOURCE_COMMIT" ] || fail 'source commit mismatch'
[ -z "$(/usr/bin/git -C "$REPO" status --porcelain --untracked-files=normal)" ] || fail 'release checkout must be clean'
/bin/sh "$SCRIPT_DIR/bootstrap-macos-toolchain.sh"
BUILD_ROOT=${MOBILE_EGRESS_MAC_BUILD_ROOT:-"$HOME/Library/Caches/com.cbjjensen.mobile-egress.build"}
GO_BIN="$BUILD_ROOT/toolchains/go/1.26.7/bin/go"
export GOPATH="$BUILD_ROOT/gopath" GOMODCACHE="$BUILD_ROOT/gomodcache" GOCACHE="$BUILD_ROOT/gocache"
export GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 MACOSX_DEPLOYMENT_TARGET=13.0
export CGO_CFLAGS='-mmacosx-version-min=13.0' CGO_CXXFLAGS='-mmacosx-version-min=13.0' CGO_LDFLAGS='-mmacosx-version-min=13.0'
BUILD_DIRECTORY="$REPO/windows-client/build"
/bin/mkdir -p "$BUILD_DIRECTORY"
BUILD_DIRECTORY=$(CDPATH= cd -- "$BUILD_DIRECTORY" && pwd -P)
if [ -z "$STAGE" ]; then
    # Standalone builds retain their successful output at a unique path.
    STAGE=$(/usr/bin/mktemp -d "$BUILD_DIRECTORY/client-macos.XXXXXX")
else
    # Release builds hand in a child of their private per-run work directory.
    # Require an existing canonical parent inside build and create exclusively;
    # cleanup must never remove a path the caller already owned.
    case "$STAGE" in /*) ;; *) fail 'Client stage path must be absolute';; esac
    STAGE_PARENT=$(CDPATH= cd -- "$(dirname -- "$STAGE")" && pwd -P)
    STAGE="$STAGE_PARENT/$(basename -- "$STAGE")"
    case "$STAGE" in "$BUILD_DIRECTORY"/*) ;; *) fail 'Client stage must stay inside the build directory';; esac
    /bin/mkdir -m 700 "$STAGE" || fail 'Client staging already exists or cannot be created'
fi
cleanup_stage() { /bin/rm -rf -- "$STAGE"; }
trap cleanup_stage EXIT HUP INT TERM
APP="$STAGE/Applications/ZFNF Mobile Egress Client.app"
DAEMON="$STAGE/Library/Application Support/MobileEgressClient/bin/mobile-egress-client"
/bin/mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources" "$(dirname "$DAEMON")" "$STAGE/Library/LaunchDaemons"
BUILD_VERSION=${RELEASE_VERSION%%-*}
/usr/bin/sed -e "s/@@RELEASE_VERSION@@/$RELEASE_VERSION/g" -e "s/@@BUILD_VERSION@@/$BUILD_VERSION/g" "$REPO/windows-client/macos/client/Info.plist.tmpl" > "$APP/Contents/Info.plist"
/bin/cp "$REPO/windows-client/macos/appicon.icns" "$APP/Contents/Resources/iconfile.icns"
/bin/cp "$REPO/windows-client/macos/client/com.zfnf.mobile-egress.client.plist" "$STAGE/Library/LaunchDaemons/"
cd "$REPO"
"$GO_BIN" build -trimpath -ldflags "-X main.version=$RELEASE_VERSION" -o "$DAEMON" ./windows-client/cmd/mobile-egress-client
"$GO_BIN" build -trimpath -tags production -ldflags "-X main.version=$RELEASE_VERSION" -o "$APP/Contents/MacOS/mobile-egress-client-app" ./windows-client/cmd/mobile-egress-client-app
for binary in "$DAEMON" "$APP/Contents/MacOS/mobile-egress-client-app"; do
    /bin/chmod 755 "$binary"
    [ "$(/usr/bin/lipo -archs "$binary")" = arm64 ] || fail 'Client binary is not arm64'
    /usr/bin/xcrun vtool -show-build "$binary" | /usr/bin/grep -Eq 'minos[[:space:]]+13\.0([[:space:]]|$)' || fail 'Client binary deployment target is not macOS 13'
done
[ "$("$DAEMON" --version)" = "$RELEASE_VERSION" ] || fail 'Client daemon version mismatch'
/usr/bin/plutil -lint "$APP/Contents/Info.plist" "$STAGE/Library/LaunchDaemons/com.zfnf.mobile-egress.client.plist" >/dev/null
trap - EXIT HUP INT TERM
printf '%s\n' "$STAGE"
