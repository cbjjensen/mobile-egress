# Android pairing QR orientation repair

Follow-up: the owner installed code 22 and confirmed that the camera still did not recognize the Mac QR. The automated evidence below remains valid, but did not establish camera acceptance. Continue with the [native camera-reader correction](2026-10-04-android-qr-camera-reader.md); code 22 is preserved as historical evidence.

## Analysis and scope

The owner cannot scan the newly installed Mac Client's pairing QR. Read-only service status confirms hosted authorization, a connected gateway, and an unredeemed invitation. The submitted screenshot preserves a 612-pixel square QR, four pixels per module and its quiet zone. ZXing 3.5.3 fails to detect it upright; the same pixels decode to identical bytes at 90, 180 and 270 degrees across five tested scales. Pure-symbol decoding also succeeds. No invitation capability, certificate or endpoint is recorded here or added to tests.

The Android scanner currently makes one orientation attempt per camera frame. Repair that optical detection limitation without changing enrollment, QR serialization, trust, gateway traffic or the installed Mac. Existing disposable interoperability material will reproduce the failure in tests. iOS continues using its native scanner; its existing orientation behavior and lifecycle remain unchanged.

## Implementation checklist

- [x] Inspect service state and reproduce the optical failure independently of enrollment.
- [x] Add a failing JVM regression using public disposable fixture material, including a frame which fails upright and succeeds rotated.
- [x] Extract bounded frame decoding and retry once at 90 degrees after initial detection failure; keep latest-frame backpressure, callback ownership and frame cleanup.
- [x] Cover ordinary success, rotated/dense input, padded camera rows and empty frames; independently review resource bounds and lifecycle integration.
- [x] Update mobile parity evidence and the sibling implementation record; no new Inevitable frontend pattern or architecture responsibility is introduced.
- [x] Run Android tests/lint, manifest validation and relevant release/signing checks. Produce a same-signer local APK update with a higher version code, preserving the previous artifact.
- [x] Record validation and remaining physical camera acceptance. Commit/push the scoped fix on the current branch; do not deploy, merge or publish a release.

## Acceptance and rollback

The failing optical fixture must pass through the production decoder with exact payload preservation in supported orientations. At most two decoding attempts run on a single retained frame; no extra camera sessions or parallel unbounded analysis are added. Missing codes keep scanning. Existing one-shot/disposal semantics and image closure remain intact. No customer QR or secret becomes test evidence. Local signed updates retain application identity and saved pairing; rollback source is independent of Mac service/gateway state. Physical confirmation must be reported separately from decoder tests.

## Validation

Investigation only at plan creation: the exact screenshot fails upright but passes all fifteen rotated/scale combinations using Android's installed ZXing version. The Mac is authorized, gateway connected and awaiting its phone. No service/pairing changes made. The current camera workaround is to turn the phone sideways while scanning a nonexpired invitation.

The public Go-generated interoperability QR reproduced three `NotFoundException` failures against the extracted original single-attempt decoder: upright, all-orientation coverage and padded non-square rows. After the bounded retry, all five new decoder cases and eight existing scan-session cases passed. The complete Android JVM suite passed 298 tests in 44 suites with no failures, errors or skips. Independent review found no actionable issue in attempt/allocation bounds, visible-row indexing, reader reset, frame closure or one-shot/disposal integration. The initial test fixture used a PNG loader unavailable on Android's unit compile classpath; it was replaced with a deterministic text module grid before the behavioral red/green run, without adding a dependency.

Release orchestration, direct-release contracts, Android Git safety, manifest validation and manifest schema checks passed. New fixture/source evidence was staged before manifest validation as required by the tracked-evidence gate. The previous code-21 signed APK was hash-verified and preserved outside build outputs before creating code 22. No connected ADB device was available, so installation and physical camera confirmation remain owner acceptance, separate from decoder tests.

The first combined lint/debug build hit a JVM native-memory allocation failure on a workstation with low free disk/virtual memory. Generated Gradle transforms were moved to the existing G-drive build cache with file/byte inventory verification and a junction preserving the original cache path. The retry passed lint (zero errors, 18 existing warnings) but debug packaging exhausted C-drive space. Generated Android build output was then moved to G with inventory verification; a task-only Gradle home redirects this project's build output and retains the original output path through a junction. The debug APK assembled successfully with one worker and a 1-GiB heap. No source, signing identity or private signing files moved; validation requirements were not relaxed.

The compiled production `QrFrameDecoder` also decoded the owner's exact upright screenshot successfully (1,515 characters), returning only a success/count diagnostic. This verifies the repair against the reported pixels without persisting the live payload or treating an expired invitation as redeemable. Physical camera acceptance still remains separate. Logs, the original APK and preserved test/lint reports are in the private local `G:/codex-build-cache/mobile-egress-hosted-20261004/android-qr-orientation/` evidence folder.

Source `ec619129e4f47b798df45aec6702a73dffdf6c26` was committed and pushed to `feature/mobile-egress-inevitable-gateway`. From that clean source, guarded `scripts/release-android.ps1` passed clean release assembly, R8, release lint and APK signature verification against the tracked original signing identity. The single-signer APK uses signature scheme v3, package `com.mobileegress.agent`, version `2.0.0`, code `22`, and is 5,314,211 bytes. SHA-256: `39392054E1E882DD493E81A6C80625B4CB16286BBB15A519F72E2D843EA302CD`.

The named immutable local copy is `G:/codex-build-cache/mobile-egress-hosted-20261004/android-qr-orientation/zfnf-mobile-egress-android-2.0.0-code22.apk`; the normal build output path also resolves to this build's canonical versioned APK. The previous code-21 APK remains separately preserved and matches its earlier hash. Install code 22 over the existing phone app without uninstalling, then show a fresh Mac invitation and scan it. No phone was available through ADB, so physical camera/install and Mac cellular-traffic acceptance remain unverified. Mac `.9`, existing pairings and gateway state were not modified. No deployment, main merge, release tag or publication occurred.
