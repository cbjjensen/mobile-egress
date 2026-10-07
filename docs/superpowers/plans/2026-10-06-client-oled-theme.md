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

At source validation, no pairing, service, mobile app, published artifact, R2
catalog or website behavior changed and no signed installer had been produced.
The later authorized publication is recorded below. Native window
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

- [x] Commit the tested OLED changes and publication record on clean `main`.
- [x] Build/sign/notarize and verify both installers with `release-desktop.ps1`.
- [x] Publish the exact frozen Desktop artifacts to GitHub as a prerelease.
- [x] Mirror them to R2 and verify public bytes before promoting the full pilot
      catalog, retaining Android's verified 2.0.3 source/artifact.
- [x] Record public hashes, download links, website handoff and acceptance limits.

Preserve the unrelated `.codex-remote-attachments/` directory locally; exclude that
tool-owned attachment directory through `.git/info/exclude`, not source history.
Publication does not authorize deploying unrelated website changes, enabling sales,
altering phone builds or claiming signed physical upgrade acceptance.

### Publication evidence

Published [v2.0.4](https://github.com/cbjjensen/mobile-egress/releases/tag/v2.0.4)
from source `d9ac6d73971d0a6c39d5bdeee2d03146509e8608` with the guarded
`scripts/release-desktop.ps1 -ReleaseVersion 2.0.4 -Publish` workflow. Windows
Authenticode identity verification passed. Mac Developer ID signatures, hardened
runtime, notarization, stapling and Gatekeeper checks passed. The release is a
prerelease containing exactly the two selected Desktop assets.

| Download | Bytes | SHA-256 |
|---|---:|---|
| [Windows installer](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/2.0.4/InevitableMobileRelaySetup.exe) | 25,838,880 | `2a6098936cdadcd02596e942e242e52d88ff522920b89313d593aa1a6c16c328` |
| [Apple Silicon Mac PKG](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/2.0.4/inevitable-mobile-relay-macos-2.0.4-arm64.pkg) | 14,612,221 | `4186715870bed90ede0fad362f8d2a60b6a76f2d01afe61933584b0dca917907` |

GitHub asset digests matched the frozen files. R2 publication verified immutable
objects, public bytes, hashes, sizes and attachment/cache headers before updating
the pilot catalog. Independent catalog readback confirmed Windows/Mac 2.0.4 and
unchanged Android 2.0.3 (code 27), SHA-256
`c5277ffdb122518e6f0b0a38abfbb956b8ee8f3ec9ca0b898faf63ad447f8185`.
Historical releases and unrelated R2 objects were preserved; iPhone builds and
TestFlight settings were not changed.

Private release/signing records, logs and the complete R2 plan are retained at
`G:\codex-build-cache\inevitable-mobile-relay-2.0.4`. The system drive approached
capacity during publication, so this task's completed native validation evidence
and signed Windows payload were moved there with original-path junctions and
hash verification. No evidence was discarded.

### Website download handoff

Prepared only `MOBILE_EGRESS_WINDOWS_DOWNLOAD_URL` and
`MOBILE_EGRESS_MACOS_DOWNLOAD_URL` changes to the verified 2.0.4 public URLs.
Read-only production preflight matched all 238 deployed environment keys and the
healthy backend/UI/outbox source `04a40086af13f3e0e426ecd5a63f155b6356c86a`.
Published the exact two-key update to the existing production environment bundle.

The frozen-source rollout, run `37494987155` attempt 2, was cancelled while queued
after discovery of independently started production run `37562440335` at newer
source `9fc9f70a3cae1bf403c15c10d1b13df61d06113d`, preventing an older-source
redeployment after that run. The independently initiated deployment succeeded at
02:43:02 UTC on 2026-10-07 (2026-10-06 local), without Terraform apply or either
gateway deployment. Read-only verification `3ac06172-015d-4799-a8cb-6accb8e8282f`
matched all 238 candidate environment settings, with no extra, missing or different
keys. Backend/UI/outbox run the expected newer source, backend health is healthy,
and its actual download environment matches the candidate.

The authenticated live Mobile Relay page now links Windows/Mac 2.0.4 and Android
2.0.3. The backend's `https://api.inevitableproxies.com/healthz` and `/readyz` both
returned HTTP 200 with `status: ok`; PostgreSQL and Redis readiness passed. The
website origin's `/healthz` also returned `ok`. Its `/readyz` is an HTML SPA fallback,
so readiness evidence uses the API origin. Local ignored `.env.production` was
synchronized only after confirming its baseline hash was unchanged; a protected
backup is retained. The website handoff is complete with sales, invitation
automation and all other settings preserved.

Signed physical upgrade/repair, native startup appearance and phone
interoperability acceptance are still separate pilot gates. Publication does not
establish those results or promote these downloads to stable.
