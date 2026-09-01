# Task 4 report — OLED dashboard and generated ZFNF assets

## Status

Implemented Task 4 on `codex/ios-agent`, starting from exact commit
`43caed023fb1218c28f964bfdc420f08edd4b81e`. The app now renders Task 2's pure
presentation as an accessible true-black OLED dashboard, retains Task 3B's action
boundaries, and uses deterministic iOS AppIcon/header raster outputs generated from the
checked-in selected ZFNF source. Native navigation, QR scanning, pull-to-refresh,
confirmation/alert behavior, and existing relay, wire, security, tunnel, and rotation
lifecycle semantics remain in place.

The exact source commit used for final required Mac verification was:

```text
ad0bcec065c22e0ce485ace1652209309506ae1b
```

## Commits

- `7042c69650a04111c8b1daf9cc0eb841b44dcc47` — generator, presentation, hierarchy, action, accessibility, and privacy contract tests; authoritative RED.
- `9bdd00ecb7ca277f76738327fe5c2bdccfda6820` — pure presentation countdown/cancellation state.
- `ad0bcec065c22e0ce485ace1652209309506ae1b` — deterministic iOS ZFNF assets, OLED SwiftUI dashboard, and view-model presentation/action glue.

## TDD evidence

### Generator RED

At test-only commit `7042c69650a04111c8b1daf9cc0eb841b44dcc47`, Windows PowerShell ran:

```text
& .\scripts\test-brand-assets.ps1
```

It exited nonzero before implementation because the checked-in generator did not yet
provide the fixture seam required by the test:

```text
A parameter cannot be found that matches parameter name 'RepositoryRoot'.
```

The test contract additionally requires deterministic 1024x1024 opaque iOS AppIcon and
256x256 transparent header outputs, a clean `-Check`, and named failures for stale or
missing iOS outputs.

### Pure presentation RED/GREEN

The exact test-only commit `7042c69650a04111c8b1daf9cc0eb841b44dcc47` was bundled,
transferred using the existing project-local ignored Mac identity, checked out detached
on the configured Mac, and confirmed as exact HEAD. Running the focused presentation
tests failed compilation as expected because `AgentDashboardPresentation` had no
`rotationCountdownSeconds` or `showsRotationCancellation` members.

At exact commit `9bdd00ecb7ca277f76738327fe5c2bdccfda6820`:

```text
swift test --filter "MobileEgressCoreTests.AgentDashboardPresentationTests"
Executed 18 tests, with 0 failures (0 unexpected)
```

The new cases prove a holding rotation exposes only its finite countdown/cancellation
state and does not copy stored IPv4/IPv6 values; inactive rotation exposes neither
control.

### SwiftUI structure RED/GREEN

At exact commit `9bdd00ecb7ca277f76738327fe5c2bdccfda6820`:

```text
swift test --filter "MobileEgressCoreTests.XcodeProjectStructureTests/testOledDashboardRendersPortablePresentationWithNativeAccessibleActions"
Executed 1 test, with 39 failures (0 unexpected)
```

Those expected failures covered the old root `List`, missing OLED hierarchy, missing
presentation-only rendering, action wiring, native confirmations, safe clipboard copy,
44-point controls, VoiceOver values, monospaced data, and reduced-motion handling.

At exact final source commit `ad0bcec065c22e0ce485ace1652209309506ae1b`, the focused
dashboard structure test and focused asset-catalog test each executed one test with zero
failures. The full suite later executed all 13 `XcodeProjectStructureTests` with zero
failures.

## Behavior delivered

### Deterministic ZFNF assets

- Extended `scripts/generate-brand-assets.ps1` with guarded `-RepositoryRoot` and
  `-Check` modes while preserving Android and Windows outputs.
- The real checked-in `assets/branding/zfnf-logo-source.png` now generates the tracked
  1024x1024 opaque RGB iOS AppIcon and the 256x256 RGBA SwiftUI header image.
- `-Check` regenerates all outputs under a guarded temporary directory, compares SHA-256
  hashes, and fails on missing or stale outputs without changing tracked assets.
- `scripts/test-brand-assets.ps1` tests dimensions, PNG color types, determinism, clean
  check mode, stale-header rejection, and missing-icon rejection in an isolated fixture.
- Generated raster files were written only by the generator; neither PNG was hand-edited.

Tracked iOS raster results:

```text
AppIcon  1024x1024 RGB   SHA256 206076A1BDA3DA90B4E080F4A0FF56BAB390867AB885E6C26FEFC6207306E17E
Header    256x256  RGBA  SHA256 76B23D00EA538305EF0672CC81A90D81C3887C085683E1C399566D7AA774A6E1
```

### OLED dashboard

- Replaced the root `List` with a true-black `NavigationStack`/`ScrollView` dashboard:
  ZFNF header, pairing card, separate cellular/relay health tiles, stream/upload/download
  metrics, finite-error panel, Agent action, cellular-IP rotation card, and safe-copy card.
- `AgentViewModel.dashboardPresentation` constructs Task 2's
  `AgentDashboardPresentation`; SwiftUI renders its status precedence, eligibility,
  metrics, finite errors, confirmation copy, countdown, and safe diagnostic text without
  inspecting raw VPN/provider/rotation/metric state.
- Existing view-model actions remain the only mutation boundary: scan, start/stop,
  request/confirm/decline/cancel/retry rotation, refresh, and finite error dismissal.
- Stop uses a native destructive confirmation dialog. Rotation stream disruption uses
  Task 2's native system alert presentation before `confirmRotationStart()`.
- Rotation guidance explicitly directs manual Control Center use: Airplane Mode on,
  wait for cue/countdown, then off. It states that the app never changes Airplane Mode
  and contains no private Settings deep link.
- Clipboard mutation copies exactly `presentation.safeStatusText`; no address, relay
  origin, credential, certificate, raw error, or opaque network token is surfaced.

### Accessibility and native behavior

- Preserved native navigation, scanner sheet/close control, pull-to-refresh, alerts, and
  confirmation dialogs.
- Controls have at least 44-point height; health/metric/status/countdown content has
  meaningful VoiceOver labels and values; metrics/countdown use monospaced digits.
- Text uses semantic Dynamic Type fonts and avoids clipped fixed-height text. Adaptive
  grids and `ViewThatFits` handle narrow and larger text layouts.
- OLED colors retain high contrast on black/near-black surfaces. Reduced Motion disables
  view transaction animations; the dashboard adds no motion-only affordance.

## Final verification

Fresh local Windows verification at the final source tree:

```text
& .\scripts\test-brand-assets.ps1
Brand asset generator checks passed.

& .\scripts\generate-brand-assets.ps1 -Check
Generated Android, Windows, and iOS branding assets are current.

& .\scripts\validate-mobile-feature-manifest.ps1
Mobile feature manifest validation passed.

git diff --check 43caed0..ad0bcec
exit 0, no output
```

For Apple verification, the clean tree was bundled and checked out detached at exact
HEAD `ad0bcec065c22e0ce485ace1652209309506ae1b` on the configured Mac before each phase.
No signing, account, or Mac software/configuration change was made.

```text
swift test
Test Suite 'All tests' passed
Executed 239 tests, with 2 tests skipped and 0 failures (0 unexpected)

swift test -Xswiftc -warnings-as-errors
Test Suite 'All tests' passed
Executed 239 tests, with 2 tests skipped and 0 failures (0 unexpected)

swift test --filter "MobileEgressCoreTests.XcodeProjectStructureTests/testOledDashboardRendersPortablePresentationWithNativeAccessibleActions"
Executed 1 test, with 0 failures (0 unexpected)

swift test --filter "MobileEgressCoreTests.XcodeProjectStructureTests/testXcodeProjectReferencesExpectedSourcesAndAssetCatalogs"
Executed 1 test, with 0 failures (0 unexpected)

xcodebuild -project MobileEgressAgent.xcodeproj -scheme MobileEgressAgent \
  -configuration Debug -sdk iphoneos \
  CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO CODE_SIGN_IDENTITY= build
** BUILD SUCCEEDED **
```

The unsigned iPhoneOS build compiled and embedded both `MobileEgressAgent` and
`MobileEgressTunnelExtension` and compiled the updated asset catalog.

## Changed files

Production/assets:

- `scripts/generate-brand-assets.ps1`
- `ios/Assets/AppAssets.xcassets/AppIcon.appiconset/MobileEgressAppIcon.png`
- `ios/Assets/AppAssets.xcassets/ZFNFHeader.imageset/Contents.json`
- `ios/Assets/AppAssets.xcassets/ZFNFHeader.imageset/ZFNFHeader.png`
- `ios/Sources/MobileEgressCore/Presentation/AgentDashboardPresentation.swift`
- `ios/MobileEgressAgent/AgentDashboardView.swift`
- `ios/MobileEgressAgent/AgentViewModel.swift`

Tests:

- `scripts/test-brand-assets.ps1`
- `ios/Tests/MobileEgressCoreTests/AgentDashboardPresentationTests.swift`
- `ios/Tests/MobileEgressCoreTests/XcodeProjectStructureTests.swift`

No new app-target Swift source file was added, so no `project.pbxproj` source membership
edit was required. The new header asset is discovered through the existing asset catalog.

## Self-review

- `git diff --check 43caed0..ad0bcec` is clean, and fresh generator checks left the
  source tree unchanged.
- Changed-file review is limited to the brand generator/test, generated catalog assets,
  pure presentation extension/tests, dashboard/view-model integration, and structure
  tests. Relay, wire, security, extension runtime, scanner source, tunnel manager,
  lifecycle coordinator, entitlements, plists, and signing settings are unchanged.
- Source scans found none of `localizedDescription`, `prefs:root=`, original network
  token/address fields, relay-origin exposure, or forbidden raw model status/rotation/
  metric reads in `AgentDashboardView.swift`.
- Mutating/removing the presentation-only render boundary, hierarchy, native action
  calls, confirmation surfaces, safe-copy assignment, accessibility affordances,
  generated asset declarations, or stale/missing checks breaks focused tests or app
  compilation.
- No dependency, signing identity/team, Apple account, Mac software, provider inventory,
  or external runtime state was changed.

## Concerns and device acceptance

- Windows Docker Engine was not running, so the baseline `scripts/test-ios.ps1` portable
  container phase could not start. The manifest step passed locally, and the required
  exact-Mac Swift, warnings-as-errors, structure, and unsigned Xcode phases are green.
- The two skipped Swift tests are the existing physical-device Secure Enclave and
  entitled Keychain acceptance tests.
- The unsigned build retains the pre-existing interface-orientation warning and an
  AppIntents metadata-extraction skip warning; the command exits zero with
  `BUILD SUCCEEDED`.
- Final release acceptance still needs a signed physical iPhone for visual review across
  accessibility text sizes/VoiceOver/Reduce Motion, scanner permission states, real
  NetworkExtension start/stop, Control Center cellular rotation, background recovery,
  and safe clipboard inspection. No simulator or device screenshot is claimed here.
