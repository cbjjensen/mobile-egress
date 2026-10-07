# Windows and Mac Client OLED alignment

The owner reported that the Windows and Mac Clients do not match Inevitable's
OLED theme. Both native apps embed the same `clientapp/assets/index.html`, whose
older blue-gray colors survived the move away from the retired controller.

## Scope and design

Use the current Inevitable website's `ui/src/styles/themes/oled.css` as the palette
reference: black canvas, near-black cards and fields, cool-white text, mint
actions/success, pale-blue information/focus, and rose destructive/error states.
Centralize these colors in semantic CSS variables in the existing shared asset.
Set the native startup background to black and request dark window appearance on
both platforms. Preserve the current layout, navigation, application logic,
branding assets, QR pixels, identifiers, pairing and protected storage.

This is a bounded presentation correction, with no service, protocol, mobile
lifecycle, website or infrastructure change. No new dependency or theme selector
is needed. Existing published artifacts remain immutable; source validation does
not update installed Clients or publish replacement installers.

## Checklist and acceptance

- [x] Trace both native builds to their shared interface and compare OLED tokens.
- [x] Apply semantic colors to all cards, controls, states and supporting text.
- [x] Match native Windows/Mac startup and window appearance configuration.
- [x] Run existing Client frontend checks and applicable Go/native build checks.
- [x] Review synthetic setup, connected, offline, recovery and destructive states
      at the default 940x760 and minimum 620x650 window sizes; verify focus,
      disabled controls, contrast and unchanged QR rendering.
- [x] Record validation and any native/signing limits.

Rollback reverts only the shared presentation and native window options. It must
not modify any installed pairing, service or credential state.

## Validation

Passed `scripts/test-all.ps1 -Components Windows`: manifest/schema validation,
release/installer/R2 contracts, all Go tests/vet/build, all 71 existing Client
frontend tests and JavaScript syntax. A separate production-tagged Windows GUI
compile passed. Release tests used their normal isolated fixtures; nothing was
published or uploaded by this validation.

An isolated snapshot of current tracked working files passed native Apple Silicon
`clientapp` and GUI package tests, focused vet and the production-tagged GUI build
with Go 1.26.7/Wails 2.14.0 on macOS 26.2/Xcode 26.6. The binary retains minimum
macOS 13.0. Archive SHA-256:
`bffadabd4711233a6abf9a1aeff841db65f550e9de2f18200da5a6b003df6ba8`.
Per-file hashes were verified before/after transfer and native checks. The unique
remote scratch directory was cleaned; the shared Mac checkout was untouched.
Only existing Keychain C API deprecation warnings appeared.

Browser previews used the real shared HTML/JavaScript with an isolated synthetic
local service fixture, not live pairing or credentials. Eight states at each
window size passed: connected, offline, fresh setup, phone pairing, start sharing,
pending connection update, Phone settings and service error. All 16 had a black
canvas, no horizontal overflow and no JavaScript errors. Cards use `#080a0f`.
Keyboard focus is pale blue; measured text contrast is 19.23:1 for body text,
9.79:1 for muted card copy and 14.87:1 for the primary action. Disabled setup steps
remain disabled. QR CSS dimensions/pixels were left untouched, no image filter is
applied, and the existing Go QR tests passed.

Private local screenshots/results are in `.local/client-oled-20261006/`. Native
Mac command/hash evidence is in
`.local/mobile-egress-client-oled-7de48075a5604e59b7211b509594794e/`.

No pairing, service, mobile app, published artifact, R2 catalog or website behavior
changed. No new signed installers were produced or installed. Native window
chrome/startup appearance and signed upgrade acceptance still require actual
Windows/Mac installation checks; browser previews and successful native builds
do not establish those results. Updated downloads require a new immutable release.

## Authorized publication: 2.0.4

The owner explicitly requested publishing the updated Windows and Mac Clients on
2026-10-06 after launching the local Windows preview. Use the guarded coupled
Desktop publisher for **2.0.4**, retaining the existing pilot channel and signing
identities. This authorizes signing/notarization and GitHub/R2 publication of those
two installers. Android remains the exact verified 2.0.3 APK and iPhone remains on
its existing TestFlight releases. Never append to or replace frozen 2.0.3 assets.

- [ ] Commit the tested OLED changes and publication record on clean `main`.
- [ ] Build/sign/notarize and verify both installers with `release-desktop.ps1`.
- [ ] Publish the exact frozen Desktop artifacts to GitHub as a prerelease.
- [ ] Mirror them to R2 and verify public bytes before promoting the full pilot
      catalog, retaining Android's verified 2.0.3 source/artifact.
- [ ] Record public hashes, download links, website handoff and acceptance limits.

Preserve the unrelated `.codex-remote-attachments/` directory locally; exclude that
tool-owned attachment directory through `.git/info/exclude`, not source history.
Publication does not authorize deploying unrelated website changes, enabling sales,
altering phone builds or claiming signed physical upgrade acceptance.
