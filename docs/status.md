# Current status

Current development is the [Inevitable hosted connectivity plan](superpowers/plans/2026-10-03-inevitable-hosted-connectivity.md) on `feature/mobile-egress-inevitable-gateway` in both repositories. New setup defaults to hosted outbound connections; existing direct installations remain direct until explicitly switched. Inevitable uses a separate default-off process on existing gateway hosts and does not meter Mobile Egress traffic. See [hosted acceptance](hosted-acceptance.md) for current evidence and blockers. This work does not deploy or publish.

## Historical direct baseline

Mobile Egress 2 direct-mode source is implemented on `main` under the [approved plan](superpowers/plans/2026-10-03-direct-client-phone.md). It replaces the relay, central controller, AWS management and Funnel runtime with a locally managed Windows/Mac Client and a phone-initiated connection. The phone can save ten Clients; every Client has one paired phone.

Android retains its owner-started foreground service. iOS serves only while the main app is active, with default-on **Keep screen awake while sharing**. Manual locking or leaving the app pauses traffic. This lifecycle difference is an explicit approved exception in the mobile manifest.

## Software validation

- Windows Go, Client UI, installer migration and release-contract checks passed.
- Android: 279 unit tests, zero failures; lint has zero errors and 18 warnings; debug APK built.
- iOS: 349 tests executed with zero failures and two hardware-dependent skips; warnings-as-errors, Xcode package tests, unsigned iPhoneOS/Simulator app and compatibility-extension builds passed.
- Native Apple Silicon Go tests, vet, race checks and unsigned Client builds passed.
- Shared Go-generated enrollment and signed-update fixtures passed Android and Apple Security verification. Manifest/schema tests passed.

The [acceptance record](direct-acceptance.md) distinguishes these automated results from hardware, signing and installed-service checks. Follow-up review corrections are complete for Android durable registry writes and Stop visibility, equivalent endpoint authorities, iOS rotation recovery/retry, and actionable per-Client authentication recovery. Regression coverage also closes stale lifecycle/callback races and keeps Android storage durability work off unchanged traffic-status refreshes. Final focused review found no additional defect in these corrections.

## Release blockers

Direct physical acceptance has not run. No Android phone was connected to ADB; the Mac inventory found no attached iPhone/iPad and no Apple Development/iPhone Developer signing identity. Public-endpoint cellular tests still need an appropriate lab environment.

Signed Windows/Mac installation, upgrade/repair, native secure-storage access and boot/logout acceptance remain pending. Physical HTTP/CONNECT/SOCKS, iPhone no-touch keep-awake and lock/background behavior, Android background recovery, and sustained one/ten-Client performance and thermal measurements are also pending.

No 2.x release was signed, published or installed by this implementation. No new throughput rate is claimed. Historical 1.x downloads and relay measurements remain historical and are not compatible direct-mode fallbacks. Required unexecuted acceptance checks block describing downloads as release-ready.
