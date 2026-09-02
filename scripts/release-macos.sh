#!/bin/sh
set -eu

fail() {
    printf 'release-macos: %s\n' "$*" >&2
    exit 1
}

RELEASE_VERSION=''
NODE_MANIFEST=''
SOURCE_COMMIT=''
PROFILE=''
TEAM_ID=''
APPLICATION_IDENTITY=''
INSTALLER_IDENTITY=''
NOTARY_PROFILE=''
NOTARY_API_KEY=''
NOTARY_API_KEY_ID=''
NOTARY_API_ISSUER_ID=''
while [ "$#" -gt 0 ]; do
    case "$1" in
        --release-version) RELEASE_VERSION=${2-}; shift 2 ;;
        --node-manifest) NODE_MANIFEST=${2-}; shift 2 ;;
        --source-commit) SOURCE_COMMIT=${2-}; shift 2 ;;
        --profile) PROFILE=${2-}; shift 2 ;;
        --team-id) TEAM_ID=${2-}; shift 2 ;;
        --application-identity) APPLICATION_IDENTITY=${2-}; shift 2 ;;
        --installer-identity) INSTALLER_IDENTITY=${2-}; shift 2 ;;
        --notary-keychain-profile) NOTARY_PROFILE=${2-}; shift 2 ;;
        --notary-api-key) NOTARY_API_KEY=${2-}; shift 2 ;;
        --notary-api-key-id) NOTARY_API_KEY_ID=${2-}; shift 2 ;;
        --notary-api-issuer-id) NOTARY_API_ISSUER_ID=${2-}; shift 2 ;;
        *) fail "unknown or incomplete argument: $1" ;;
    esac
done

printf '%s' "$RELEASE_VERSION" | /usr/bin/grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' || fail 'release version is required'
printf '%s' "$SOURCE_COMMIT" | /usr/bin/grep -Eq '^[0-9a-f]{40}$' || fail 'source commit must be exactly 40 lowercase hex characters'
printf '%s' "$TEAM_ID" | /usr/bin/grep -Eq '^[A-Z0-9]{10}$' || fail 'ten-character Apple Team ID is required'
[ -n "$APPLICATION_IDENTITY" ] || fail 'Developer ID Application identity is required'
[ -n "$INSTALLER_IDENTITY" ] || fail 'Developer ID Installer identity is required'
[ -n "$NOTARY_PROFILE" ] || fail 'notarytool Keychain profile name is required'
[ -n "$NOTARY_API_KEY" ] || fail 'notary API key path is required'
printf '%s' "$NOTARY_API_KEY_ID" | /usr/bin/grep -Eq '^[A-Z0-9]{10,}$' || fail 'notary API key ID is required'
printf '%s' "$NOTARY_API_ISSUER_ID" | /usr/bin/grep -Eiq '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' || fail 'notary API issuer ID must be a UUID'
[ -f "$PROFILE" ] && [ ! -L "$PROFILE" ] || fail 'Developer ID provisioning profile must be a regular non-symlink file'
[ -f "$NOTARY_API_KEY" ] && [ ! -L "$NOTARY_API_KEY" ] || fail 'notary API key must be a regular non-symlink file'
[ "$(/usr/bin/uname -s)" = 'Darwin' ] || fail 'macOS is required'
[ "$(/usr/bin/uname -m)" = 'arm64' ] || fail 'Apple Silicon is required'

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
REPOSITORY_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd -P)
WINDOWS_CLIENT_ROOT="$REPOSITORY_ROOT/windows-client"
BUILD_ROOT=${MOBILE_EGRESS_MAC_BUILD_ROOT:-"$HOME/Library/Caches/com.cbjjensen.mobile-egress.build"}
GO_BIN="$BUILD_ROOT/toolchains/go/1.26.7/bin/go"
OUTPUT_DIR="$WINDOWS_CLIENT_ROOT/build/release"
ARTIFACT_NAME="mobile-egress-macos-$RELEASE_VERSION-arm64.pkg"
FINAL_PKG="$OUTPUT_DIR/$ARTIFACT_NAME"
FINAL_RECORD="$OUTPUT_DIR/mobile-egress-macos-$RELEASE_VERSION-arm64.verification.json"
[ ! -e "$FINAL_PKG" ] && [ ! -e "$FINAL_RECORD" ] || fail 'release output already exists'
/bin/mkdir -p "$OUTPUT_DIR"
WORK=$(/usr/bin/mktemp -d "$OUTPUT_DIR/.release-macos.XXXXXX")
/bin/chmod 700 "$WORK"
PKG="$WORK/$ARTIFACT_NAME"
UNSIGNED_PKG="$WORK/mobile-egress-macos-$RELEASE_VERSION-arm64.unsigned.pkg"
cleanup() {
    /bin/rm -rf "$WORK"
    if [ ! -e "$FINAL_RECORD" ]; then
        /bin/rm -f "$FINAL_PKG"
    fi
}
trap cleanup EXIT HUP INT TERM

/usr/bin/security find-identity -v -p codesigning | /usr/bin/grep -F -- "\"$APPLICATION_IDENTITY\"" >/dev/null || fail 'configured Developer ID Application identity is unavailable'
/usr/bin/security find-identity -v -p basic | /usr/bin/grep -F -- "\"$INSTALLER_IDENTITY\"" >/dev/null || fail 'configured Developer ID Installer identity is unavailable'
printf '%s' "$APPLICATION_IDENTITY" | /usr/bin/grep -F "($TEAM_ID)" >/dev/null || fail 'application identity does not carry the configured Team ID'
printf '%s' "$INSTALLER_IDENTITY" | /usr/bin/grep -F "($TEAM_ID)" >/dev/null || fail 'installer identity does not carry the configured Team ID'

/usr/bin/security cms -D -i "$PROFILE" > "$WORK/profile.plist"
/usr/bin/plutil -lint "$WORK/profile.plist" >/dev/null
[ "$(/usr/bin/plutil -extract TeamIdentifier.0 raw -o - "$WORK/profile.plist")" = "$TEAM_ID" ] || fail 'profile Team ID mismatch'
[ "$(/usr/bin/plutil -extract ProvisionsAllDevices raw -o - "$WORK/profile.plist")" = 'true' ] || fail 'profile is not a Developer ID distribution profile'

NODE_MANIFEST_SHA=$(/usr/bin/shasum -a 256 "$NODE_MANIFEST" | /usr/bin/awk '{print $1}')
/bin/sh "$SCRIPT_DIR/build-macos.sh" --release-version "$RELEASE_VERSION" --node-manifest "$NODE_MANIFEST" --source-commit "$SOURCE_COMMIT"
APP="$WINDOWS_CLIENT_ROOT/build/bin/ZFNF Mobile Egress.app"
[ -d "$APP" ] || fail 'staged app is missing'
/bin/cp "$PROFILE" "$APP/Contents/embedded.provisionprofile"
/usr/bin/sed "s/@@TEAM_ID@@/$TEAM_ID/g" "$WINDOWS_CLIENT_ROOT/macos/controller.entitlements.plist.tmpl" > "$WORK/controller.entitlements.plist"
/usr/bin/plutil -lint "$WORK/controller.entitlements.plist" >/dev/null

RELAY="$APP/Contents/Resources/mobile-egress-relay"
/usr/bin/codesign --force --options runtime --timestamp --identifier com.cbjjensen.mobile-egress.relay --sign "$APPLICATION_IDENTITY" "$RELAY"
/usr/bin/codesign --verify --strict --verbose=2 "$RELAY"
/usr/bin/codesign --force --options runtime --timestamp --entitlements "$WORK/controller.entitlements.plist" --sign "$APPLICATION_IDENTITY" "$APP"
/usr/bin/codesign --verify --deep --strict --verbose=2 "$APP"

for required in \
    'Contents/Info.plist' \
    'Contents/embedded.provisionprofile' \
    'Contents/MacOS/mobile-egress-windows' \
    'Contents/Resources/iconfile.icns' \
    'Contents/Resources/mobile-egress-relay' \
    'Contents/Library/LaunchDaemons/com.cbjjensen.mobile-egress.relay.plist'; do
    [ -f "$APP/$required" ] && [ ! -L "$APP/$required" ] || fail "required bundle file is missing or unsafe: $required"
done

/usr/bin/codesign -d --verbose=4 "$APP" 2> "$WORK/app-codesign.txt"
/usr/bin/codesign -d --verbose=4 "$RELAY" 2> "$WORK/relay-codesign.txt"
/usr/bin/grep -F 'Identifier=com.cbjjensen.mobile-egress.controller' "$WORK/app-codesign.txt" >/dev/null || fail 'signed app bundle ID mismatch'
/usr/bin/grep -F 'Identifier=com.cbjjensen.mobile-egress.relay' "$WORK/relay-codesign.txt" >/dev/null || fail 'signed relay identifier mismatch'
/usr/bin/grep -F "Authority=$APPLICATION_IDENTITY" "$WORK/app-codesign.txt" >/dev/null || fail 'signed app authority mismatch'
/usr/bin/grep -F "Authority=$APPLICATION_IDENTITY" "$WORK/relay-codesign.txt" >/dev/null || fail 'signed relay authority mismatch'
/usr/bin/grep -F "TeamIdentifier=$TEAM_ID" "$WORK/app-codesign.txt" >/dev/null || fail 'signed app Team ID mismatch'
/usr/bin/grep -F "TeamIdentifier=$TEAM_ID" "$WORK/relay-codesign.txt" >/dev/null || fail 'signed relay Team ID mismatch'
/usr/bin/grep -F '(runtime)' "$WORK/app-codesign.txt" >/dev/null || fail 'app signature lacks hardened runtime'
/usr/bin/grep -F '(runtime)' "$WORK/relay-codesign.txt" >/dev/null || fail 'relay signature lacks hardened runtime'
/usr/bin/codesign -d --entitlements :- "$APP" > "$WORK/observed-entitlements.plist" 2>/dev/null
if /usr/bin/plutil -extract com.apple.security.app-sandbox raw -o - "$WORK/observed-entitlements.plist" >/dev/null 2>&1; then
    fail 'App Sandbox entitlement must be absent'
fi

BUILD_VERSION=${RELEASE_VERSION%%-*}
/usr/bin/pkgbuild --component "$APP" --install-location /Applications --identifier com.cbjjensen.mobile-egress.controller --version "$BUILD_VERSION" --ownership recommended "$UNSIGNED_PKG"
/usr/bin/productsign --timestamp --sign "$INSTALLER_IDENTITY" "$UNSIGNED_PKG" "$PKG"
/bin/rm -f "$UNSIGNED_PKG"
/usr/sbin/pkgutil --check-signature "$PKG" > "$WORK/pkg-signature.txt"
/usr/bin/grep -F "$INSTALLER_IDENTITY" "$WORK/pkg-signature.txt" >/dev/null || fail 'PKG signer identity mismatch'
/usr/bin/grep -F "($TEAM_ID)" "$WORK/pkg-signature.txt" >/dev/null || fail 'PKG signer Team ID mismatch'

/usr/bin/xcrun notarytool submit "$PKG" --key "$NOTARY_API_KEY" --key-id "$NOTARY_API_KEY_ID" --issuer "$NOTARY_API_ISSUER_ID" --wait --output-format json > "$WORK/notary.json"
[ "$(/usr/bin/plutil -extract status raw -o - "$WORK/notary.json")" = 'Accepted' ] || fail 'Apple notarization was not accepted'
/usr/bin/xcrun stapler staple "$PKG"
/usr/bin/xcrun stapler validate "$PKG"

/usr/bin/codesign --verify --deep --strict --verbose=2 "$APP"
/usr/sbin/spctl -a -t exec -vv "$APP"
/usr/sbin/pkgutil --check-signature "$PKG" >/dev/null
/usr/sbin/spctl -a -t install -vv "$PKG"
/usr/bin/xcrun stapler validate "$PKG"
ARTIFACT_SHA=$(/usr/bin/shasum -a 256 "$PKG" | /usr/bin/awk '{print $1}')

RECORD_PLIST="$WORK/verification.plist"
RECORD_TEMP="$WORK/verification.json"
/usr/bin/plutil -create xml1 "$RECORD_PLIST"
/usr/bin/plutil -insert schemaVersion -integer 1 "$RECORD_PLIST"
/usr/bin/plutil -insert releaseVersion -string "$RELEASE_VERSION" "$RECORD_PLIST"
/usr/bin/plutil -insert sourceCommit -string "$SOURCE_COMMIT" "$RECORD_PLIST"
/usr/bin/plutil -insert nodeManifestSha256 -string "$NODE_MANIFEST_SHA" "$RECORD_PLIST"
/usr/bin/plutil -insert artifactName -string "$ARTIFACT_NAME" "$RECORD_PLIST"
/usr/bin/plutil -insert artifactSha256 -string "$ARTIFACT_SHA" "$RECORD_PLIST"
/usr/bin/plutil -insert architecture -string arm64 "$RECORD_PLIST"
/usr/bin/plutil -insert minimumMacOS -string 13.0 "$RECORD_PLIST"
/usr/bin/plutil -insert controllerBundleId -string com.cbjjensen.mobile-egress.controller "$RECORD_PLIST"
/usr/bin/plutil -insert relayBundleId -string com.cbjjensen.mobile-egress.relay "$RECORD_PLIST"
/usr/bin/plutil -insert applicationIdentity -string "$APPLICATION_IDENTITY" "$RECORD_PLIST"
/usr/bin/plutil -insert installerIdentity -string "$INSTALLER_IDENTITY" "$RECORD_PLIST"
/usr/bin/plutil -insert hardenedRuntime -bool true "$RECORD_PLIST"
/usr/bin/plutil -insert appSandbox -bool false "$RECORD_PLIST"
/usr/bin/plutil -insert bundleLayout -array "$RECORD_PLIST"
index=0
for required in \
    'Contents/Info.plist' \
    'Contents/embedded.provisionprofile' \
    'Contents/MacOS/mobile-egress-windows' \
    'Contents/Resources/iconfile.icns' \
    'Contents/Resources/mobile-egress-relay' \
    'Contents/Library/LaunchDaemons/com.cbjjensen.mobile-egress.relay.plist'; do
    /usr/bin/plutil -insert "bundleLayout.$index" -string "$required" "$RECORD_PLIST"
    index=$((index + 1))
done
/usr/bin/plutil -insert nestedRelaySignature -string valid "$RECORD_PLIST"
/usr/bin/plutil -insert appSignature -string valid "$RECORD_PLIST"
/usr/bin/plutil -insert packageSignature -string valid "$RECORD_PLIST"
/usr/bin/plutil -insert notarization -string accepted "$RECORD_PLIST"
/usr/bin/plutil -insert staple -string valid "$RECORD_PLIST"
/usr/bin/plutil -insert checks -dictionary "$RECORD_PLIST"
for check in codesign pkgutil spctl stapler; do
    /usr/bin/plutil -insert "checks.$check" -string passed "$RECORD_PLIST"
done
/usr/bin/plutil -convert json -o "$RECORD_TEMP" "$RECORD_PLIST"

(
    cd "$REPOSITORY_ROOT"
    "$GO_BIN" run ./windows-client/cmd/mobile-egress-macos-release validate-record "$RECORD_TEMP" "$RELEASE_VERSION" "$SOURCE_COMMIT" "$NODE_MANIFEST_SHA" "$ARTIFACT_SHA" "$APPLICATION_IDENTITY" "$INSTALLER_IDENTITY"
)
/bin/mv "$PKG" "$FINAL_PKG"
/bin/mv "$RECORD_TEMP" "$FINAL_RECORD"
trap - EXIT HUP INT TERM
/bin/rm -rf "$WORK"
printf 'Notarized PKG: %s\nVerification record: %s\nSHA-256: %s\n' "$FINAL_PKG" "$FINAL_RECORD" "$ARTIFACT_SHA"
