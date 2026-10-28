# Current cellular IP implementation plan

**Goal:** Show current cellular IPv4/IPv6 before rotation on Android and iOS, including before sharing starts, while retaining the completed rotation comparison.

**Design:** Reuse each platform's existing cellular-only public IP probe. The foreground dashboard observes cellular availability and checks once on entry, path change, rotation completion, or explicit Refresh IP. Do not poll, change sharing intent, log addresses, or include them in copied diagnostics. Clear the current snapshot on loss, deactivation, refresh, and rotation start. Cancel pending work and reject stale results by generation. Keep the native lifecycle exception unchanged.

**Approaches:** Reusing only the sharing service would omit addresses before Start. Continuous polling would create unnecessary traffic. Use a small foreground controller with native path observation and manual refresh instead.

## Implementation and verification

- [x] Add Kotlin and Swift controller regressions covering availability without sharing, refresh coalescing, late results after path loss/replacement, foreground suspension, rotation suppression/resumption, and failed/partial probes.
- [x] Implement generation-guarded controllers; wire Android dashboard lifecycle/network callbacks and iOS existing path/scene/rotation observations.
- [x] Display Current cellular IPv4/IPv6 and Refresh IP near rotation controls on both platforms. Keep completed before/after rows and accessible wrapping.
- [x] Update both platform entries in the parity manifest with tracked source and tests.
- [x] Run Android unit tests/debug build/lint, parity validation, native Swift tests (including warnings as errors), iPhoneOS/simulator builds, and Xcode package tests. Request focused code review and fix actionable findings.

## Verification record

- Android: `:app:testDebugUnitTest :app:assembleDebug :app:lintDebug` passed after the review fix: 322 tests, zero failures; lint reported zero errors and 18 warnings.
- iOS: `scripts/test-ios.ps1 -UseMacBuildServer -MacUser chad.jensen` passed on source commit `7dc7585`: Swift tests, warnings-as-errors tests, unsigned iPhoneOS/simulator builds, and Xcode package tests. Each suite executed 399 tests with zero failures; Secure Enclave physical-device acceptance and entitled Keychain acceptance were skipped. Subsequent changes affect only Android and this record.
- Inspected simulator-rendered synthetic fixtures for current addresses with a long IPv6 value, completed rotation comparison, and unverified addresses at 375-point width. No clipping or missing rows observed. This does not establish physical cellular or carrier acceptance.
- Focused review caught same-network Android address changes. Added `onLinkPropertiesChanged` wiring and a regression proving that changed addresses reject a late old result, ignore old-network notifications, and avoid a duplicate initial check. Android full checks passed again; follow-up review found no remaining actionable issues.
- No release publication, website change, or TestFlight assignment is part of this change.

## Review focus

- Late completion must never restore an address after loss, rotation, or app suspension.
- Stop sharing must not be required to look up an address; refresh must not start sharing.
- Wi-Fi must never supply an address, including when a cellular probe fails.
- Background Android sharing and iOS foreground sharing retain their current behavior.
- Addresses stay local to display; safe diagnostics exclude them.
