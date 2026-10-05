# Android camera QR reader correction

## Evidence and decision

The owner installed Android code 22 and reports that scanning the Mac QR leaves the camera open with no result. The same phone previously paired a Windows Client. The new screenshot passes the compiled decoder at native resolution; Mac status remains authorized, gateway connected and unpaired. The rotation repair therefore did not establish physical scanning reliability.

Camera-sized representations reproduce content-dependent failures in ZXing Java 3.5.3. Center crops, extra rotations and the multi-reader do not reliably recover them. Padded rows behave identically to contiguous rows; CameraX already enables continuous autofocus. The current CameraX configuration leaves analysis at its 640x480 default strategy and displays a cropped FILL_CENTER preview. Increasing resolution alone still produces detector failures.

Compare the same frames with the bundled ZXing-C++ 3.1.1 engine, using explicit matching QR-only options. Java code 22 passes 12/26; C++ passes 23/26. At 1280x960, C++ passes all six QR sizes (384-864 pixels), compared with two for Java. Remaining low-resolution failures include codes below two pixels per module. The public disposable interoperability fixture also passes in all four orientations. These are image/engine tests, not evidence from the phone camera. No live invitation or screenshot will become a tracked fixture.

Replace the Android detector with the tested bundled C++ engine, request bounded 1280x960 analysis with a compatible lower-resolution fallback, and display the complete preview. Retain the existing CameraX lifecycle, latest-frame backpressure, single worker, protected one-shot callbacks and frame closure. No QR format, trust, enrollment, gateway or mobile runtime change is needed. The library runs locally; no QR upload or model download is introduced. iOS retains its existing VisionKit scanner and approved lifecycle exception.

References: [CameraX analysis resolution](https://developer.android.com/reference/androidx/camera/core/ImageAnalysis.Builder), [ZXing-C++ Android wrapper](https://github.com/zxing-cpp/zxing-cpp/blob/v3.1.1/wrappers/android/README.md), [matching detector test package](https://pypi.org/project/zxing-cpp/3.1.1/).

## Implementation checklist

- [x] Preserve code-22 artifact and diagnose the confirmed camera failure without resetting pairing.
- [x] Add tracked disposable sensor-frame regression coverage and a repeatable native detector comparison; retain the original Java failure as test-only evidence.
- [x] Integrate the pinned offline detector with explicit tested options, bounded resolution and uncropped preview. Preserve existing app toolchain/CameraX versions where binary-compatible; verify any required dependency changes before broadening scope.
- [x] Verify single-frame ownership, exactly-once callbacks, disposal, no-code continuation, decoder failure and missing-native-library behavior. Remove the unused Java decoder from runtime.
- [x] Update mobile parity evidence, Android guidance and the sibling pilot record. No Inevitable frontend pattern or accepted infrastructure responsibility changes.
- [x] Run full Android tests/lint/build, native detector regression, manifest/schema and release checks plus independent review.
- [x] Build and verify a same-signer local code-23 APK from committed source; preserve previous artifacts. Record exact source/hash and physical camera acceptance still required.
- [x] Commit/push only the current feature/docs branches. No deployment, main merge or release publication.

## Acceptance, scope and rollback

Tests must demonstrate the actual detector regression, not merely assert the library choice. Require exact decoded bytes for public fixtures across camera sizes, offsets, rotations and padded rows at the intended analysis resolution. Keep device-ABI/JNI and physical optical acceptance distinct from portable native-core tests. Do not claim that synthetic frames prove the owner's camera works. No phone is attached to ADB and no Android emulator is installed in the existing environment.

Cap analysis dimensions through supported resolution selection; retain one synchronous decode in flight and close each ImageProxy on every outcome. A failed native library initialization must report scanner unavailable rather than crash. Existing saved pairings, one-use invitation security, ten-Client limit, service behavior and iOS lifecycle remain unchanged. Rollback is a signed subsequent build with preserved app identity; do not uninstall the phone app or remove its data.

## Validation

Investigation and engine comparison are complete as recorded above. The tracked native-core harness passes all 45 required public-fixture cases: six sizes in four orientations, four off-center placements, fractional resampling/mild blur, four padded-row views and the exact dependency-free 480-pixel frame retained in the JVM regression. Three undersampled cases are recorded as observations rather than guarantees. Independent comparison of the same public 1280x960 frames also confirms Java code 22 misses the centered 576- and 672-pixel codes that the replacement reads exactly. No customer QR is required to reproduce that limitation.

Release orchestration, direct release contracts, Android Git safety, mobile manifest and schema regression checks pass. The native Android wrapper compiles against the existing Kotlin 2.2.10 and CameraX 1.4.2; scoped transitive exclusions preserve those versions. License and attribution are bundled with the app. The callback path also exposed a stale-status presentation bug: scan errors changed state/tone but the pairing header kept its old text. This correction must display a generic actionable scan error without changing saved pairing state or surfacing exception details.

Implementation, full Android and signed artifact checks follow. Private evaluation material stays under the restricted local `G:/codex-build-cache/mobile-egress-hosted-20261004/android-qr-camera-evaluation/`; only public disposable fixture material may enter Git. Code-23 reports/artifacts are preserved separately in `G:/codex-build-cache/mobile-egress-hosted-20261004/android-qr-camera-code23/`.

Behavioral RED reproduced five expected failures in the focused suite: missing-native initialization/read failures, decoder options, oversized resolution acceptance, and stale presentation text. The preceding compile-only attempt raced a source/test edit and was rerun before drawing behavioral conclusions. Independent final source review found no actionable defect in frame bounds/ownership, null/no-code continuation, error sanitization, one-shot/disposed callbacks, CameraX API compatibility or JNI keep rules. Device execution remains separate.

The full Android JVM suite passes 309 tests across 46 suites, with zero failures, errors or skips. Debug lint passes with zero errors and 18 existing warnings; debug APK assembly passes. The pairing card renders complete actionable scanner error text below its compact status badge, and retry/cancel restores the saved pairing status. Source-only test/lint reports are preserved in `android-qr-camera-evaluation/code23-green-validation/` before the clean signed build.

## Signed local artifact and remaining acceptance

From clean source `8556040bd2d6a5cfe75758f3723b1141f286234d`, guarded `scripts/release-android.ps1` passed clean release assembly, R8, release lint and exact original-signer verification. Package `com.mobileegress.agent`, version `2.0.0`, code `23`, minimum SDK 29 and target SDK 35 were independently confirmed. The APK has one signer using scheme v3. All four native ABIs are present, with 16-KiB ELF load alignment; `zipalign -c -P 16 -v 4` passes. The final R8 configuration/mapping preserves JNI names, and license/attribution assets are included.

Immutable local file: `G:/codex-build-cache/mobile-egress-hosted-20261004/android-qr-camera-code23/zfnf-mobile-egress-android-2.0.0-code23.apk`, 11,864,968 bytes. SHA-256: `0792B2C42A9A547A6B9633FF9B377CC44D20A20A91FAE52D1C4147E3F40430EC`. Code 21 and 22 artifacts remain intact. Install this update over the existing app, without uninstalling or clearing saved Clients, then scan a fresh nonexpired Mac invitation.

Physical Android JNI loading, camera scanning and Mac cellular traffic remain unverified until the owner tests the phone; no device or emulator is available locally. Portable core tests and packaged-library checks do not close that gate. Mac `.9`, invitation/trust format, gateway runtime, iOS behavior and existing pairings were not changed. The only scope addition was the directly related generic scanner-error presentation repair. No deployment, main merge or public release occurred.
