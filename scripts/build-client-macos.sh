#!/bin/sh
set -eu
fail() { printf 'build-client-macos: %s\n' "$*" >&2; exit 1; }
RELEASE_VERSION=''
SOURCE_COMMIT=''
STAGE=''
RUNTIME_MODE=service
while [ "$#" -gt 0 ]; do
    case "$1" in
        --release-version) RELEASE_VERSION=${2-}; shift 2;;
        --source-commit) SOURCE_COMMIT=${2-}; shift 2;;
        --stage-dir) STAGE=${2-}; shift 2;;
        --runtime-mode) RUNTIME_MODE=${2-}; shift 2;;
        *) fail "unknown argument: $1";;
    esac
done
printf '%s' "$RELEASE_VERSION" | /usr/bin/grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' || fail 'invalid release version'
BASE_VERSION=${RELEASE_VERSION%%-*}
MAJOR=${BASE_VERSION%%.*}
MINOR_PATCH=${BASE_VERSION#*.}
MINOR=${MINOR_PATCH%%.*}
PATCH=${MINOR_PATCH#*.}
if [ "$MAJOR" -lt 2 ] || { [ "$MAJOR" -eq 2 ] && [ "$MINOR" -eq 0 ] && [ "$PATCH" -lt 2 ]; }; then
    fail 'renamed source requires release version 2.0.2 or later; rebuild only from the original historical source checkout'
fi
case "$RUNTIME_MODE" in service|app) ;; *) fail 'runtime mode must be service or app';; esac
USER_DMG_CONTRACT=0
if [ "$MAJOR" -gt 2 ] || { [ "$MAJOR" -eq 2 ] && { [ "$MINOR" -gt 0 ] || [ "$PATCH" -ge 6 ]; }; }; then USER_DMG_CONTRACT=1; fi
[ "$RUNTIME_MODE" != app ] || [ "$USER_DMG_CONTRACT" = 1 ] || fail 'user DMG requires 2.0.6 or later'
printf '%s' "$SOURCE_COMMIT" | /usr/bin/grep -Eq '^[0-9a-f]{40}$' || fail 'invalid source commit'
[ "$(/usr/bin/uname -s)" = Darwin ] && [ "$(/usr/bin/uname -m)" = arm64 ] || fail 'Apple Silicon macOS is required'
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
REPO=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd -P)
[ "$(/usr/bin/git -C "$REPO" rev-parse HEAD)" = "$SOURCE_COMMIT" ] || fail 'source commit mismatch'
[ -z "$(/usr/bin/git -C "$REPO" status --porcelain --untracked-files=normal)" ] || fail 'release checkout must be clean'
/bin/sh "$SCRIPT_DIR/bootstrap-macos-toolchain.sh" --go-only
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
APP="$STAGE/Applications/Inevitable Mobile Relay.app"
DAEMON="$STAGE/Library/Application Support/MobileEgressClient/bin/mobile-egress-client"
TAGS=production
if [ "$RUNTIME_MODE" = app ]; then APP="$STAGE/Inevitable Mobile Relay.app"; TAGS=production,client_usermode; fi
/bin/mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
if [ "$RUNTIME_MODE" = service ]; then /bin/mkdir -p "$(dirname "$DAEMON")" "$STAGE/Library/LaunchDaemons"; fi
BUILD_VERSION=${RELEASE_VERSION%%-*}
/usr/bin/sed -e "s/@@RELEASE_VERSION@@/$RELEASE_VERSION/g" -e "s/@@BUILD_VERSION@@/$BUILD_VERSION/g" -e "s/@@RUNTIME_MODE@@/$RUNTIME_MODE/g" -e "s/@@SOURCE_COMMIT@@/$SOURCE_COMMIT/g" "$REPO/windows-client/macos/client/Info.plist.tmpl" > "$APP/Contents/Info.plist"
/bin/cp "$REPO/windows-client/macos/appicon.icns" "$APP/Contents/Resources/iconfile.icns"
cd "$REPO"
if [ "$RUNTIME_MODE" = service ]; then
    /bin/cp "$REPO/windows-client/macos/client/com.zfnf.mobile-egress.client.plist" "$STAGE/Library/LaunchDaemons/"
    "$GO_BIN" build -buildvcs=true -trimpath -ldflags "-X main.version=$RELEASE_VERSION" -o "$DAEMON" ./windows-client/cmd/mobile-egress-client
fi
"$GO_BIN" build -buildvcs=true -trimpath -tags "$TAGS" -ldflags "-X main.version=$RELEASE_VERSION" -o "$APP/Contents/MacOS/mobile-egress-client-app" ./windows-client/cmd/mobile-egress-client-app
verify_binary_platform() {
    binary=$1
    /bin/chmod 755 "$binary"
    [ "$(/usr/bin/lipo -archs "$binary")" = arm64 ] || fail 'Client binary is not arm64'
    /usr/bin/xcrun vtool -show-build "$binary" | /usr/bin/grep -Eq 'minos[[:space:]]+13\.0([[:space:]]|$)' || fail 'Client binary deployment target is not macOS 13'
}
verify_binary_platform "$APP/Contents/MacOS/mobile-egress-client-app"
if [ "$RUNTIME_MODE" = service ]; then
    verify_binary_platform "$DAEMON"
    [ "$("$DAEMON" --version)" = "$RELEASE_VERSION" ] || fail 'Client daemon version mismatch'
    /usr/bin/plutil -lint "$STAGE/Library/LaunchDaemons/com.zfnf.mobile-egress.client.plist" >/dev/null
fi
if [ "$USER_DMG_CONTRACT" = 1 ]; then
    GUI="$APP/Contents/MacOS/mobile-egress-client-app"
    [ "$("$GUI" --version)" = "$RELEASE_VERSION" ] || fail 'Client app version mismatch'
    [ "$("$GUI" --runtime-mode)" = "$RUNTIME_MODE" ] || fail 'Client app runtime mode mismatch'
    "$GO_BIN" version -m "$GUI" > "$STAGE/build-info"
    /usr/bin/grep -Eq '^[[:space:]]*path[[:space:]]+mobile-egress/windows-client/cmd/mobile-egress-client-app$' "$STAGE/build-info" || fail 'Client app source command mismatch'
    /usr/bin/grep -Eq "^[[:space:]]*build[[:space:]]+vcs.revision=$SOURCE_COMMIT$" "$STAGE/build-info" || fail 'Client app source revision mismatch'
    /usr/bin/grep -Eq '^[[:space:]]*build[[:space:]]+vcs.modified=false$' "$STAGE/build-info" || fail 'Client app source is dirty'
    if [ "$RUNTIME_MODE" = app ]; then
        /usr/bin/grep -Eq '^[[:space:]]*build[[:space:]]+-tags=([^[:space:]]*,)?client_usermode(,[^[:space:]]*)?$' "$STAGE/build-info" || fail 'Client app user build tag missing'
    fi
    /bin/rm -- "$STAGE/build-info"
fi
/usr/bin/plutil -lint "$APP/Contents/Info.plist" >/dev/null
trap - EXIT HUP INT TERM
printf '%s\n' "$STAGE"
