# Task 3A report — Apple cellular probe, path, checkpoint, notification, and logging adapters

## Status

Implemented the Task 3A Apple adapters in `MobileEgressCore` without changing the portable Task 2 reducer semantics. The work adds the cellular-only public-IP probe, bounded HTTP response parser, finite unified-log diagnostics, cellular path observer, App Group checkpoint store, and first-use local-notification cue. No app target, SwiftUI, Xcode project, relay/wire protocol, tunnel behavior, security policy, signing, Apple-account, provider inventory, or Mac software state changed.

## Commits

- `9415059ca305674e719531acf3361841a8836bf5` — test-only expected RED commit.
- `fa599ac` — initial adapter implementation.
- `de05229` — Swift 6 async test observation correction.
- `883d5f1` — require already-parsed HTTP status in finite parser failures.
- `e4f8497` — defer `UNUserNotificationCenter.current()` lookup until live use.
- `5f4f1f6f11eab5f3309e2555c7c51eb6d6ddef62` — final code commit, including cancellation-race hardening.

## TDD evidence

### Exact-commit RED

The test-only commit `9415059ca305674e719531acf3361841a8836bf5` was transferred through the project-local ignored SSH identity and checked out detached on the Mac. The checkout verified the exact commit before running:

```text
swift test --filter CellularPublicIPProbeAdapterTests
```

Expected failure evidence included:

```text
error: cannot find type 'PublicIPFamily' in scope
error: cannot find 'CellularPublicIPHTTPResponseParser' in scope
error: cannot find type 'CellularPublicIPFamilyRequesting' in scope
error: cannot find type 'CellularNetworkDiagnosticLogging' in scope
error: cannot find 'AppGroupCellularIPRotationCheckpointStore' in scope
error: cannot find type 'CellularIPRotationNotificationCenter' in scope
error: cannot find type 'CellularPathMonitoring' in scope
error: fatalError
```

The failures were caused by the missing production interfaces and adapters, not by existing tests.

### GREEN iterations

At `fa599ac`, all new production sources compiled on macOS. Swift 6 then rejected `await` expressions inside XCTest autoclosures in two new test files. Commit `de05229` evaluated those actor observations before assertions without changing tested behavior.

At `de05229`, 12 executed probe/path/checkpoint tests compiled; 11 behaviors passed and one table-driven parser test reported six expected-value mismatches because the parser retained the already-available HTTP 200 status. Commit `883d5f1` corrected the expectations to require the status, consistent with the brief's permitted finite HTTP-status field.

The first direct notification run at `883d5f1` reproduced a macOS XCTest-host exception from eager evaluation of `UNUserNotificationCenter.current()` outside an application bundle. The stack terminated at the adapter's default parameter. Commit `e4f8497` made the system-center lookup lazy, so construction remains side-effect free and live app calls still use the Apple singleton.

Focused GREEN on the final code reported:

```text
CellularPublicIPProbeAdapterTests: 6 tests, 0 failures
CellularPathObserverTests: 2 tests, 0 failures
CellularIPRotationCheckpointStoreTests: 4 tests, 0 failures
CellularIPRotationNotificationCueTests: 5 tests, 0 failures
```

## Final Mac verification

The Mac detached checkout verified exact commit:

```text
5f4f1f6f11eab5f3309e2555c7c51eb6d6ddef62
```

Focused commands:

```text
swift test --filter CellularPublicIPProbeAdapterTests
swift test --filter CellularPathObserverTests
swift test --filter CellularIPRotationCheckpointStoreTests
swift test --filter Notification
```

Full commands:

```text
swift test
swift test -Xswiftc -warnings-as-errors
```

Each full command reported:

```text
Test Suite 'All tests' passed
Executed 201 tests, with 2 tests skipped and 0 failures (0 unexpected)
```

The two skips are the existing entitled Keychain and physical-device Secure Enclave acceptance tests. Windows Docker was unavailable (`Docker Desktop Linux engine` returned HTTP 500), so no Windows portable result is claimed; the required Mac Swift and warnings-as-errors suites are the authoritative results for this Apple-adapter task.

## Delivered behavior

### Public-IP probe and safe logging

- Added portable `CellularPublicIPProbing.probe() async -> PublicIPSnapshot`.
- Added live `CellularPublicIPProbe` with concurrent IPv4/IPv6 child tasks and independent family results.
- Uses exact `api.ipify.org` and `api6.ipify.org` HTTPS requests, port 443, system TLS, `.cellular` required, Wi-Fi/wired Ethernet prohibited, proxy preference disabled where supported, eight-second family timers, and a 128-byte body limit.
- Task cancellation reaches both child requests and cancels each live `NWConnection`; an already-cancelled-start race resumes rather than stranding its continuation.
- The HTTP/1.1 parser requires one valid `Content-Length`, a complete exact-length body, 2xx status, bounded headers/body, and strict expected-family literals. It rejects chunking, duplicate/missing lengths, trailing/truncated data, oversized responses, wrong-family values, malformed values, and ambiguous status/header syntax.
- Added finite probe/relay diagnostic component, family, class, and optional status types. `AppleUnifiedNetworkDiagnosticLogger` can receive only that finite structure, so its API cannot accept raw errors, addresses, bodies, hostnames, URLs, tokens, origins, or certificate material.

### Cellular path observation

- Added portable `CellularPathObserving` and live `CellularPathObserver`.
- The live backend creates `NWPathMonitor(requiredInterfaceType: .cellular)` and publishes only satisfied cellular availability.
- Start/cancel and delivery are serialized with a recursive lock, allowing callback-initiated cancellation while guaranteeing that cancellation detaches both callback layers and prevents later delivery.
- The observer has no relay-state dependency.

### App Group checkpoint persistence

- Added `CellularIPRotationCheckpointStoring` and `AppGroupCellularIPRotationCheckpointStore`.
- App Group container resolution failure maps to finite `containerUnavailable`.
- Saves use `Data.write(..., .atomic)` to replace the one checkpoint file.
- Loads require an active checkpoint, the expected attempt ID, a non-future save time, and an age under five minutes; exact five-minute expiry is rejected and removed.
- Awaiting-loss and awaiting-return states require their original timeout deadline, so legacy timing-free data cannot silently reset Task 2's timeout window.
- Malformed, inactive, unexpected-deadline, wrong-attempt, expired, read, and write outcomes are finite. The store has no logging or clipboard surface.
- `clear()` removes the checkpoint terminally and is idempotent when absent.

### First-use notification cue

- Added injected notification-center and first-use-store protocols plus actor-isolated `CellularIPRotationNotificationCue`.
- Permission is requested only when the first rotation use observes `.notDetermined`; the first-use flag is set before requesting so denial or error cannot cause repeated prompts.
- Granted authorization schedules the exact `Turn Airplane Mode off` cue for the supplied hold deadline.
- Denial, authorization error, and scheduling error return finite non-throwing results.
- Cancellation derives one attempt-specific identifier and removes only that pending request.
- The Apple notification center is resolved lazily, keeping ordinary construction safe in Mac unit tests and avoiding permission side effects.

## Files

Production:

- `ios/Sources/MobileEgressCore/Network/SafeNetworkDiagnostics.swift`
- `ios/Sources/MobileEgressCore/Network/CellularPublicIPHTTPResponseParser.swift`
- `ios/Sources/MobileEgressCore/Network/CellularPublicIPProbe.swift`
- `ios/Sources/MobileEgressCore/Network/CellularPathObserver.swift`
- `ios/Sources/MobileEgressCore/Rotation/AppGroupCellularIPRotationCheckpointStore.swift`
- `ios/Sources/MobileEgressCore/Rotation/CellularIPRotationNotificationCue.swift`

Tests:

- `ios/Tests/MobileEgressCoreTests/CellularPublicIPProbeAdapterTests.swift`
- `ios/Tests/MobileEgressCoreTests/CellularPathObserverTests.swift`
- `ios/Tests/MobileEgressCoreTests/CellularIPRotationCheckpointStoreTests.swift`
- `ios/Tests/MobileEgressCoreTests/CellularIPRotationNotificationCueTests.swift`

## Self-review

- `git diff --check 0d3bc1e..5f4f1f6` reported no whitespace errors.
- The changed-file list contains only the six permitted `MobileEgressCore` source files and four focused test files; no app target, SwiftUI, `TunnelManager`, `AgentViewModel`, Xcode project, plist, entitlement, relay, wire, or security implementation changed.
- No dependency was added.
- Source scans found no exception-message interpolation, `localizedDescription`, raw-error stringification, print/NSLog call, checkpoint logging, or diagnostic field capable of carrying an address/body/token/origin/certificate.
- Tests use injected actors/stores/monitors/requesters and do not perform real cellular, VPN, notification-permission, or App Group entitlement operations.
- Mutation review: changing endpoint family, interface constraints, timeout/body bound, family isolation, cancellation propagation, HTTP acceptance, finite relay mapping, path detach behavior, attempt/expiry/timing validation, first-use prompting, cue text/deadline, or attempt-scoped removal breaks at least one focused test.

## Remaining acceptance concerns

- Real cellular routing, DNS family resolution, system-TLS exchange behavior, App Group entitlement access, notification delivery, and physical-device cancellation remain device acceptance items; this task intentionally proves their configuration and injected boundaries without causing those side effects.
- Task 3B still owns coordinator/lifecycle integration, tunnel pause/resume, foreground recovery, and app publication. These adapters do not alter those behaviors preemptively.

## Round 1/5 review remediation — 2026-09-01

### Status and commits

All three round-one findings are fixed and verified on the exact committed Mac checkout.

- `31a01b0` — initial test-only race/grammar regression fixtures.
- `91f19c333c2f38050ff35dc132e8e79098545f47` — test-only Swift 6 harness correction and authoritative expected RED commit.
- `303499818cf5490de913a7471f38e6f26ddcd0cc` — production fix for notification generations, byte-level HTTP parsing, and synchronized path callbacks.

The first test-only commit deliberately demonstrated that the public path callback did not conform to `Sendable`, but also exposed two fixture-only Swift 6 errors: an implicit actor return and test tasks capturing `XCTestCase.self`. The second test-only commit corrected only those fixtures and was transferred as a fresh bundle. The detached Mac checkout printed and verified exact HEAD `91f19c333c2f38050ff35dc132e8e79098545f47` before the authoritative behavioral RED.

### Exact-commit RED

Focused commands:

```text
swift test --filter CellularIPRotationNotificationCueTests
swift test --filter CellularPublicIPProbeAdapterTests
swift test --filter CellularPathObserverTests
```

Expected failures at `91f19c3`:

- Notification: 8 executed, 3 failing tests / 7 assertions. Cancellation during authorization-status lookup, permission request, and notification add all returned `scheduled`; the first two left stale requests and the add race lacked the second removal needed to undo its late commit.
- Parser/probe: 8 executed, 2 failing table tests / 8 assertions. Repeated or missing status separators, absent reason phrase, tab/NUL controls in the reason phrase, and controls in ignored header values were accepted.
- Path: 4 executed, 1 failure. Runtime reflection of the public callback type did not contain `@Sendable`.

These failures matched the review findings; existing cases in the same focused suites remained green.

### GREEN implementation

Notification scheduling now assigns an actor-isolated generation to each active attempt. Cancellation marks only that attempt's active generation before removing its identifier. Scheduling rechecks the generation after authorization-status lookup, permission request (including its error path), and notification add. A cancellation observed after a late successful add performs a second attempt-specific removal before returning the new finite `cancelled` result. Finished generations are removed without disturbing a newer generation.

The HTTP parser no longer converts headers to `String`. It scans only the bounded header window, splits literal CRLF at the byte level, and requires the exact `HTTP/1.1 SP 3DIGIT SP reason-phrase` shape with a 100–599 code, one separator at each boundary, a non-empty visible reason phrase, and no control/NUL/DEL bytes. Every header name is checked as an ASCII token and every header value is control-checked before recognized fields are interpreted. The prior single-`Content-Length`, no-transfer-encoding, exact-body, 128-byte, 2xx, and strict-address checks remain intact.

The public cellular availability handler is now `@Sendable`. The live `NWCellularPathMonitor` stores and loads its callback behind an `NSLock`; the existing outer observer lock continues to serialize delivery with cancellation, so an update copied immediately before cancellation is suppressed after cancellation returns. Repeated concurrent cancellation remains idempotent.

### Exact-commit GREEN and full verification

The Mac detached checkout printed and verified exact HEAD `303499818cf5490de913a7471f38e6f26ddcd0cc`.

Focused results:

```text
CellularIPRotationNotificationCueTests: 8 tests, 0 failures
CellularPublicIPProbeAdapterTests: 8 tests, 0 failures
CellularPathObserverTests: 4 tests, 0 failures
```

Full commands:

```text
swift test
swift test -Xswiftc -warnings-as-errors
```

Both commands exited zero and reported 208 tests executed, 2 existing device/entitlement tests skipped, and 0 failures. No compiler warnings were accepted by the warnings-as-errors run.

### Round-one files

Production:

- `ios/Sources/MobileEgressCore/Rotation/CellularIPRotationNotificationCue.swift`
- `ios/Sources/MobileEgressCore/Network/CellularPublicIPHTTPResponseParser.swift`
- `ios/Sources/MobileEgressCore/Network/CellularPathObserver.swift`

Tests:

- `ios/Tests/MobileEgressCoreTests/CellularIPRotationNotificationCueTests.swift`
- `ios/Tests/MobileEgressCoreTests/CellularPublicIPProbeAdapterTests.swift`
- `ios/Tests/MobileEgressCoreTests/CellularPathObserverTests.swift`

### Round-one self-review

- `git diff --check 7e9adc4..3034998` reported no whitespace errors.
- The round-one changed-file list is exactly the three permitted `MobileEgressCore` sources and their three focused test files. No app target, SwiftUI, Xcode project, `TunnelManager`, `AgentViewModel`, relay, wire, security, signing, account, or Mac configuration file changed.
- The notification state is keyed by `attemptID` and generation; every removal still derives only `com.mobileegress.agent.rotation.<attemptID>`. The added `cancelled` value is a local finite adapter result and is not part of relay or wire encoding.
- The parser allocates only the at-most-8-KiB header slice and the already-bounded body string. It does not log or expose status text, header values, response bodies, addresses, endpoints, or raw errors.
- The live path wrapper never reads or writes its callback without its private lock. The outer recursive lock still permits callback-initiated cancellation while ensuring cancellation cannot return before an already-delivering user callback completes.
- Focused mutation review: removing any post-await generation check, either late-add removal, status separator/control check, header-value control check, `@Sendable`, callback lock, or idempotent cancellation guard breaks a new regression test or Swift 6 compilation contract.
