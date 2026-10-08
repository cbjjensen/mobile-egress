# Mac app DMG release and acceptance

The user app is an additional Mac format from Desktop 2.0.6. The existing PKG
continues installing the same service and using its established system storage.
The DMG contains `Inevitable Mobile Relay.app` and `Read Me.txt`. Copy the app into
the user's own `~/Applications` (or another folder they own); no administrator
password, daemon, LaunchAgent, login item or automatic startup is involved.
Keep the app open while using proxies. Minimize keeps traffic active; close,
Quit and sign-out stop traffic. The formats have separate activation/identity/
pairing; a same-signed DMG app upgrade retains per-user state.

The GUI bundle identifier remains `com.zfnf.mobile-egress.client.app` and its
executable remains `mobile-egress-client-app`. The `client_usermode` build tag
selects the user runtime explicitly. Signed Info.plist records
`MobileEgressRuntimeMode=app` and `MobileEgressSourceCommit`; the default PKG GUI
records `service`. An unavailable daemon never selects the user runtime.

## Guarded publisher flow

Use `scripts/release-desktop.ps1 -ReleaseVersion <version>` after review,
clean-main and the applicable release gates. Desktop contracts from 2.0.6 require
Windows EXE, Mac PKG and `inevitable-mobile-relay-macos-<version>-arm64.dmg`.
Historical release contracts and artifact bytes remain immutable. Android/iOS
are independent of this Desktop change.

`scripts/release-client-dmg.sh` runs on the Apple Silicon development Mac with
the established Developer ID Application identity and notary API inputs. It
builds an app-only payload in a private stage, verifies actual executable
mode/version/clean embedded source and macOS 13/arm64, signs with hardened
runtime, notarizes a zip of the app, staples the app, creates/signs/notarizes/
staples the image, then mounts it read-only and verifies signed payload, source,
exact executable hash, instructions, both tickets and Gatekeeper acceptance.
Both app and image must pass. Final output uses exclusive links with rollback
of this invocation's output after a promotion failure; existing or concurrently
created outputs are never overwritten.

Private evidence is `inevitable-mobile-relay-macos-<version>-arm64.dmg.verification.json`.
Schema 1 binds version/source/image hash, app identifier/executable, actual and
signed runtime mode, executable version/source/mounted hash, platform, Developer
ID, hardened runtime, both signatures/notarizations/staples and native checks.
`validate-client-dmg-record` rejects incomplete or mismatched evidence.
Verification JSON stays private.

The Windows publisher transfers PKG and DMG into unique partials and validates
both remote/local hashes and records before promoting either format. A later
failure rolls back its created outputs, preserves concurrent files and removes
partials. All three public artifacts participate in the existing source-bound
freeze, GitHub draft verification and R2 public checks. Schema 1 retains PKG
`platforms.macos` metadata and adds `alternatives.dmg`; a Mac selection expands
to both formats from 2.0.6. See [R2 downloads](r2-downloads.md).

## Native validation and field acceptance

The standalone shell entry point also supports a fresh prerelease validation
name (for example `2.0.6-user-validation.20261007.1`) without invoking Windows,
freezing a tag or uploading. It requires all native signing/notary checks and
an exact clean source commit. Public Desktop/R2 require canonical versions.

Offline checks cover contracts, staging, records and failure behavior. Native
signing/notary/stapling/Gatekeeper and mounted-payload checks must pass on the
actual released artifact. Record standard-user installation, Keychain error/
upgrade behavior, duplicate launch, PKG coexistence, occupied ports, lifecycle
traffic cancellation, real phone QR pairing and cellular HTTP/CONNECT/SOCKS
acceptance separately. Missing field evidence is not proof of those behaviors.

On 2026-10-07 the owner explicitly authorized releasing this pilot without
waiting for tester confirmation. Keep any unperformed standard-user or physical
phone acceptance marked unverified, while retaining automated/native publisher
checks, pilot status, immutable artifacts and scoped website/live-link checks.

Offline checks: `go test ./windows-client/internal/macosrelease
./windows-client/cmd/mobile-egress-macos-release`,
`scripts/test-user-dmg-release.ps1`, existing release suites, and
`node --test scripts/test-r2-downloads.mjs`. The full component gate includes
the new Desktop DMG contracts.
