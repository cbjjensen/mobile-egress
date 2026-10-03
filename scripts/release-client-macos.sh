#!/bin/sh
set -eu
fail() { printf 'release-client-macos: %s\n' "$*" >&2; exit 1; }
RELEASE_VERSION='' SOURCE_COMMIT='' TEAM_ID='' APPLICATION_IDENTITY='' INSTALLER_IDENTITY=''
NOTARY_API_KEY='' NOTARY_API_KEY_ID='' NOTARY_API_ISSUER_ID=''
while [ "$#" -gt 0 ]; do
    case "$1" in
        --release-version) RELEASE_VERSION=${2-}; shift 2;;
        --source-commit) SOURCE_COMMIT=${2-}; shift 2;;
        --team-id) TEAM_ID=${2-}; shift 2;;
        --application-identity) APPLICATION_IDENTITY=${2-}; shift 2;;
        --installer-identity) INSTALLER_IDENTITY=${2-}; shift 2;;
        --notary-api-key) NOTARY_API_KEY=${2-}; shift 2;;
        --notary-api-key-id) NOTARY_API_KEY_ID=${2-}; shift 2;;
        --notary-api-issuer-id) NOTARY_API_ISSUER_ID=${2-}; shift 2;;
        *) fail "unknown argument: $1";;
    esac
done
printf '%s' "$RELEASE_VERSION" | /usr/bin/grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' || fail 'invalid release version'
printf '%s' "$SOURCE_COMMIT" | /usr/bin/grep -Eq '^[0-9a-f]{40}$' || fail 'invalid source commit'
printf '%s' "$TEAM_ID" | /usr/bin/grep -Eq '^[A-Z0-9]{10}$' || fail 'invalid Team ID'
[ -f "$NOTARY_API_KEY" ] && [ ! -L "$NOTARY_API_KEY" ] || fail 'notary API key is unavailable'
case "$APPLICATION_IDENTITY" in "Developer ID Application: "*"($TEAM_ID)") ;; *) fail 'application identity mismatch';; esac
case "$INSTALLER_IDENTITY" in "Developer ID Installer: "*"($TEAM_ID)") ;; *) fail 'installer identity mismatch';; esac
[ "$(/usr/bin/uname -s)" = Darwin ] && [ "$(/usr/bin/uname -m)" = arm64 ] || fail 'Apple Silicon macOS is required'
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
REPO=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd -P)
OUTPUT="$REPO/windows-client/build/release"
NAME="mobile-egress-client-macos-$RELEASE_VERSION-arm64.pkg"
FINAL="$OUTPUT/$NAME"
RECORD="$OUTPUT/mobile-egress-client-macos-$RELEASE_VERSION-arm64.verification.json"
[ ! -e "$FINAL" ] && [ ! -e "$RECORD" ] || fail 'Client release output already exists'
/bin/mkdir -p "$OUTPUT"
WORK=$(/usr/bin/mktemp -d "$OUTPUT/.release-client-macos.XXXXXX")
/bin/chmod 700 "$WORK"
trap '/bin/rm -rf "$WORK"' EXIT HUP INT TERM
STAGE="$WORK/payload"
/bin/sh "$SCRIPT_DIR/build-client-macos.sh" --release-version "$RELEASE_VERSION" --source-commit "$SOURCE_COMMIT" --stage-dir "$STAGE"
APP="$STAGE/Applications/ZFNF Mobile Egress Client.app"
DAEMON="$STAGE/Library/Application Support/MobileEgressClient/bin/mobile-egress-client"
/usr/bin/codesign --force --options runtime --timestamp --identifier com.zfnf.mobile-egress.client --sign "$APPLICATION_IDENTITY" "$DAEMON"
/usr/bin/codesign --force --options runtime --timestamp --sign "$APPLICATION_IDENTITY" "$APP"
for binary in "$DAEMON" "$APP"; do
    /usr/bin/codesign --verify --strict --verbose=2 "$binary"
    /usr/bin/codesign -d --verbose=4 "$binary" 2> "$WORK/signature.txt"
    /usr/bin/grep -Fx "Authority=$APPLICATION_IDENTITY" "$WORK/signature.txt" >/dev/null || fail 'Client signer mismatch'
    /usr/bin/grep -Fx "TeamIdentifier=$TEAM_ID" "$WORK/signature.txt" >/dev/null || fail 'Client Team ID mismatch'
    /usr/bin/grep -F '(runtime)' "$WORK/signature.txt" >/dev/null || fail 'Client lacks hardened runtime'
done
/usr/bin/codesign -d --verbose=4 "$DAEMON" 2> "$WORK/daemon.txt"
/usr/bin/grep -Fx 'Identifier=com.zfnf.mobile-egress.client' "$WORK/daemon.txt" >/dev/null || fail 'daemon signing identity mismatch'
[ "$(/usr/bin/plutil -extract CFBundleIdentifier raw -o - "$APP/Contents/Info.plist")" = com.zfnf.mobile-egress.client.app ] || fail 'Client app bundle mismatch'
# Use a private copy of executable installer scripts; repository file modes may
# originate on Windows. The package payload has no relay/Tailscale/AWS tools.
/usr/bin/ditto "$REPO/windows-client/macos/client/scripts" "$WORK/scripts"
/bin/chmod 755 "$WORK/scripts/preinstall" "$WORK/scripts/postinstall"
/usr/bin/pkgbuild --root "$STAGE" --component-plist "$REPO/windows-client/macos/client/component.plist" --scripts "$WORK/scripts" --install-location / --identifier com.zfnf.mobile-egress.client.pkg --version "${RELEASE_VERSION%%-*}" --ownership recommended "$WORK/unsigned.pkg"
/usr/bin/productsign --timestamp --sign "$INSTALLER_IDENTITY" "$WORK/unsigned.pkg" "$WORK/$NAME"
/usr/sbin/pkgutil --check-signature "$WORK/$NAME" > "$WORK/package.txt"
/usr/bin/grep -F "$INSTALLER_IDENTITY" "$WORK/package.txt" >/dev/null || fail 'Client package signer mismatch'
/usr/bin/xcrun notarytool submit "$WORK/$NAME" --key "$NOTARY_API_KEY" --key-id "$NOTARY_API_KEY_ID" --issuer "$NOTARY_API_ISSUER_ID" --wait --output-format json > "$WORK/notary.json"
[ "$(/usr/bin/plutil -extract status raw -o - "$WORK/notary.json")" = Accepted ] || fail 'Client notarization was not accepted'
/usr/bin/xcrun stapler staple "$WORK/$NAME"
/usr/bin/xcrun stapler validate "$WORK/$NAME"
/usr/sbin/spctl -a -t install -vv "$WORK/$NAME"
/usr/sbin/spctl -a -t exec -vv "$APP"
/usr/bin/codesign --verify --strict "$DAEMON"
HASH=$(/usr/bin/shasum -a 256 "$WORK/$NAME" | /usr/bin/awk '{print $1}')
P="$WORK/record.plist"
/usr/bin/plutil -create xml1 "$P"
/usr/bin/plutil -insert schemaVersion -integer 1 "$P"
/usr/bin/plutil -insert releaseVersion -string "$RELEASE_VERSION" "$P"
/usr/bin/plutil -insert sourceCommit -string "$SOURCE_COMMIT" "$P"
/usr/bin/plutil -insert artifactName -string "$NAME" "$P"
/usr/bin/plutil -insert artifactSha256 -string "$HASH" "$P"
/usr/bin/plutil -insert architecture -string arm64 "$P"
/usr/bin/plutil -insert minimumMacOS -string 13.0 "$P"
/usr/bin/plutil -insert appBundleId -string com.zfnf.mobile-egress.client.app "$P"
/usr/bin/plutil -insert daemonBundleId -string com.zfnf.mobile-egress.client "$P"
/usr/bin/plutil -insert applicationIdentity -string "$APPLICATION_IDENTITY" "$P"
/usr/bin/plutil -insert installerIdentity -string "$INSTALLER_IDENTITY" "$P"
/usr/bin/plutil -insert hardenedRuntime -bool true "$P"
for field in appSignature daemonSignature packageSignature staple; do /usr/bin/plutil -insert "$field" -string valid "$P"; done
/usr/bin/plutil -insert notarization -string accepted "$P"
/usr/bin/plutil -insert checks -dictionary "$P"
for check in codesign pkgutil spctl stapler; do /usr/bin/plutil -insert "checks.$check" -string passed "$P"; done
/usr/bin/plutil -convert json -o "$WORK/record.json" "$P"
BUILD_ROOT=${MOBILE_EGRESS_MAC_BUILD_ROOT:-"$HOME/Library/Caches/com.cbjjensen.mobile-egress.build"}
cd "$REPO"
"$BUILD_ROOT/toolchains/go/1.26.7/bin/go" run ./windows-client/cmd/mobile-egress-macos-release validate-client-record "$WORK/record.json" "$RELEASE_VERSION" "$SOURCE_COMMIT" unused "$HASH" "$APPLICATION_IDENTITY" "$INSTALLER_IDENTITY"
/bin/mv "$WORK/$NAME" "$FINAL"
/bin/mv "$WORK/record.json" "$RECORD"
printf 'Notarized Client PKG: %s\nSHA-256: %s\n' "$FINAL" "$HASH"
