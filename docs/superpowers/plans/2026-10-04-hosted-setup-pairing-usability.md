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
- [ ] Build and verify a new signed local Windows validation installer with the existing signer. Use the normal installer handoff; preserve activation and pairing. No public release.
- [ ] Verify installed status and retry phone scanning/pairing with the owner. Record physical HTTP/CONNECT/SOCKS acceptance separately; never infer it from socket or gateway status.

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
