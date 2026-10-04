# Release, deployment, and acceptance

Mobile Egress 2 distributes the workload Client and compatible phone Agents. There is no controller, relay, AWS bootstrap, Tailscale, or Funnel build in the new product. Historical published 1.x tags and assets remain immutable and are incompatible with direct pairing.

## Artifacts and scope

| Component | Primary artifact | Required validation |
|---|---|---|
| Windows x64 Client | `MobileEgressClientSetup.exe` | Established Authenticode identity, timestamp, exact embedded payload, service upgrade/repair |
| Apple Silicon Mac Client | `mobile-egress-client-macos-<version>-arm64.pkg` | Established Developer ID identities, notarization, staple, package and daemon checks |
| Android Agent | Versioned signed APK | Established APK signing certificate and increasing versionCode |
| iOS Agent | Compatible signed app/TestFlight build | Authorized app/cleanup-extension profiles, device acceptance |

The guarded `Desktop` scope means the coupled Windows/Mac **Clients**. `Windows,Android` is the non-Apple lane; iOS signing/distribution remains separate. Only compatible same-major builds may supply download links. An absent 2.x phone release must remain unavailable, never link to a 1.x fallback. The primary Windows artifact is not a renamed controller installer or a raw EC2 executable.

## Source checks and release commands

Run the source gates before release preparation:

```powershell
& .\scripts\test-all.ps1 -Components Windows,Android
& .\scripts\test-ios.ps1 -UseMacBuildServer -MacHost '<host>' -MacUser '<user>'
```

These include manifest validation, Client frontend behavior, Go tests/vet/build, release contracts, and the selected mobile checks. Native macOS tests and signed physical acceptance are separate gates.

Prepare only from the intended clean `main` commit. The release scripts validate the source origin, component scope, tracked signing identities, exact source bundle and artifact hashes. Mac verification records are private release evidence, not GitHub assets. Preserve their matching source commit, package hash, signature and notarization results.

```powershell
& .\scripts\release-all.ps1 -ReleaseVersion '2.0.0' -Components Desktop,Android
```

This command builds/signs and can freeze a local release tag; it is not a read-only check. Publication through `-Publish` additionally changes GitHub and source/tag state. Do not execute it merely to inspect readiness. Never overwrite a frozen tag or published asset. Verify the exact downloaded bytes after publication.

## Acceptance before stable downloads

Complete [the physical record](templates/physical-acceptance-record.md), including both workload platforms, both phone platforms, fresh pairing/migration, proxy traffic, security/recovery, native service boot/logout, signed upgrades and repair. The personal relay must be offline during direct traffic tests; AWS credentials and Tailscale must be absent from the test dependency chain.

The iPhone requires a signed physical keep-awake test beyond a short auto-lock interval without touches, followed by preference-off, Stop, manual-lock and foreground-return cases. Android needs background and cellular recovery tests. Both need sustained single- and ten-Client measurements.

A passing unit, simulator, unsigned build or loopback test does not satisfy those gates. Required FAIL, NOT RUN or PENDING entries block stable promotion. See [current evidence](direct-acceptance.md); no 2.0 download is release-ready until the applicable signed physical record is accepted.
