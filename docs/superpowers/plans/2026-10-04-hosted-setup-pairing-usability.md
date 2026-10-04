# Hosted setup handoff and readable pairing QR

## Scope and current evidence

Repair the two reported pilot setup failures on the existing hosted feature branch. Preserve the installed owner, authority, activated device, proxy credentials and any pending pairing. Do not deploy infrastructure, merge, publish a release, or change the direct/hosted wire protocol.

The signed Windows validation installer `2.0.0-hosted-validation.20261004.1` is installed. Protected IPC works; the service reports authorized, gateway connected, and awaiting phone. Physical phone pairing/traffic is not yet accepted.

- Browser setup required a second click. The automatic activation worker cancels its own context before saving the hosted endpoint. The frontend also assumes it observes a pending snapshot immediately before approval.
- The submitted desktop QR screenshot cannot be decoded with the Android APK's ZXing 3.5.3 decoder, even before camera losses. A 512-pixel QR is rendered inside a 280 CSS-pixel border box. Dense invitations need readable module sizes and preserved quiet zones.
- Android and iOS scanner-unavailable feedback has a separate gap. Record it for a focused parity repair; establish the primary desktop optical fix first without forcing an APK replacement.

## Implementation checklist

- [x] Inspect service activation, protected stores, Client frontend, QR encoder and Android decoder. Preserve existing branch and live activation.
- [x] Add failing automatic-worker and missed-status-poll regression tests. Finish endpoint setup before cancelling the worker; advance automatically only when saved configuration is ready, respecting Finish later and explicit direct setup.
- [x] Add a representative dense invitation QR regression. Render whole QR modules at sufficient size without CSS shrinking; preserve invitation bytes, trust, expiry and one-use behavior.
- [x] Run targeted tests, decode representative generated images with the installed Android decoder, review both fixes, and run Windows/frontend/release-contract/mobile-manifest gates.
- [x] Update implementation evidence and synchronized Inevitable pilot records. Commit independently reviewable source changes on the existing feature branch.
- [x] Build and verify a new signed local Windows validation installer with the existing signer. Use the normal installer handoff; preserve activation and pairing. No public release.
- [x] Verify installed status and retry phone scanning/pairing with the owner. Record physical HTTP/CONNECT/SOCKS acceptance separately; never infer it from socket or gateway status.

## Interfaces and acceptance

Keep protected IPC methods and enrollment payloads compatible. QR rendering may change presentation only. Browser approval must finish without a second button click, including when a status poll misses pending state. A still-valid invitation must remain the same across redisplay. Generated dense QR images must round-trip through ZXing with exact bytes and render without fractional module shrinkage. Failure messages must remain secret-safe.

## Risks, rollback and commit boundaries

Activation tests must exercise the actual worker context, not only a manual background-context call. Avoid cancellation leaks or saving after service shutdown. Large QR images must remain accessible in smaller windows without resampling. Keep regressions with the service/UI fix; keep presentation regressions with QR changes. Roll back through a signed installer preserving protected settings; do not delete state or replace authority. Signing and physical checks remain explicit blockers until performed.

## Validation record

- Initial live read-only status: hosted, authorized, gateway connected, awaiting phone, no acknowledged pairing.
- Original submitted screenshot: ZXing 3.5.3 `HybridBinarizer` + `TRY_HARDER` fails on full screenshot, crop, pure-barcode crop and nearest-neighbor enlargement. No invitation data logged.
- Automatic-worker regression failed before the fix: approval did not persist the endpoint without Resume. Both missed-pending/readiness frontend regressions failed before repair. Service now finishes configuration before cancelling its worker; frontend advances on usable authorization rather than an exact prior pending snapshot.
- Dense QR regression failed against fixed 512px generation: hosted 153 modules, direct 149, signed update 97 did not divide evenly into the image. Four pixels per module plus native-size/pixelated CSS preserves exact payloads, quiet zones and whole module grids. Default window width is 940px; smaller windows scroll instead of shrinking and show enlargement guidance. This applies to both pairing and update QRs.
- Android's exact ZXing 3.5.3 decoder round-tripped hosted (612px) and signed update (388px) fixtures at 100/125/150/175/200% scaling with both nearest and bilinear interpolation, all 20 comparisons exact. The direct fixture (596px) is valid but triggers a separate upright ZXing finder ambiguity: pure-barcode and 90/180/270-degree rotations decode; the 90-degree fixture passes all ten DPI/interpolation comparisons. Do not describe that fixture as passing upright. Three-pixel modules were rejected as the default because 150% scaling produced detector failures.
- Full `scripts/test-all.ps1 -Components Windows` passed: Go tests/vet/build, installer/payload and release contracts, manifest/schema, 42 frontend cases and JS syntax. Independent review found no actionable regressions. Windows race tests could not run because CGO/compiler support is unavailable; no race-pass claim is made. Established Windows publisher validation passed.
- Android/iOS source and wire formats remain unchanged. Additional scanner-unavailable feedback gaps and Android's orientation-sensitive detector are recorded for a focused mobile follow-up if physical scanning still fails; the existing APK can test the corrected desktop QR first. Manifest evidence is unchanged and validation passed. Native Mac installation and physical phone acceptance remain separate unperformed gates.
- The existing unexpired pilot invitation was re-rendered without changing its payload/reservation: 612px PNG passed ordinary ZXing decoding at 100/125/150/175/200% with both interpolation modes. Its short remaining lifetime was not extended or reset. A fresh invitation can be requested normally after expiry.
- Source `8bb6f090dbb03fcf2cad066aef06e5517b15ac36` was committed and pushed on the existing hosted branch. Guarded local build `2.0.0-hosted-validation.20261004.2` passed complete `Assert-MobileEgressDirectWindowsArtifacts` verification: all three timestamped signatures use the established publisher; exact clean source, platform, versions and embedded payload match. Installer SHA-256: `10BC8F97F01171F87436A928935884C68D3C8F521DF6A03B186114ACB30AEAF4`. The normal installer was opened for owner confirmation. Installation and physical scan/traffic acceptance remain pending at this checkpoint; no tag or public release was created. Synchronized Inevitable documentation is committed separately as `f96ddf57` on `codex/mobile-egress-setup-pilot-record`; no production change or main merge was performed.

## Owner feedback: explicit phone handoff

The owner installed `.2` and confirmed that the QR scanned successfully. Their screenshot showed acknowledged pairing but a waiting phone session; the owner found the verification page unclear about needing to act in the mobile app. Subsequent protected status confirmed `.2`, authorized, paired, gateway connected and an authenticated live phone connection. Traffic acceptance is recorded separately after actual requests.

Keep this follow-up small: rename step 4 to **Start on your phone**, put phone instructions before technical connection status, name the existing native buttons (**Start cellular Agent** on Android and **Start sharing** on iPhone), explain automatic progression, and collapse troubleshooting. Keep iPhone foreground requirements explicit. Do not change the phone apps, start sharing remotely, replace pairing, require Wi-Fi changes in the normal path, or treat gateway attachment as phone connectivity.

Implementation also removes stale invitation-ready feedback when consumed/expired, without clearing unrelated errors. Unpaired and awaiting-acknowledgement states retain scan/confirmation guidance and cancellation; Start instructions appear only for an acknowledged, disconnected phone. Connected status still advances automatically. Existing dashboard-on-reopen behavior is preserved.

The focused regressions failed before implementation and all 43 frontend tests passed afterward, with JavaScript syntax and whitespace checks passing. Full Windows validation and signed update preparation follow; the current live phone connection must not be interrupted by automatically running an installer for this copy change.

The full Windows integration gate passed (Go tests/vet/build, installer/release contracts, manifest/schema, 43 frontend tests and syntax). Independent review prompted one wording correction: unavailable service status must not claim a saved pairing for an unpaired Client. Frontend checks were rerun after that correction. No other actionable findings remained.

The follow-up is committed as `4c3bdc3f5759fe69f8f3a41072da05e3f4c05ea9`. Guarded local Windows build `2.0.0-hosted-validation.20261004.3` passed full artifact verification with the established timestamped publisher identity, exact clean source/platform/version and embedded payload. Installer SHA-256: `F08C170C0609BD92545727DCFF85A23226AEFD36F4CAC77EFD10694725820553`. It is prepared but deliberately not installed during the live owner test; final protected status still confirms `.2` paired and connected. No public release, service interruption or pairing replacement occurred.

### Windows/Android live traffic smoke

With the owner-operated Android phone connected to installed Windows `.2`, three tiny requests through the protected local proxy credentials passed: HTTP absolute-form returned 200 in 583ms; HTTPS over HTTP CONNECT returned 200 in 944ms; HTTPS over SOCKS5 returned 200 in 944ms. All returned the same valid public exit IP, different from a direct PC HTTPS comparison (200 in 223ms). The phone remained paired and connected. The helper used credentials only in memory and changed no clipboard, configuration, pairing or service state. No raw secrets or public IPs are included in this record.

This is an actual Windows/Android hosted traffic smoke, not a throughput benchmark, independent carrier identification, Android lifecycle test, iOS/Mac acceptance, or Core coexistence qualification. The ignored helper is under `windows-client/.local/operator/trafficprobe/`.
