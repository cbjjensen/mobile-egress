#!/bin/sh
set -eu
fail() { printf 'release-client-dmg: %s\n' "$*" >&2; exit 1; }
RELEASE_VERSION='' SOURCE_COMMIT='' TEAM_ID='' APPLICATION_IDENTITY=''
NOTARY_API_KEY='' NOTARY_API_KEY_ID='' NOTARY_API_ISSUER_ID=''
while [ "$#" -gt 0 ]; do
    case "$1" in
        --release-version) RELEASE_VERSION=${2-}; shift 2;;
        --source-commit) SOURCE_COMMIT=${2-}; shift 2;;
        --team-id) TEAM_ID=${2-}; shift 2;;
        --application-identity) APPLICATION_IDENTITY=${2-}; shift 2;;
        --notary-api-key) NOTARY_API_KEY=${2-}; shift 2;;
        --notary-api-key-id) NOTARY_API_KEY_ID=${2-}; shift 2;;
        --notary-api-issuer-id) NOTARY_API_ISSUER_ID=${2-}; shift 2;;
        *) fail "unknown argument: $1";;
    esac
done
printf '%s' "$RELEASE_VERSION" | /usr/bin/grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' || fail 'invalid release version'
BASE_VERSION=${RELEASE_VERSION%%-*}; MAJOR=${BASE_VERSION%%.*}; MINOR_PATCH=${BASE_VERSION#*.}; MINOR=${MINOR_PATCH%%.*}; PATCH=${MINOR_PATCH#*.}
if [ "$MAJOR" -lt 2 ] || { [ "$MAJOR" -eq 2 ] && [ "$MINOR" -eq 0 ] && [ "$PATCH" -lt 6 ]; }; then fail 'user DMG requires 2.0.6 or later'; fi
printf '%s' "$SOURCE_COMMIT" | /usr/bin/grep -Eq '^[0-9a-f]{40}$' || fail 'invalid source commit'
printf '%s' "$TEAM_ID" | /usr/bin/grep -Eq '^[A-Z0-9]{10}$' || fail 'invalid Team ID'
printf '%s' "$NOTARY_API_KEY_ID" | /usr/bin/grep -Eq '^[A-Z0-9]{10,}$' || fail 'invalid notary key ID'
printf '%s' "$NOTARY_API_ISSUER_ID" | /usr/bin/grep -Eq '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$' || fail 'invalid notary issuer ID'
[ -f "$NOTARY_API_KEY" ] && [ ! -L "$NOTARY_API_KEY" ] || fail 'notary API key is unavailable'
case "$APPLICATION_IDENTITY" in "Developer ID Application: "*"($TEAM_ID)") ;; *) fail 'application identity mismatch';; esac
[ "$(/usr/bin/uname -s)" = Darwin ] && [ "$(/usr/bin/uname -m)" = arm64 ] || fail 'Apple Silicon macOS is required'
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
REPO=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd -P)
OUTPUT="$REPO/windows-client/build/release"
NAME="inevitable-mobile-relay-macos-$RELEASE_VERSION-arm64.dmg"
FINAL="$OUTPUT/$NAME"; RECORD="$FINAL.verification.json"
[ ! -e "$FINAL" ] && [ ! -L "$FINAL" ] && [ ! -e "$RECORD" ] && [ ! -L "$RECORD" ] || fail 'Client DMG release output already exists'
/bin/mkdir -p "$OUTPUT"
WORK=$(/usr/bin/mktemp -d "$OUTPUT/.release-client-dmg.XXXXXX")
/bin/chmod 700 "$WORK"
MOUNTED=0 IMAGE_PROMOTED=0 RECORD_PROMOTED=0 COMMITTED=0
MOUNT="$WORK/mounted"
cleanup_release() {
    if [ "$MOUNTED" = 1 ]; then /usr/bin/hdiutil detach "$MOUNT" >/dev/null 2>&1 || true; fi
    if [ "$COMMITTED" = 0 ]; then
        if [ "$RECORD_PROMOTED" = 1 ]; then /bin/rm -f -- "$RECORD"; fi
        if [ "$IMAGE_PROMOTED" = 1 ]; then /bin/rm -f -- "$FINAL"; fi
    fi
    /bin/rm -rf -- "$WORK"
}
trap cleanup_release EXIT HUP INT TERM
STAGE="$WORK/payload"
/bin/sh "$SCRIPT_DIR/build-client-macos.sh" --release-version "$RELEASE_VERSION" --source-commit "$SOURCE_COMMIT" --stage-dir "$STAGE" --runtime-mode app
APP="$STAGE/Inevitable Mobile Relay.app"
BUILD_ROOT=${MOBILE_EGRESS_MAC_BUILD_ROOT:-"$HOME/Library/Caches/com.cbjjensen.mobile-egress.build"}
GO_BIN="$BUILD_ROOT/toolchains/go/1.26.7/bin/go"
hash_file() {
    hash_output=$(/usr/bin/shasum -a 256 "$1") || fail 'SHA-256 calculation failed'
    hash_value=$(printf '%s\n' "$hash_output" | /usr/bin/awk '{print $1}')
    printf '%s' "$hash_value" | /usr/bin/grep -Eq '^[0-9a-f]{64}$' || fail 'invalid SHA-256 calculation output'
    printf '%s\n' "$hash_value"
}
check_app() {
    checked_app=$1
    info="$checked_app/Contents/Info.plist"
    gui="$checked_app/Contents/MacOS/mobile-egress-client-app"
    [ ! -L "$checked_app" ] && [ -f "$gui" ] && [ ! -L "$gui" ] || fail 'invalid app executable payload'
    [ "$(/usr/bin/plutil -extract CFBundleIdentifier raw -o - "$info")" = com.zfnf.mobile-egress.client.app ] || fail 'app bundle identity mismatch'
    [ "$(/usr/bin/plutil -extract CFBundleExecutable raw -o - "$info")" = mobile-egress-client-app ] || fail 'app executable identity mismatch'
    [ "$(/usr/bin/plutil -extract CFBundleShortVersionString raw -o - "$info")" = "$RELEASE_VERSION" ] || fail 'app version mismatch'
    [ "$(/usr/bin/plutil -extract LSMinimumSystemVersion raw -o - "$info")" = 13.0 ] || fail 'app minimum macOS mismatch'
    [ "$(/usr/bin/plutil -extract MobileEgressRuntimeMode raw -o - "$info")" = app ] || fail 'signed app runtime mode mismatch'
    [ "$(/usr/bin/plutil -extract MobileEgressSourceCommit raw -o - "$info")" = "$SOURCE_COMMIT" ] || fail 'signed app source mismatch'
    [ "$("$gui" --runtime-mode)" = app ] || fail 'actual app runtime mode mismatch'
    [ "$("$gui" --version)" = "$RELEASE_VERSION" ] || fail 'actual app version mismatch'
    [ "$(/usr/bin/lipo -archs "$gui")" = arm64 ] || fail 'app executable is not arm64'
    /usr/bin/xcrun vtool -show-build "$gui" | /usr/bin/grep -Eq 'minos[[:space:]]+13\.0([[:space:]]|$)' || fail 'app executable deployment target mismatch'
    "$GO_BIN" version -m "$gui" > "$WORK/binary-source.txt"
    /usr/bin/grep -Eq '^[[:space:]]*path[[:space:]]+mobile-egress/windows-client/cmd/mobile-egress-client-app$' "$WORK/binary-source.txt" || fail 'app binary source command mismatch'
    /usr/bin/grep -Eq "^[[:space:]]*build[[:space:]]+vcs.revision=$SOURCE_COMMIT$" "$WORK/binary-source.txt" || fail 'app binary source revision mismatch'
    /usr/bin/grep -Eq '^[[:space:]]*build[[:space:]]+vcs.modified=false$' "$WORK/binary-source.txt" || fail 'app binary source is dirty'
    /usr/bin/grep -Eq '^[[:space:]]*build[[:space:]]+-tags=([^[:space:]]*,)?client_usermode(,[^[:space:]]*)?$' "$WORK/binary-source.txt" || fail 'app binary user build tag missing'
    /usr/bin/codesign --verify --strict --verbose=2 "$checked_app"
    /usr/bin/codesign -d --verbose=4 "$checked_app" 2> "$WORK/app-signature.txt"
    /usr/bin/grep -Fx "Authority=$APPLICATION_IDENTITY" "$WORK/app-signature.txt" >/dev/null || fail 'app signer mismatch'
    /usr/bin/grep -Fx "TeamIdentifier=$TEAM_ID" "$WORK/app-signature.txt" >/dev/null || fail 'app Team ID mismatch'
    /usr/bin/grep -Fx 'Identifier=com.zfnf.mobile-egress.client.app' "$WORK/app-signature.txt" >/dev/null || fail 'app signing identifier mismatch'
    /usr/bin/grep -F '(runtime)' "$WORK/app-signature.txt" >/dev/null || fail 'app lacks hardened runtime'
}
/usr/bin/codesign --force --options runtime --timestamp --sign "$APPLICATION_IDENTITY" "$APP"
check_app "$APP"
# Notarize and staple the app before creating the image so offline app launches
# use its own ticket. The image then receives an independent notary ticket.
/usr/bin/ditto -c -k --keepParent "$APP" "$WORK/app.zip"
/usr/bin/xcrun notarytool submit "$WORK/app.zip" --key "$NOTARY_API_KEY" --key-id "$NOTARY_API_KEY_ID" --issuer "$NOTARY_API_ISSUER_ID" --wait --output-format json > "$WORK/app-notary.json"
[ "$(/usr/bin/plutil -extract status raw -o - "$WORK/app-notary.json")" = Accepted ] || fail 'app notarization was not accepted'
/usr/bin/xcrun stapler staple "$APP"
/usr/bin/xcrun stapler validate "$APP"
/usr/sbin/spctl -a -t exec -vv "$APP"
check_app "$APP"
BINARY_HASH=$(hash_file "$APP/Contents/MacOS/mobile-egress-client-app")
/bin/cp "$REPO/windows-client/macos/client/DMG-ReadMe.txt" "$STAGE/Read Me.txt"
/usr/bin/hdiutil create -volname 'Inevitable Mobile Relay' -srcfolder "$STAGE" -format UDZO "$WORK/$NAME"
/usr/bin/codesign --force --timestamp --identifier com.zfnf.mobile-egress.client.app.dmg --sign "$APPLICATION_IDENTITY" "$WORK/$NAME"
/usr/bin/codesign --verify --strict "$WORK/$NAME"
/usr/bin/codesign -d --verbose=4 "$WORK/$NAME" 2> "$WORK/image-signature.txt"
for expectation in "Authority=$APPLICATION_IDENTITY" "TeamIdentifier=$TEAM_ID" 'Identifier=com.zfnf.mobile-egress.client.app.dmg'; do
    /usr/bin/grep -Fx "$expectation" "$WORK/image-signature.txt" >/dev/null || fail 'image signing identity mismatch'
done
/usr/bin/xcrun notarytool submit "$WORK/$NAME" --key "$NOTARY_API_KEY" --key-id "$NOTARY_API_KEY_ID" --issuer "$NOTARY_API_ISSUER_ID" --wait --output-format json > "$WORK/image-notary.json"
[ "$(/usr/bin/plutil -extract status raw -o - "$WORK/image-notary.json")" = Accepted ] || fail 'image notarization was not accepted'
/usr/bin/xcrun stapler staple "$WORK/$NAME"
/usr/bin/xcrun stapler validate "$WORK/$NAME"
/usr/sbin/spctl -a -t open --context context:primary-signature -vv "$WORK/$NAME"
/usr/bin/codesign --verify --strict "$WORK/$NAME"
/bin/mkdir -m 700 "$MOUNT"
MOUNTED=1
/usr/bin/hdiutil attach "$WORK/$NAME" -readonly -nobrowse -mountpoint "$MOUNT"
[ "$(/usr/bin/find "$MOUNT" -mindepth 1 -maxdepth 1 | /usr/bin/wc -l | /usr/bin/tr -d ' ')" = 2 ] || fail 'unexpected mounted DMG payload'
[ -f "$MOUNT/Read Me.txt" ] && [ ! -L "$MOUNT/Read Me.txt" ] || fail 'mounted instructions missing'
/usr/bin/cmp "$STAGE/Read Me.txt" "$MOUNT/Read Me.txt" || fail 'mounted instructions mismatch'
check_app "$MOUNT/Inevitable Mobile Relay.app"
MOUNTED_HASH=$(hash_file "$MOUNT/Inevitable Mobile Relay.app/Contents/MacOS/mobile-egress-client-app")
[ "$MOUNTED_HASH" = "$BINARY_HASH" ] || fail 'mounted app executable hash mismatch'
/usr/bin/xcrun stapler validate "$MOUNT/Inevitable Mobile Relay.app"
/usr/sbin/spctl -a -t exec -vv "$MOUNT/Inevitable Mobile Relay.app"
/usr/bin/hdiutil detach "$MOUNT"
MOUNTED=0
HASH=$(hash_file "$WORK/$NAME")
P="$WORK/record.plist"
/usr/bin/plutil -create xml1 "$P"
/usr/bin/plutil -insert schemaVersion -integer 1 "$P"
for pair in "releaseVersion:$RELEASE_VERSION" "sourceCommit:$SOURCE_COMMIT" "artifactName:$NAME" "artifactSha256:$HASH" 'architecture:arm64' 'minimumMacOS:13.0' 'appBundleId:com.zfnf.mobile-egress.client.app' 'appExecutable:mobile-egress-client-app' 'runtimeMode:app' 'binaryRuntimeMode:app' "binaryVersion:$RELEASE_VERSION" "binarySourceCommit:$SOURCE_COMMIT" "binarySha256:$BINARY_HASH" "mountedBinarySha256:$MOUNTED_HASH"; do
    /usr/bin/plutil -insert "${pair%%:*}" -string "${pair#*:}" "$P"
done
/usr/bin/plutil -insert applicationIdentity -string "$APPLICATION_IDENTITY" "$P"
/usr/bin/plutil -insert hardenedRuntime -bool true "$P"
for field in appSignature imageSignature appStaple imageStaple; do /usr/bin/plutil -insert "$field" -string valid "$P"; done
for field in appNotarization imageNotarization; do /usr/bin/plutil -insert "$field" -string accepted "$P"; done
/usr/bin/plutil -insert checks -dictionary "$P"
for check in codesign spctlApp spctlImage staplerApp staplerImage mountedPayload binarySource; do /usr/bin/plutil -insert "checks.$check" -string passed "$P"; done
/usr/bin/plutil -convert json -o "$WORK/record.json" "$P"
cd "$REPO"
"$GO_BIN" run ./windows-client/cmd/mobile-egress-macos-release validate-client-dmg-record "$WORK/record.json" "$RELEASE_VERSION" "$SOURCE_COMMIT" "$HASH" "$APPLICATION_IDENTITY"
# Exclusive hard links on this same filesystem prevent overwriting a concurrent
# writer. Failed promotion removes only this invocation's own published links.
/bin/ln "$WORK/$NAME" "$FINAL"
IMAGE_PROMOTED=1
/bin/ln "$WORK/record.json" "$RECORD"
RECORD_PROMOTED=1
COMMITTED=1
printf 'Notarized user Client DMG: %s\nSHA-256: %s\nPrivate verification: %s\n' "$FINAL" "$HASH" "$RECORD"
