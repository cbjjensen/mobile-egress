# Current cellular IP implementation plan

**Goal:** Show current cellular IPv4/IPv6 before rotation on Android and iOS, including before sharing starts, while retaining the completed rotation comparison.

**Design:** Reuse each platform's existing cellular-only public IP probe. The foreground dashboard observes cellular availability and checks once on entry, path change, rotation completion, or explicit Refresh IP. Do not poll, change sharing intent, log addresses, or include them in copied diagnostics. Clear the current snapshot on loss, deactivation, refresh, and rotation start. Cancel pending work and reject stale results by generation. Keep the native lifecycle exception unchanged.

**Approaches:** Reusing only the sharing service would omit addresses before Start. Continuous polling would create unnecessary traffic. Use a small foreground controller with native path observation and manual refresh instead.

## Implementation and verification

- [ ] Add Kotlin and Swift controller regressions covering availability without sharing, refresh coalescing, late results after path loss/replacement, foreground suspension, rotation suppression/resumption, and failed/partial probes.
- [ ] Implement generation-guarded controllers; wire Android dashboard lifecycle/network callbacks and iOS existing path/scene/rotation observations.
- [ ] Display Current cellular IPv4/IPv6 and Refresh IP near rotation controls on both platforms. Keep completed before/after rows and accessible wrapping.
- [ ] Update both platform entries in the parity manifest with tracked source and tests.
- [ ] Run Android unit tests/debug build/lint, parity validation, native Swift tests (including warnings as errors), iPhoneOS/simulator builds, and Xcode package tests. Request focused code review and fix actionable findings.

## Review focus

- Late completion must never restore an address after loss, rotation, or app suspension.
- Stop sharing must not be required to look up an address; refresh must not start sharing.
- Wi-Fi must never supply an address, including when a cellular probe fails.
- Background Android sharing and iOS foreground sharing retain their current behavior.
- Addresses stay local to display; safe diagnostics exclude them.
