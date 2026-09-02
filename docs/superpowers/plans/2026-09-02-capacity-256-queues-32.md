# Mobile Egress Capacity Relaxation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow one authenticated Client to use all 256 Agent streams and expand every retained stream-data mailbox to 32 frames while enforcing separate 8,192-frame/64-MiB directional budgets.

**Architecture:** Keep protocol v1 and the existing relay writer, Android selector, iOS `NWConnection`, and Windows synchronous outbound paths. Add exact reservation objects around retained data, share Agent→Clients capacity across relay Client mailboxes, and centralize the Go numeric contract while preserving platform-native implementations and stream-local data saturation.

**Tech Stack:** Go 1.26, Gorilla WebSocket, Kotlin/JVM with Android `SocketChannel`/`Selector`, Swift 6 with Network.framework, PowerShell repository gates

**Spec:** `docs/superpowers/specs/2026-09-02-capacity-256-queues-32-design.md`

## Global Constraints

- Work only on `codex/capacity-256-queues-32`, created from clean `origin/main` in the existing worktree.
- Complete and commit both planning documents as `docs: plan 256-stream queue expansion` before modifying any code or tests.
- Client admission is 256; Agent-wide admission remains 256; one Client may consume all Agent slots.
- Every retained data lane is bounded at 32 frames per stream, 8,192 frames aggregate, and 64 MiB aggregate. The two directions have separate budgets.
- Accounting includes queued and in-flight data until completion or cancellation, with exact idempotent refunds on every terminal path.
- Data saturation closes only the contributing stream. Required-control saturation or writer failure closes the affected session.
- Preserve protocol v1, authentication, sealed configuration, first-come admission, one active Agent, ten Client identities, existing error codes, the 30-second connect/open timeout, and five-minute idle timeout.
- Preserve 16 KiB preferred outbound chunks and acceptance of protocol-valid `data` frames through 32 KiB.
- Preserve Windows' two retained idle HTTP streams, 64 KiB pre-open guards, and synchronously backpressured outbound writer.
- Preserve Android's selector architecture and iOS asynchronous `NWConnection`.
- Do not change versions, merge, push, publish, or run load, soak, benchmark, profiling, physical-device, authenticated-harness, 15-minute-hold, or real 256-connection tests.
- Historical documents under `docs/superpowers/plans/` and `docs/superpowers/specs/` are immutable except for the two files created for this change.
- Every production behavior change follows red-green-refactor: add a focused failing test, observe the intended failure, implement the minimum change, and rerun the focused test.

---

### Task 1: Establish the shared Go contract and relay data budgets

**Files:**
- Create: `internal/capacity/capacity.go`
- Create: `internal/capacity/capacity_test.go`
- Modify: `relay/internal/service/service.go`
- Modify: `relay/internal/service/session.go`
- Modify: `relay/internal/service/outbound_mailbox.go`
- Modify: `relay/internal/service/outbound_mailbox_test.go`
- Modify: `relay/internal/service/session_test.go`
- Modify: `relay/internal/service/session_writer_test.go`
- Modify: `relay/internal/service/session_race_test.go` only if an existing deterministic race fixture needs the new completion hook

**Interfaces:**
- Produces Go constants `capacity.ClientMaxConcurrentStreams`, `capacity.AgentMaxConcurrentStreams`, `capacity.DataFramesPerStream`, `capacity.DataFramesPerLane`, `capacity.DataBytesPerLane`, `capacity.ControlFramesPerSession`, and `capacity.StreamTombstones`.
- Produces a relay-local shared `outboundDataBudget` whose `tryReserve(byteCount int) (*outboundDataReservation, bool)` charges one frame plus exact bytes and whose reservation `release()` is idempotent.
- Produces relay mailbox items that retain their reservation after dequeue and release it only when the session writer finishes, cancellation discards the item, or teardown closes the mailbox.
- Later Windows work consumes the shared Go constants, not the relay-local budget implementation.

- [ ] **Step 1: Add failing shared-contract and admission tests.**

Add table assertions in `internal/capacity/capacity_test.go` and update relay admission tests so the 256th stream is accepted and the 257th receives the existing limit error. Use in-memory service/session fixtures; do not create 256 TCP destinations.

```go
func TestProductionCapacityContract(t *testing.T) {
    if capacity.ClientMaxConcurrentStreams != 256 || capacity.AgentMaxConcurrentStreams != 256 {
        t.Fatal("stream contract drifted")
    }
    if capacity.DataFramesPerStream != 32 || capacity.DataFramesPerLane != 8192 || capacity.DataBytesPerLane != 64<<20 {
        t.Fatal("data-lane contract drifted")
    }
}
```

- [ ] **Step 2: Run the focused tests and observe the 32-stream/current-constant failure.**

Run: `go test ./internal/capacity ./relay/internal/service -run 'TestProductionCapacityContract|Admission|StreamLimit' -count=1`

Expected: FAIL because `internal/capacity` and the 256-per-Client contract do not exist yet.

- [ ] **Step 3: Add the canonical constants and wire relay admission/tombstones to them.**

Create the exact constants from the spec. Retain `maxClientStreams`/`maxAgentStreams` injection in relay tests, but make production defaults 256/256 and the production tombstone bound 1,024.

```go
const (
    ClientMaxConcurrentStreams = 256
    AgentMaxConcurrentStreams  = 256
    DataFramesPerStream        = 32
    DataFramesPerLane          = 8_192
    DataBytesPerLane           = 64 << 20
    ControlFramesPerSession    = 512
    StreamTombstones           = 1_024
)
```

- [ ] **Step 4: Add failing mailbox tests for all three data limits and lifetime accounting.**

Construct mailboxes with injected small budgets and assert: frame 32 succeeds/frame 33 saturates one stream; aggregate frame N+1 fails; aggregate byte exact-boundary succeeds/one byte beyond fails; a polled item remains charged; writer completion refunds; discard and close refund; double completion does not underflow; two Client mailboxes contend for one shared budget; controls retain priority; data remains round-robin.

```go
func TestOutboundMailboxRetainsReservationUntilCompletion(t *testing.T) { /* 1-frame injected budget */ }
func TestClientMailboxesShareAgentToClientsBudget(t *testing.T) { /* two mailboxes, one budget */ }
func TestOutboundMailboxSaturationIsStreamLocal(t *testing.T) { /* peer stream remains writable */ }
```

- [ ] **Step 5: Run the mailbox tests and observe failures caused by two-frame queues and dequeue-time refunds.**

Run: `go test ./relay/internal/service -run 'OutboundMailbox|SharedAgentToClients|Reservation|Saturation' -count=1`

Expected: FAIL at the new per-stream, aggregate-byte, shared-budget, and in-flight assertions.

- [ ] **Step 6: Implement exact relay reservations without holding locks during network writes.**

Wrap data envelopes in a mailbox item carrying a reservation. Charge `len(envelope.Payload)` for relay data, preserve 512 independent controls per session, pass one budget to the Agent mailbox and one shared budget to all Client mailboxes, and release after each writer attempt. Release queued items during `discardStreamData`/`close`; never invoke WebSocket writes under the service, mailbox, or budget mutex.

```go
type outboundDataReservation struct {
    budget *outboundDataBudget
    bytes  int
    once   sync.Once
}

func (reservation *outboundDataReservation) release() {
    reservation.once.Do(func() { reservation.budget.refund(1, reservation.bytes) })
}
```

- [ ] **Step 7: Prove stream/session failure policy and writer cleanup.**

Update deterministic writer tests so per-stream/frame/byte saturation emits the existing stream-local `agent_unavailable` behavior, required-control saturation still closes one session, writer failure refunds its in-flight item before session teardown, and closing a Client returns its share for another Client.

Run: `go test ./relay/internal/service -run 'Writer|Saturation|Teardown|RoundRobin|ControlPriority' -count=1`

Expected: PASS.

- [ ] **Step 8: Run the complete focused relay suite.**

Run: `go test ./internal/capacity ./relay/... -count=1`

Expected: PASS. Do not run race stress loops or load tests; ordinary unit/component tests are sufficient for this task.

- [ ] **Step 9: Commit the relay task.**

```powershell
git add internal/capacity relay/internal/service
git commit -m "feat: expand relay data lane capacity"
```

---

### Task 2: Expand the Windows combined session and inbound mailbox

**Files:**
- Modify: `windows-client/internal/relayclient/session.go`
- Modify: `windows-client/internal/relayclient/session_test.go`
- Modify: `windows-client/internal/httpconnect/server_test.go`
- Modify: `windows-client/internal/socks/server_test.go`

**Interfaces:**
- Consumes `capacity.ClientMaxConcurrentStreams`, `capacity.DataFramesPerStream`, `capacity.DataFramesPerLane`, `capacity.DataBytesPerLane`, and `capacity.StreamTombstones`.
- Preserves exported `relayclient.MaxConcurrentStreams` as `const MaxConcurrentStreams = capacity.ClientMaxConcurrentStreams` so HTTP/CONNECT and SOCKS share one relay-session gate.
- Produces a session-owned inbound budget and per-frame idempotent reservation retained by `relayStream` until the complete decoded payload is read or discarded.

- [ ] **Step 1: Add failing tests for 256/257 combined admission and 1,024 tombstones.**

Use fake/open-result relay fixtures and mixed HTTP/CONNECT/SOCKS acquisition paths. Assert one session reaches 256 total including two pooled idle HTTP streams, attempt 257 fails with `ErrStreamLimit`, and every close/eviction/cancellation releases exactly one slot.

```go
func TestSessionAllows256CombinedStreamsAndRejects257(t *testing.T) { /* fake relay */ }
func TestMixedProxyUsersShareOneRelaySessionLimit(t *testing.T) { /* HTTP, CONNECT, SOCKS, idle */ }
func TestClosedStreamHistoryRetainsExactly1024(t *testing.T) { /* bounded late frames */ }
```

- [ ] **Step 2: Run the admission tests and observe current 32/128 failures.**

Run: `go test ./windows-client/internal/relayclient ./windows-client/internal/httpconnect ./windows-client/internal/socks -run '256|257|Combined|Tombstone|Limit' -count=1`

Expected: FAIL at the old 32-stream and 128-tombstone boundaries.

- [ ] **Step 3: Wire the session and SOCKS gate to shared constants.**

Change only the numeric capacity source. Keep the same session map as the authoritative combined admission gate and keep SOCKS' local early rejection tied to `relayclient.MaxConcurrentStreams`.

- [ ] **Step 4: Add failing inbound reservation tests.**

Inject small frame/byte budgets into a test session. Assert per-stream frame N+1 saturation, aggregate frame and byte boundaries across streams, a partial read remaining charged, full read refund, remote-close drain, local close discard, cancellation, malformed/error teardown, and session close all refund exactly once. Assert a saturated stream sends existing `client_closed` while a peer stream keeps reading.

```go
func TestInboundPartialReadRetainsReservation(t *testing.T) { /* refund after final byte */ }
func TestInboundSessionBudgetIsSharedAcrossStreams(t *testing.T) { /* small injected limits */ }
func TestInboundCloseAndFailureRefundExactlyOnce(t *testing.T) { /* all terminal paths */ }
```

- [ ] **Step 5: Run the inbound tests and observe the four-frame/no-byte-budget failures.**

Run: `go test ./windows-client/internal/relayclient -run 'Inbound|PartialRead|Refund|Saturation' -count=1`

Expected: FAIL because the current channel holds four raw byte slices and has no session budget.

- [ ] **Step 6: Implement the 32-frame mailbox and session-wide reservation lifecycle.**

Replace `chan []byte` with a 32-entry channel of reserved frames. Charge decoded `len(payload)` before enqueue, retain the reservation in the partial-read field, release after its last byte, and drain/refund unread frames on every non-draining terminal path. Preserve remote-close drain-before-EOF and synchronous outbound writes.

```go
type inboundFrame struct {
    payload     []byte
    reservation *inboundReservation
}
```

- [ ] **Step 7: Verify framing and HTTP reuse regressions.**

Run: `go test ./windows-client/internal/relayclient ./windows-client/internal/httpconnect ./windows-client/internal/socks -count=1`

Expected: PASS, including 16 KiB outbound splits, 32 KiB inbound acceptance, 64 KiB pre-open guards, authenticated listeners, and two-stream idle reuse.

- [ ] **Step 8: Commit the Windows task.**

```powershell
git add windows-client/internal/relayclient windows-client/internal/httpconnect/server_test.go windows-client/internal/socks/server_test.go
git commit -m "feat: expand windows stream mailboxes"
```

---

### Task 3: Expand Android outbound, target-bound, and selector-command capacity

**Files:**
- Modify: `android/app/src/main/java/com/mobileegress/agent/session/AgentSession.kt`
- Modify: `android/app/src/main/java/com/mobileegress/agent/session/OutboundMailbox.kt`
- Modify: `android/app/src/main/java/com/mobileegress/agent/session/TargetIoReactor.kt`
- Modify: `android/app/src/test/java/com/mobileegress/agent/session/StreamAdmissionTest.kt`
- Modify: `android/app/src/test/java/com/mobileegress/agent/session/OutboundMailboxTest.kt`
- Modify: `android/app/src/test/java/com/mobileegress/agent/session/TargetIoReactorTest.kt`
- Modify: `android/app/src/test/java/com/mobileegress/agent/session/TargetReactorPolicyTest.kt`
- Do not add a 256-socket case to `TargetIoReactorIntegrationTest.kt`

**Interfaces:**
- Produces `AgentCapacity` values: 256 streams, 512 controls, 32 per-stream outbound frames, 8,192 aggregate outbound frames, 64 MiB outbound bytes, 32 target-bound frames, 8,192 target-bound frames, 64 MiB target-bound bytes, 8,192 data commands, 1,024 reserved control commands, 9,216 total commands, 512 commands processed per cycle, and 1,024 retained stream records.
- Extends `OutboundMailbox` with an injected aggregate-byte capacity and snapshot/test seam while retaining `offerData`, `poll`/`receive`, `emit`, cancellation, and required-control APIs.
- Keeps one FIFO reactor command queue, with separate counters deciding whether a data or control command can enter it.

- [ ] **Step 1: Add failing constant/admission/mailbox tests.**

Use in-memory `StreamAdmission` and small injected `OutboundMailbox` budgets. Assert 256/257 admission, 32/33 frames on one stream, aggregate frame and encoded-byte limits, reservation persistence after `poll`, refund after `emit`, and exact cancellation/close cleanup. Retain control priority and round-robin assertions.

```kotlin
@Test fun `production limits expose 32-frame 64-MiB lanes`() { /* exact constants */ }
@Test fun `polled outbound data remains charged until emission`() { /* injected budget */ }
@Test fun `data saturation blocks only its contributing stream`() { /* peer emits */ }
```

- [ ] **Step 2: Run focused Android unit tests and observe current capacity failures.**

From `android`, run: `.\gradlew.bat testDebugUnitTest --tests 'com.mobileegress.agent.session.StreamAdmissionTest' --tests 'com.mobileegress.agent.session.OutboundMailboxTest'`

Expected: FAIL at the current two-frame/256-frame and absent byte-accounting boundaries.

- [ ] **Step 3: Implement outbound encoded-byte reservations.**

Charge `frame.size` when `offerData` retains an encoded frame. Do not decrement frame or byte counters in `poll`; release through the existing frame-release path after emission/cancellation/failure. Make `discardData` and `close` release every queued and in-flight cancellation record exactly once.

```kotlin
private var outstandingDataFrames = 0
private var outstandingDataBytes = 0L
```

- [ ] **Step 4: Add failing fake-reactor tests for command reserves and target-bound accounting.**

With injected tiny limits and fake selector/channel adapters, fill data-command capacity, prove 1,024-equivalent reserved control slots remain usable, reject the next control after total capacity, and assert FIFO order. Queue more than 512 commands and prove one cycle consumes exactly 512 before selected-key work. Test per-stream target frame saturation, session frame/byte boundaries, partial writes retaining reservations, final writes/refunds, cancellation, failure, and shutdown.

```kotlin
@Test fun `data commands cannot consume reserved control slots`() { /* scaled injected limits */ }
@Test fun `selector processes at most configured commands per cycle`() { /* 512 production assertion */ }
@Test fun `partial target write retains frame and byte reservations`() { /* fake channel */ }
```

- [ ] **Step 5: Run fake-reactor tests and observe current single-512-queue/eight-frame failures.**

From `android`, run: `.\gradlew.bat testDebugUnitTest --tests 'com.mobileegress.agent.session.TargetIoReactorTest' --tests 'com.mobileegress.agent.session.TargetReactorPolicyTest'`

Expected: FAIL because the reactor currently uses one 512-entry limit, drains that limit per cycle, and uses smaller target-bound limits.

- [ ] **Step 6: Implement FIFO command reservation and target-bound limits.**

Keep one queue. Track `queuedDataCommands`; admit data only below 8,192 and any command only below 9,216. Decrement the data counter when a data command leaves the queue. Drain at most 512 commands per selector cycle. Raise target-bound per-stream/frame/byte values and keep reservations through partial channel writes until completion/discard.

```kotlin
private fun canQueue(command: Command): Boolean =
    commands.size < totalCommandCapacity &&
        (command !is Command.Data || queuedDataCommands < dataCommandCapacity)
```

- [ ] **Step 7: Run ordinary Android unit, lint, and compile checks.**

From `android`, run in order:

```powershell
.\gradlew.bat testDebugUnitTest
.\gradlew.bat lintDebug
.\gradlew.bat assembleDebug
```

Expected: PASS. Do not run the authenticated harness, physical-device acceptance, a 256-socket integration case, or a soak test.

- [ ] **Step 8: Commit the Android task.**

```powershell
git add android/app/src/main/java/com/mobileegress/agent/session android/app/src/test/java/com/mobileegress/agent/session
git commit -m "feat: expand android session queues"
```

---

### Task 4: Expand iOS outbound and target-bound capacity

**Files:**
- Modify: `ios/Sources/MobileEgressCore/Runtime/AgentRuntimeModels.swift`
- Modify: `ios/Sources/MobileEgressCore/Session/SessionPrimitives.swift`
- Modify: `ios/Sources/MobileEgressCore/Runtime/AgentSessionStateMachine.swift` only where exact refund behavior or test visibility needs adjustment
- Modify: `ios/Tests/MobileEgressCoreTests/RelayRuntimeConfigurationTests.swift`
- Modify: `ios/Tests/MobileEgressCoreTests/SessionPrimitivesTests.swift`
- Modify: `ios/Tests/MobileEgressCoreTests/AgentSessionStateMachineTests.swift`
- Modify: `ios/Tests/MobileEgressCoreTests/AgentSessionRuntimeTests.swift` only if mocked runtime cleanup needs a regression assertion

**Interfaces:**
- Produces `AgentRuntimeLimits.production` with 256 streams, 1,024 tombstones, 512 controls, 32 per-stream outbound frames, 8,192 aggregate outbound frames, 64 MiB outbound encoded bytes, 32 per-stream target frames, 8,192 target frames, and 64 MiB target bytes.
- Extends `OutboundMailbox` with `dataByteCapacity` and exact outstanding frame/byte accounting held through `emit`.
- Preserves `TargetConnectionConfiguration.inboundQueueCapacity`, `AgentSessionStateMachine`, and asynchronous `NWConnection` APIs.

- [ ] **Step 1: Add failing production-limit and outbound-mailbox tests.**

Assert the exact production values, 32/33 per-stream behavior, injected aggregate frame/encoded-byte boundaries, dequeue retaining capacity, emission refund, cancellation/close refund, O(1) deque wraparound, control priority, and round-robin scheduling.

```swift
func testProductionLimitsExposeExpandedBoundedLanes() { /* exact values */ }
func testOutboundReservationSurvivesPollUntilEmission() { /* injected limits */ }
func testOutboundCancellationRefundsFramesAndBytesExactlyOnce() { /* no underflow */ }
```

- [ ] **Step 2: Run the focused Swift tests and observe current two-frame/256-frame failures.**

From `ios`, run: `swift test --filter 'RelayRuntimeConfigurationTests|SessionPrimitivesTests'`

Expected: FAIL at the new production limits and missing outbound byte capacity.

- [ ] **Step 3: Implement outbound encoded-byte accounting.**

Charge `Data.count` when data is retained, keep frame and byte counters charged after `popFirst`, and refund via the frame's single completion path after successful/failed/canceled emission. Release queued values on stream discard and mailbox close. Leave controls outside the data budget.

```swift
private var outstandingDataFrames = 0
private var outstandingDataBytes = 0
```

- [ ] **Step 4: Add failing target-bound state-machine tests.**

Use mocked connections and injected tiny limits to prove per-stream 32/33 behavior, aggregate frame/byte boundaries across streams, partial-write/in-flight retention, final completion refunds, cancellation, target failure, and session teardown. Assert saturation closes the contributing stream while a peer continues.

```swift
func testTargetIngressReservationSurvivesUntilWriteCompletion() { /* mocked target */ }
func testTargetIngressSaturationIsStreamLocal() { /* peer remains active */ }
func testTargetIngressTeardownRefundsAllReservations() { /* snapshot returns zero */ }
```

- [ ] **Step 5: Run state-machine tests and observe current eight/512/8-MiB failures.**

From `ios`, run: `swift test --filter 'AgentSessionStateMachineTests|AgentSessionRuntimeTests'`

Expected: FAIL at the old target-bound production values or newly exposed accounting assertions.

- [ ] **Step 6: Raise target-bound production limits and complete exact refunds.**

Set `TargetConnectionConfiguration.inboundQueueCapacity` from production limits, keep decoded `Data.count` charged through partial writes, and make terminal/cancel/teardown paths idempotently return all frame/byte capacity. Do not change `NWConnection`, cellular requirements, or timeout behavior.

- [ ] **Step 7: Run ordinary Swift unit and warning-strict checks.**

From `ios`, run in order:

```powershell
swift test
swift test -Xswiftc -warnings-as-errors
```

If Swift is unavailable locally, run the repository's ordinary iOS test script later in Task 6. Do not perform physical-device or 256-connection acceptance.

- [ ] **Step 8: Commit the iOS task.**

```powershell
git add ios/Sources/MobileEgressCore ios/Tests/MobileEgressCoreTests
git commit -m "feat: expand ios session queues"
```

---

### Task 5: Align the non-release harness, current documentation, and parity manifest

**Files:**
- Modify: `windows-client/internal/capacityharness/runner.go`
- Modify: `windows-client/internal/capacityharness/runner_test.go`
- Modify: `windows-client/internal/capacityharness/session_test.go` if topology constants are asserted there
- Modify: `windows-client/cmd/mobile-egress-capacity/main_test.go` if bounded result totals change
- Modify: `README.md` only if its current capacity wording is stale
- Modify: `android/README.md`
- Modify: `windows-client/README.md`
- Modify: `docs/architecture.md`
- Modify: `docs/protocol.md`
- Modify: `docs/operations.md`
- Modify: `docs/deployment.md`
- Modify: `docs/status.md`
- Modify: `docs/capacity-acceptance.md`
- Modify: `docs/templates/physical-acceptance-record.md`
- Modify: `docs/mobile-feature-manifest.json`
- Do not modify older files in `docs/superpowers/plans/` or `docs/superpowers/specs/`

**Interfaces:**
- Consumes the 256-per-Client/256-Agent contract.
- Produces a harness topology of one 256-stream holder plus a second authenticated Client identity probing aggregate stream 257.
- Produces current documentation that labels this expansion unit-tested and explicitly not load-/physical-validated.
- Produces Android and iOS `agent.stream-capacity` evidence that passes `scripts/validate-mobile-feature-manifest.ps1`.

- [ ] **Step 1: Add failing harness-definition unit tests.**

Assert exactly two identities are requested, identity one opens/holds/verifies 256 streams, identity two attempts only the 257th aggregate probe, the existing Agent-limit category is expected, cleanup closes every held stream, and emitted totals stay secret-free. Do not invoke the production dialer or runner.

```go
func TestRunTopologyUsesOne256StreamHolderAndSecondIdentityProbe(t *testing.T) { /* fakes only */ }
func TestRunTopologyCleansUpAllHeldStreamsAfterProbe(t *testing.T) { /* fakes only */ }
```

- [ ] **Step 2: Run tagged harness unit tests and observe the old eight-by-32 expectation.**

Run: `go test -tags capacityharness ./windows-client/internal/capacityharness ./windows-client/cmd/mobile-egress-capacity -run 'Topology|Runner|Secret|Output' -count=1`

Expected: FAIL because the current harness provisions eight identities with 32 streams apiece.

- [ ] **Step 3: Change only the harness topology definitions.**

Reuse normal authentication and existing fakeable interfaces. Keep the build tag, one-time-token TLS target, bounded output, and release exclusion unchanged. Do not add an authorization bypass and do not execute the harness.

- [ ] **Step 4: Update all current capacity documentation.**

Replace current 32-per-Client/256-total claims with 256-per-Client/256-total, document 32 frames per stream and the two 64-MiB directional budgets, describe stream-local/session-fatal saturation, and state that only unit/component and ordinary build checks were performed. Rewrite the acceptance runbook/template definition to one 256-stream holder plus a second-identity probe, while marking execution pending/prohibited for this change.

- [ ] **Step 5: Update both mobile parity entries.**

For `agent.stream-capacity`, retain Android/iOS implementation statuses and add the exact outbound/target budget source files and deterministic unit tests. Do not cite physical acceptance or `TargetIoReactorIntegrationTest.kt` as new 256-stream evidence.

- [ ] **Step 6: Run harness-definition and manifest validation tests.**

Run in order:

```powershell
go test -tags capacityharness ./windows-client/internal/capacityharness ./windows-client/cmd/mobile-egress-capacity -count=1
powershell -NoProfile -File scripts\validate-mobile-feature-manifest.ps1
```

Expected: PASS. These commands compile and unit-test the non-release harness but never connect it to a relay.

- [ ] **Step 7: Scan for stale current capacity claims and historical-plan edits.**

Run:

```powershell
rg -n "32 streams|32 per Client|eight.*32|8.*32" README.md docs android\README.md windows-client\README.md -g "*.md" -g "!docs/superpowers/**"
git diff --name-only origin/main...HEAD -- docs/superpowers
```

Expected: no stale current-capacity claim remains; the second command lists only the new 2026-09-02 spec and plan.

- [ ] **Step 8: Commit the harness/documentation task.**

```powershell
git add windows-client/internal/capacityharness windows-client/cmd/mobile-egress-capacity/main_test.go README.md android/README.md windows-client/README.md docs
git commit -m "docs: align 256-stream capacity evidence"
```

---

### Task 6: Run ordinary gates and complete independent review

**Files:**
- Modify only files required to fix a deterministic test, lint, compile, documentation, or review finding within this specification.
- Do not modify version metadata, release scripts/policy, historical plans, or release artifacts.

**Interfaces:**
- Consumes all preceding task outputs.
- Produces a reviewed branch with ordinary unit/component/build evidence and an explicit `not load-validated` handoff.

- [ ] **Step 1: Run Go and tagged harness unit/component checks.**

```powershell
go test ./...
go test -tags capacityharness ./windows-client/internal/capacityharness ./windows-client/cmd/mobile-egress-capacity
```

Expected: PASS. Do not add `-bench`, repeated stress counts, race stress loops, or execute the capacity command.

- [ ] **Step 2: Run the complete ordinary Windows/frontend/Android repository gate.**

Run: `powershell -NoProfile -File scripts\test-all.ps1`

Expected: Go tests, frontend tests/build, Android unit tests, Android lint, and Android debug compile/build pass. This gate is not physical or load acceptance.

- [ ] **Step 3: Run ordinary iOS unit and compile/build checks.**

Run: `powershell -NoProfile -File scripts\test-ios.ps1`

Expected: the available Swift unit, warning-strict, simulator, and unsigned compile/build checks pass. Do not select a physical device and do not perform a 256-connection scenario.

- [ ] **Step 4: Run repository hygiene checks.**

```powershell
git diff --check origin/main...HEAD
git status --short
git log --oneline origin/main..HEAD
```

Expected: no whitespace errors, no generated/release artifacts, and only scoped commits on `codex/capacity-256-queues-32`.

- [ ] **Step 5: Request an independent whole-branch review.**

Give a fresh reviewer `origin/main...HEAD`, the design spec, this plan, and the prohibition on load/physical testing. Require the reviewer to inspect correctness, reservation/refund lifetimes, lock ordering, stream-vs-session failure policy, cross-platform constant parity, and unintended version/release changes. Resolve every Critical/Important finding with a focused red-green test and obtain re-review.

- [ ] **Step 6: Re-run only the ordinary focused checks affected by review fixes.**

Run the smallest relevant unit suite for each fix, then repeat `git diff --check origin/main...HEAD`. Do not introduce a load test to prove a review point.

- [ ] **Step 7: Commit any review fixes and prepare the handoff.**

```powershell
git add -u
git commit -m "fix: resolve capacity review findings"
```

Omit this commit when no fixes are required. Report the branch name, commits, ordinary test/build evidence, remaining environmental skips, and the explicit limitation: **256-stream/32-frame capacity is unit-tested but not load-, soak-, memory-, harness-, or physical-device-validated.** Do not merge, push, publish, tag, or change versions.
