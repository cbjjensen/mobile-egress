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
- [ ] Commit source, build same-identity local Android code 24 and signed/notarized Mac validation installer using existing guarded local build paths. Keep earlier artifacts immutable. No release orchestration/tag/publication or main merge.
- [ ] Verify exact hashes/versions/signers and report any unavailable native/signing/physical checks. Preserve the owner's working Mac/phone pairing; installing/testing must not silently revoke it.

## Acceptance and rollback

All languages must recover identical original bytes from shared fixtures. Malformed wrappers must fail before repository pairing, key creation, slot reservation or update application. Valid signed updates must still pass their original signature checks; mutated expanded content must fail normal trust validation. Cross-platform tests must cover old and compact input and decompression limits without capturing live invitations.

Existing phones require the compact-capable update to scan newly generated compact codes; older desktop QR/text codes remain valid in updated phone apps. Text import remains a compatible recovery route. Do not claim the diagnostic sample proves final fresh-pairing acceptance. Retain Android/iOS lifecycle distinctions and all remaining physical/release gates.

Rollback is a later same-signer Client build that resumes generating original QR text. Updated phones continue accepting the original format. No stored data or protocol migration is required.

## Implementation and validation record

The Go encoder and Android/iOS normalizers share three valid and thirteen malformed public vectors. Normalizers additionally test raw-input and expansion boundaries, exact signed-update verification and rejection after tampering. Behavioral red tests were observed before implementation in all three languages; Android's plain-code compatibility case already passed. The desktop renderer regression also failed against the original dense representation before being updated.

Android: 316 JVM tests in 47 suites, no failures/errors/skips; lint has zero errors and 18 existing warnings; debug assembly passes. Native Swift: 373 tests, zero failures and two existing opt-in security acceptance skips; all seven compact tests execute. Unsigned iPhoneOS and simulator app/extension builds pass. The additional Xcode package test runner compiled but could not attach to `testmanagerd.control` under SSH, including one runbook retry. This remains an infrastructure blocker; native Swift execution is recorded separately.

Independent review found one caller-boundary issue: iOS paste/file callers trimmed before the size check. They now pass raw input to the bounded normalizer, and the existing file-size limit matches the import limit. Swift additionally bounds UTF-8 bytes; the ASCII compact format has the same limit on both platforms. Android file import is deliberately unchanged because exported update files remain uncompressed. No generic binary/signature decoder or repository network path accepts the envelope. Final parser/encoder review has no outstanding security findings.

The full Windows integration gate passes: all Go packages, installer contracts, vet/build, 71 frontend tests/syntax, Desktop/direct/release contracts and mobile manifest/schema. Android Git signing-path regression and changed documentation local links pass. The native optical harness passes all 90 required frames (45 historical, 45 compact), plus six informational low-resolution observations. Its compact grid is generated from the real Go renderer and restores exactly the shared fixture. Historical grids remain unchanged. The existing IPC test's placeholder was replaced with canonical public test input because rendering now strictly rejects malformed source bundles.

Preserved evidence: `G:/codex-build-cache/mobile-egress-hosted-20261004/android-compact-qr-code24/` and `ios-compact-qr-20261005/`. The diagnostic sample's physical success and existing text-import traffic are separate from fresh compact pairing with final artifacts. Signed packaging and native Mac Client checks are pending at this source checkpoint.
