# Mac app DMG release and acceptance

The user app is an additional Mac format from Desktop 2.0.6. The existing PKG
continues installing the same service and using its established system storage.
The DMG contains `Inevitable Mobile Relay.app` and `Read Me.txt`. Copy the app into
the user's own `~/Applications` (or another folder they own); no administrator
password, daemon, LaunchAgent, login item or automatic startup is involved.
Keep the app open while using proxies. Minimize keeps traffic active; close,
Quit and sign-out stop traffic. The formats have separate activation/identity/
pairing; a same-signed DMG app upgrade retains per-user state.

## HTTP proxy compatibility (2.0.10)

Both Mac runtime modes display and copy HTTP proxy lines as
`localtest.me:PORT:USERNAME:PASSWORD`. The first phone keeps port 1081;
later slots retain their existing odd ports through 1099. SOCKS5 remains on
numeric `127.0.0.1`. Listener binding is always numeric IPv4 loopback;
the compatibility hostname is never a bind address, and no alias,
administrator prompt, firewall change or additional service is introduced.
Existing installations need the updated build and a fresh HTTP copy;
credentials, ports, activation and pairing remain intact.

`localtest.me` is third-party DNS that resolves to loopback. The consuming
application needs working DNS; DNS rebinding protection may block it. For
applications that accept numeric loopback, `127.0.0.1` with the same details
still works. Refract rejects that numeric entry. No automatic alternate
address or networking change is made if DNS fails.

On 2026-10-15 the owner confirmed Refract accepted the hostname and worked
with the existing DMG after editing its copied HTTP line. An authenticated
HTTPS CONNECT check on the same Mac returned 200 through both the hostname
and numeric loopback, with a proxy exit IP different from the Mac's direct
connection. An earlier CONNECT timeout did not recur; its cause is unknown.
This validates the manual compatibility format, not a newly packaged release.
New-build signing, notarization and distribution remain separate release gates.

Source validation on 2026-10-15 passed the Windows component gate and native
macOS `go test ./...`, `go vet ./...` and `go build ./...`. The Mac also passed
the `client_usermode` runtime tests and app build, plus race checks for the
node service, HTTP CONNECT, SOCKS and proxy endpoint packages. Regression
coverage checks displayed and copied HTTP/SOCKS lines at slots 0, 1 and 9.
Native builds retain existing deprecated Keychain API warnings. Validation
used a private source directory without replacing the installed app; no
installer was signed, notarized or published.

The owner authorized release on 2026-10-15. Prepare Desktop 2.0.10 through
the existing coupled Windows/PKG/DMG publisher and keep the pilot channel.
Advance only Mac PKG/DMG in the customer download catalog; Windows 2.0.8,
Android 2.0.3 and iOS remain on their existing customer versions. Signing,
notarization and public download verification remain mandatory release gates.

The GUI bundle identifier remains `com.zfnf.mobile-egress.client.app` and its
executable remains `mobile-egress-client-app`. The `client_usermode` build tag
selects the user runtime explicitly. Signed Info.plist records
`MobileEgressRuntimeMode=app` and `MobileEgressSourceCommit`; the default PKG GUI
records `service`. An unavailable daemon never selects the user runtime.

## PKG background activity attribution

The PKG daemon's `AssociatedBundleIdentifiers` must name the installed GUI,
`com.zfnf.mobile-egress.client.app`. The GUI's bundle/display name is
`Inevitable Mobile Relay`. Native staging checks that the association is a
string matching the staged app's bundle identifier before building or signing.
Preserve the daemon label, executable, storage and signing identities.

[Apple documents](https://developer.apple.com/documentation/servicemanagement/updating-helper-executables-from-earlier-versions-of-macos)
that legacy services without this association fall back to their signing
certificate's organization name in Login Items. The missing association was
identified on 2026-10-14 after a reported background-activity notification used
the individual signer name. This corrects app attribution; it does not remove
the name from the Developer ID certificates or guarantee every security prompt
uses the product name. Removing that certificate exposure requires an Apple
organization identity and a separately planned signing migration. Existing
signed downloads retain their original identity.

For release acceptance, check a fresh signed PKG installation and an upgrade
from the previous PKG on an Apple Silicon Mac. Verify both the background
notification and Login Items & Extensions show the product app. Inspect the
installed plist and the relevant `sfltool dumpbtm` entry locally; do not publish
the full dump because it contains unrelated user/application information.
Also check PKG/DMG coexistence, where both app formats share a bundle identifier.
Retain activation, pairing and existing user background permissions. macOS may
cache old attribution; do not reset its global background-item database or
promise an upgrade will erase an already-delivered notification. Automated
metadata/staging checks do not establish these customer-visible results.

On 2026-10-14 the owner authorized publishing the background-name correction.
Prepare Desktop 2.0.9 through the existing coupled Windows/PKG/DMG publisher;
Android 2.0.3 and iOS remain unchanged. Windows and native Apple Silicon
packaging tests pass, including rejection of missing, wrong-app and wrong-type
associations. Retain the existing Developer ID identities and pilot channel.
Signing/notarization and public download verification are release gates;
fresh-install/upgrade notification and Login Items UI acceptance remain pending.

### Published 2.0.9 pilot

[Desktop 2.0.9](https://github.com/cbjjensen/mobile-egress/releases/tag/v2.0.9)
was published on 2026-10-14 from clean source
`92605e948287bb416857f0ac50e2db27776a4a25`. The existing coupled publisher ran
the complete component gate (including Go tests/vet/build, installer/release
contracts, 19 R2 tests and 88 Client frontend tests), verified established
signers, notarization, staples and Gatekeeper, and verified every GitHub asset
digest before exposing the prerelease. The Mac DMG also passed app/image and
mounted-payload checks. A separate expansion of the signed PKG verified the
actual bundled daemon association, app identity/name/version and stable daemon
label/root user. This is package evidence, not notification UI acceptance.

| Artifact | SHA-256 |
| --- | --- |
| `InevitableMobileRelaySetup.exe` | `9b6141a680c787322a256845297a2dad6a393becfdcd2bfe554274548b94c5fa` |
| `inevitable-mobile-relay-macos-2.0.9-arm64.pkg` | `cda2b8b44514569358a99e7a9c4036f817e3620c90f2e1dee469d5a585b6c6a1` |
| `inevitable-mobile-relay-macos-2.0.9-arm64.dmg` | `47990647e432630f1bcf241a2dcac1927f527570e509a03095968cddb954bdd0` |

Guarded R2 dry-run/publication and independent public catalog readback passed.
Only the customer catalog's Mac PKG/DMG entries advanced to 2.0.9; Windows 2.0.8
and Android 2.0.3 entries are byte-for-byte equivalent as JSON values. iOS was
not released. Read-only SSM verification
`de5f0427-f357-4285-999d-33b81fb0ec6a` ran the deployed download resolver with
its real configuration and confirmed the new Mac links and preserved other
platforms. The backend remained on source
`ce1153bd7cb09f8aff188a4ce4ddfd6898243a19`; no website deployment or environment
edit was needed. Existing HTTP-process caches may take five minutes to refresh.

The dashboard browser could not attach, so authenticated rendered-page links
were not independently inspected. Fresh-install/upgrade notification and Login
Items appearance, cached attribution and PKG/DMG coexistence remain unverified.
Developer ID certificate names and TestFlight identity remain unchanged.

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
