# Compact pairing and connection-update QR repair

## Evidence and scope

Android code 23 reads a short control QR but still cannot capture the Mac's dense invitation. Text import successfully paired the same Mac, and actual HTTP/CONNECT/SOCKS traffic passed. The owner then successfully scanned a compact-format public sample: its camera closed with the expected invalid-code error. This tests the proposed optical representation before another app change.

The original public sample occupied 149 modules (596 pixels at four pixels/module); the compact sample occupies 125 modules (500 pixels). The disposable sample cannot enroll or reserve a Client. Keep the existing bundled detector, native preview and camera lifecycle. The repair changes only the QR representation at the desktop/mobile import boundary. Trust, capabilities, ten-minute expiry, one-use redemption, signatures, enrollment and live transport stay unchanged. Existing saved pairings and plain setup-code import remain intact.

## Interface

- Compact QR text is `MEQR1:` followed by canonical unpadded base64url of **one zlib stream containing the original decoded bundle JSON bytes**. Do not compress the outer base64 string: that provides almost no reduction.
- Desktop continues returning/copying the original canonical base64url bundle in `BundleView.Bundle`; only `QRDataURL` uses the compact envelope. Four-pixel modules and the full quiet zone remain.
- Android and iOS normalize compact user input back into the exact original canonical bundle before existing invitation/update parsing. Older uncompressed QR/text inputs remain supported. Do not add compact acceptance to generic signature/capability decoding or network protocols.
- Input text is bounded to 87,384 characters; compressed and expanded bytes are each bounded to 65,536. Enforce expansion bounds while inflating. Require nonempty output, a complete zlib stream, valid checksum, no dictionary and no trailing/concatenated data. Reject malformed/noncanonical base64url and unknown envelope versions. Do not expose input or decoder exception details.
- No new dependency, external lookup, server API, trust bootstrap, account behavior, gateway change, usage collection or cloud service is needed. Use native standard zlib implementations.

## Ordered checklist

- [x] Establish working pairing/traffic via text import and physically verify the compact optical sample.
- [x] Document this plan before product implementation; retain earlier test/build evidence and physical failure records.
- [x] Add public cross-language fixtures for compact direct/hosted invitations and signed updates, plus malformed/oversized/truncated/trailing-data cases.
- [x] Implement bounded mobile normalization on Android and iOS with red/green tests. Preserve old input and signature validation.
- [x] Emit compact desktop QRs with unchanged copyable setup codes. Test exact bytes, reduced density, quiet zone/module sizing and invalid input. Update optical harness coverage.
- [x] Update mobile manifest, protocol/onboarding guidance and sibling Inevitable pilot record. Final artifact acceptance records follow packaging. No Inevitable frontend patterns or accepted infrastructure responsibilities change.
- [x] Run relevant Go/frontend/release-contract gates, Android tests/lint/build, native Swift/iOS build checks, manifest/schema and independent security review; record the Xcode runner limitation below.
- [x] Commit source, build same-identity local Android code 24 and signed/notarized Mac validation installer using existing guarded local build paths. Keep earlier artifacts immutable. No release orchestration/tag/publication or main merge.
- [x] Verify exact hashes/versions/signers and report unavailable native/physical checks. Preserve the owner's working Mac/phone pairing; installation on the owner devices is not performed by this change.

## Acceptance and rollback

All languages must recover identical original bytes from shared fixtures. Malformed wrappers must fail before repository pairing, key creation, slot reservation or update application. Valid signed updates must still pass their original signature checks; mutated expanded content must fail normal trust validation. Cross-platform tests must cover old and compact input and decompression limits without capturing live invitations.

Existing phones require the compact-capable update to scan newly generated compact codes; older desktop QR/text codes remain valid in updated phone apps. Text import remains a compatible recovery route. Do not claim the diagnostic sample proves final fresh-pairing acceptance. Retain Android/iOS lifecycle distinctions and all remaining physical/release gates.

Rollback is a later same-signer Client build that resumes generating original QR text. Updated phones continue accepting the original format. No stored data or protocol migration is required.

## Implementation and validation record

The Go encoder and Android/iOS normalizers share three valid and thirteen malformed public vectors. Normalizers additionally test raw-input and expansion boundaries, exact signed-update verification and rejection after tampering. Behavioral red tests were observed before implementation in all three languages; Android's plain-code compatibility case already passed. The desktop renderer regression also failed against the original dense representation before being updated.

Android: 316 JVM tests in 47 suites, no failures/errors/skips; lint has zero errors and 18 existing warnings; debug assembly passes. Native Swift: 373 tests, zero failures and two existing opt-in security acceptance skips; all seven compact tests execute. Unsigned iPhoneOS and simulator app/extension builds pass. The additional Xcode package test runner compiled but could not attach to `testmanagerd.control` under SSH, including one runbook retry. This remains an infrastructure blocker; native Swift execution is recorded separately.

Independent review found one caller-boundary issue: iOS paste/file callers trimmed before the size check. They now pass raw input to the bounded normalizer, and the existing file-size limit matches the import limit. Swift additionally bounds UTF-8 bytes; the ASCII compact format has the same limit on both platforms. Android file import is deliberately unchanged because exported update files remain uncompressed. No generic binary/signature decoder or repository network path accepts the envelope. Final parser/encoder review has no outstanding security findings.

The full Windows integration gate passes: all Go packages, installer contracts, vet/build, 71 frontend tests/syntax, Desktop/direct/release contracts and mobile manifest/schema. Android Git signing-path regression and changed documentation local links pass. The native optical harness passes all 90 required frames (45 historical, 45 compact), plus six informational low-resolution observations. Its compact grid is generated from the real Go renderer and restores exactly the shared fixture. Historical grids remain unchanged. The existing IPC test's placeholder was replaced with canonical public test input because rendering now strictly rejects malformed source bundles.

Preserved evidence: `G:/codex-build-cache/mobile-egress-hosted-20261004/android-compact-qr-code24/`, `ios-compact-qr-20261005/` and `mac-client-compact-20261005/`. The diagnostic sample's physical success and existing text-import traffic are separate from fresh compact pairing with final artifacts.

## Local signed artifacts and remaining acceptance

Clean source `23f748c44434ffa604aa2f41aa1e45252dd5b8b4` is committed/pushed on the feature branch. The native Apple Silicon Client package tests, vet, race detector and 71 frontend tests pass. The existing guarded local signing paths produced:

- Android `2.0.0` / code `24`, 11,864,970 bytes, SHA-256 `ed3a06f85d765dd49d4af7fe2e6d49932587a9ac1cee122204fa472cea58dff8`. Original signer verified; R8/release lint/assembly passed. Package/minimum SDK/target SDK verified; all four ABI library sets and license asset are byte-identical to verified code 23, and APK 16-KiB alignment passes. Immutable artifact: `android-compact-qr-code24/zfnf-mobile-egress-android-2.0.0-code24.apk` under the evidence root.
- Mac `2.0.0-hosted-validation.20261005.1`, 13,767,283 bytes, SHA-256 `a7b6c0e7c79b5bb382097d164cd309d2d0657260b79c775243eb2c425f571004`. Existing Developer ID Application/Installer identities, notarization, staple, Gatekeeper, exact source/version/provenance and transfer hash all pass. Immutable artifact: `mac-client-compact-20261005/mobile-egress-client-macos-2.0.0-hosted-validation.20261005.1-arm64.pkg` under the evidence root.

Earlier artifacts remain untouched. No owner-device installation, pairing removal, deployment, tag, public release or main merge occurred. Physical signed-upgrade/pairing retention and fresh compact invitation/update scanning remain owner acceptance on Android and iPhone. iOS unsigned builds/native tests pass, with the separate Xcode testmanagerd and opt-in security gates recorded above. Existing release blockers remain; do not describe the signed local artifacts as generally release-ready.
