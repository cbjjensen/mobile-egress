# Task 3B report — Apple rotation coordinator, tunnel lifecycle, and view-model integration

## Status

Implemented Task 3B on `codex/ios-agent`, based on exact starting commit `4509f01b2ff959aabd3ce76326664950f351a0a6`. The app now has a main-actor rotation coordinator, portable tunnel pause/resume transaction with an opaque intent receipt, foreground/checkpoint recovery, cellular-path and probe orchestration, finite resume failures, and `AgentViewModel` publication/actions. Ordinary tunnel start/stop, scan, relay, wire, and security behavior remains in place. No SwiftUI redesign, asset, signing, Apple-account, Mac-software, or provider change was made.

The exact source commit used for final required Mac verification was:

```text
edf9d9855981b5841952fa1773d5d14653f4284e
```

## Commits

- `068e36a` — initial coordinator/lifecycle behavior tests.
- `3af3158` — corrected only the probe gate in the test fixture; authoritative initial RED.
- `21fe792` — coordinator and portable rotation preference transaction implementation.
- `f4c7069` — app-integration structure test; authoritative app-glue RED.
- `54830f0` — `TunnelManager`, `AgentViewModel`, and scene-activation integration.
- `83f0cfa` — deterministic waiting for asynchronous resume side effects in tests.
- `cefbab4` — duplicate-start ownership regression test; authoritative self-review RED.
- `b7a23ad` — reject ineligible/duplicate requests before mutating owned tasks.
- `0f4a0b0` — explicit cellular-return-timeout coordinator coverage.
- `edf9d98` — accept direct or refined tunnel-session conformance in the legacy source-structure test.

## TDD evidence

### Initial coordinator RED

Commit `3af3158db4f3bb50084ea47ad60619ae69b4b9a1` was bundled, transferred with the project-local ignored SSH identity, checked out detached on the Mac, and verified as exact HEAD before running:

```text
swift test --filter CellularIPRotationCoordinatorTests
```

The command exited 1 as expected because production had not been added. Key errors were:

```text
cannot find 'CellularIPRotationCoordinator' in scope
cannot find type 'CellularIPRotationClock' in scope
cannot find type 'CellularIPRotationSleeping' in scope
cannot find type 'CellularIPRotationTunnelControlling' in scope
cannot find type 'TunnelRotationPreferenceSession' in scope
cannot find 'TunnelRotationPreferenceTransaction' in scope
value of type 'RotationCheckpointStoreStub' has no member 'load(at:)'
```

### App-integration RED

At exact commit `f4c706928794384fb392f9a915e2f0e817f3e770`:

```text
swift test --filter XcodeProjectStructureTests.testAppleRotationCoordinatorWiresTunnelLifecycleActivationAndSafeActions
```

The one selected test failed on 19 assertions because `TunnelManager`, `AgentViewModel`, and scene activation had not yet been wired. Those expected failures covered tunnel rotation protocol conformance, pause/resume calls, published cellular/rotation state, all requested actions, safe status copy, and foreground activation.

### Ownership regression RED

Self-review found that a second request during an active attempt reached the reducer only after the coordinator had cancelled the first attempt's timeout. The test-only exact commit `cefbab44bf90957f3b455954d3b514cd4984c58c` ran:

```text
swift test --filter CellularIPRotationCoordinatorTests.testDuplicateStartDuringActiveAttemptDoesNotCancelOwnedLossTimeout
```

Expected output:

```text
Executed 1 test, with 2 failures (0 unexpected)
Timed out waiting for coordinator state
TASK3B_RED_EXIT=1
```

Commit `b7a23ad` moved pure eligibility validation ahead of attempt-ID, generation, and task mutation. Exact-commit GREEN then reported:

```text
CellularIPRotationCoordinatorTests.testDuplicateStartDuringActiveAttemptDoesNotCancelOwnedLossTimeout: 1 test, 0 failures
CellularIPRotationCoordinatorTests: 7 tests, 0 failures
TunnelPreferenceTransactionTests: 8 tests, 0 failures
```

The later return-timeout case at `0f4a0b0` also passed as one selected test with zero failures. The final coordinator suite contains eight tests, including loss timeout, return timeout, cancellation/stale probe rejection, notification denial, early return, recovery, resume failure, and duplicate-start ownership.

### Structure compatibility RED/GREEN

The return-timeout test passed at `0f4a0b0`, after which the existing structure test reproduced one expected self-review failure:

```text
swift test --filter XcodeProjectStructureTests.testAppleManagerConsumesPortablePreferenceTransaction
Executed 1 test, with 1 failure (0 unexpected)
```

The assertion required the old literal direct conformance even though `TunnelRotationPreferenceSession` refines `TunnelPreferenceSession`. Commit `edf9d98` accepts either spelling; Swift compilation still enforces the refinement. The full final run passed all 12 project-structure tests.

## Behavior delivered

### Coordinator

- Added generic, main-actor `CellularIPRotationCoordinator` with injected clock, sleeper, public-IP probe, cellular-path observer, checkpoint store, notification cue, and tunnel controller.
- `start(holdSeconds:)`, confirmation, cancellation, and `resumeAfterActivation()` interpret Task 2 reducer effects and own pause, probe, loss-timeout, hold, return-timeout, and resume tasks.
- Attempt generations and IDs reject stale callbacks. A duplicate/ineligible request is rejected before it can cancel the current attempt's owned work.
- Every active transition saves a checkpoint. Loss/return states retain the originally scheduled deadline instead of granting a fresh 120/180-second window.
- Terminal transitions cancel owned timers, clear the checkpoint, cancel only the attempt-specific cue, and resume the tunnel exactly once. A resume error becomes finite `tunnelResumeFailed` without replay.
- Activation starts cellular observation once, republishes current state, and attempts checkpoint recovery once. Valid recovery preserves the saved deadline; expired/malformed recovery clears data, fails finite, and performs best-effort fallback resume without an activation loop.

### Tunnel lifecycle

- Added `TunnelRotationPreferenceSession`, opaque public `TunnelRotationReceipt`, and `TunnelRotationPreferenceTransaction` in `MobileEgressCore`.
- Pause loads preferences, captures prior running/on-demand intent, persists disabled on-demand state, reloads it, and only then stops the session.
- Resume restores prior on-demand intent and explicitly starts only when the receipt records prior running intent. A missing recovery receipt uses the existing ordinary start transaction as best-effort restoration.
- `TunnelManager` adopts the injected coordinator and portable transaction protocols. Its ordinary `start()` and `stop()` implementations remain unchanged.

### App integration

- `AgentViewModel` publishes `cellularHealth` and `rotationState`, computes eligibility/confirmation through Task 2's pure `CellularIPRotationAvailability`, and synchronizes enrolled/running/stream availability.
- Added request, confirm, decline, cancel, 30-second retry, foreground resume, and privacy-safe copied-status actions.
- Live construction uses Task 3A's probe, cellular path, App Group checkpoint, Apple notification center, and first-use store adapters.
- `MobileEgressAgentApp` calls foreground recovery when `scenePhase == .active`.
- Safe-copy code returns Task 2's finite `safeCopiedStatus`; no `UIPasteboard` mutation or raw error/string surface was added.
- Existing Task 2 presentation copy guides manual Control Center use. No private Settings URL or claim that the app toggles Airplane Mode exists.

## Final required Mac verification

The clean local tree was bundled, and detached Mac checkout HEAD was asserted equal to `edf9d9855981b5841952fa1773d5d14653f4284e` before each phase.

Commands:

```text
swift test
swift test -Xswiftc -warnings-as-errors
xcodebuild -list -project MobileEgressAgent.xcodeproj
xcodebuild -project MobileEgressAgent.xcodeproj -scheme MobileEgressAgent -configuration Debug -sdk iphoneos CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO CODE_SIGN_IDENTITY= build
```

Results:

```text
Test Suite 'All tests' passed
Executed 220 tests, with 2 tests skipped and 0 failures (0 unexpected)
TASK3B_FULL_SWIFT=PASSED

Test Suite 'All tests' passed
Executed 220 tests, with 2 tests skipped and 0 failures (0 unexpected)
TASK3B_WARNINGS_AS_ERRORS=PASSED

Targets: MobileEgressAgent, MobileEgressTunnelExtension
Schemes: MobileEgressAgent, MobileEgressCore
** BUILD SUCCEEDED **
TASK3B_UNSIGNED_IPHONEOS_BUILD=PASSED
```

The two skips are the existing physical-device Secure Enclave and entitled Keychain acceptance tests. `scripts/validate-mobile-feature-manifest.ps1` also passed locally. Windows Docker was unavailable, so no Windows portable result is claimed; the required Mac Swift and Xcode phases are authoritative.

## Changed files

Production/app:

- `ios/Sources/MobileEgressCore/Rotation/CellularIPRotationCoordinator.swift` (new)
- `ios/Sources/MobileEgressCore/Rotation/AppGroupCellularIPRotationCheckpointStore.swift`
- `ios/Sources/MobileEgressCore/Rotation/CellularIPRotation.swift`
- `ios/Sources/MobileEgressCore/Runtime/TunnelPreferenceTransaction.swift`
- `ios/MobileEgressAgent/TunnelManager.swift`
- `ios/MobileEgressAgent/AgentViewModel.swift`
- `ios/MobileEgressAgent/MobileEgressAgentApp.swift`

Tests:

- `ios/Tests/MobileEgressCoreTests/CellularIPRotationCoordinatorTests.swift` (new)
- `ios/Tests/MobileEgressCoreTests/CellularIPRotationCheckpointStoreTests.swift`
- `ios/Tests/MobileEgressCoreTests/TunnelPreferenceTransactionTests.swift`
- `ios/Tests/MobileEgressCoreTests/XcodeProjectStructureTests.swift`

No new app-target source file was added, so no `project.pbxproj` entry was necessary.

## Self-review

- `git diff --check 4509f01..edf9d98` reported no whitespace errors, and the verified source tree was clean.
- The changed-file inventory contains only rotation, tunnel preference, app lifecycle/view-model integration, and focused tests. `AgentDashboardView`, assets, `project.pbxproj`, plists, entitlements, relay, wire, security, extension runtime, and scanner source did not change.
- Source scans found no `prefs:root=`, Settings-opening API, `UIPasteboard`, Airplane Mode toggle claim, or tracked SSH identity.
- Ordinary `TunnelPreferenceTransaction.start/stop` code is unchanged; focused tests cover both its prior behaviors and the new rotation transaction ordering/intent restoration.
- No dependency, signing team, account, Mac configuration, provider inventory, or external system state was changed.
- Mutation review: removing coordinator generation/attempt checks, pre-mutation eligibility, either timeout, original deadline preservation, recovery-once guard, terminal checkpoint/cue cleanup, resume failure mapping, on-demand-before-stop ordering, prior-intent restoration, or a view-model action breaks focused tests or app compilation.

## Concerns and device acceptance

- The tracked standalone Xcode package-test command builds the test bundle but cannot launch it on the configured Mac because `com.apple.testmanagerd.control` is invalidated (`xcodebuild` exit 65). The identical host-service failure reproduced on exact baseline `4509f01` and exact final source commit `edf9d98`; it is not a Task 3B source regression. `swift test` and warnings-as-errors execute all 220 tests successfully.
- The unsigned Xcode build retains existing warnings about interface-orientation coverage and skipped AppIntents metadata; it exits zero and reports `BUILD SUCCEEDED`.
- Physical-device acceptance still needs to exercise real NetworkExtension preference timing, on-demand reconnection, cellular loss/return, Control Center background/foreground transitions, App Group recovery, local notification delivery/denial, and public-IP change. Those Apple side effects were intentionally covered through injected seams here without changing signing, accounts, or Mac software.
- Visual exposure of these actions belongs to the later SwiftUI task; this task wires the model and lifecycle only, as required.
