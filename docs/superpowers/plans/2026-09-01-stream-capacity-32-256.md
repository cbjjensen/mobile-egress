# Mobile Egress v1.1.0 Stream Capacity Implementation Plan

**Goal:** Raise Mobile Egress from 4 to 32 simultaneous TCP streams per authenticated Client identity and from 32 to 256 simultaneous streams per connected Agent while keeping memory, writers, and target I/O bounded.

**Architecture:** Keep wire protocol v1 and the existing first-come admission model. Give each relay WebSocket session one bounded writer with prioritized controls and round-robin stream data, move Android target sockets to one selector reactor, retain iOS `NWConnection`, and keep one shared 32-stream relay-session limit across the Windows headless Client's HTTP/CONNECT and SOCKS listeners.

**Execution branch:** `codex/capacity-32-256`, based on committed `main` revision `3bc0edcef7823982cc995bec29f5a924ccd5aba8`. Do not edit, merge, commit, stash, or otherwise alter the user's in-progress macOS worktree.

## Global Constraints

- Wire protocol v1, existing message schemas, existing limit error codes, certificate authentication, sealed configuration, and release-manifest schema remain unchanged.
- Admission is first-come and fail-closed: 32 live streams per Client identity, 256 live streams per Agent, ten Client identities maximum, and one active Agent.
- TCP only. Do not add UDP, QUIC, a VPN, a public proxy, a controller-host proxy, capability negotiation, or matched-version UI.
- Preferred outbound payload is 16 KiB on Windows, Android, and iOS. Continue accepting protocol-valid `data` payloads up to 32 KiB.
- Keep the existing 30-second open/connect and five-minute idle timeouts.
- Relay Agent writer: 512 control frames, 256 aggregate data frames, two data frames per stream, and 1,024 closed-stream tombstones.
- Relay Client writer: 64 control frames, 32 aggregate data frames, and two data frames per stream.
- Android and iOS Agent: 256 active streams, 1,024 tombstones, 512 outbound controls, 256 aggregate outbound data frames, two outbound data frames per stream, and two relay-to-target frames per stream.
- Android selector command queue: 512 entries. One selector thread handles all target sockets and performs at most one read chunk and one write chunk for each selected key per cycle.
- Windows headless Client: 32 streams combined across HTTP forwarding, HTTPS CONNECT, and SOCKS; 128 late-frame tombstones; HTTP pool remains two idle streams.
- Relay data saturation closes only the affected stream with existing `agent_unavailable` behavior. Required-control saturation or writer failure closes the affected WebSocket session.
- No per-frame or per-stream goroutine/thread fan-out for relay teardown or Android target reads/writes.
- Every behavior change follows red-green-refactor TDD. Tests assert behavior through real components; mocks are limited to slow or external boundaries.

## Task 1: Integrate the iOS Agent branch

Merge `codex/ios-agent` into `codex/capacity-32-256` with a non-fast-forward merge.

- Resolve conflicts by preserving current `main` security/release behavior and adding the iOS Agent as a new platform.
- Do not incorporate the dirty macOS worktree or modify the `codex/ios-agent` worktree.
- Run `scripts/test-all.ps1` after the merge to prove Windows and Android remain green.
- Record any Swift checks that require the Mac build server for Task 7 rather than weakening them.
- Commit only the merge/conflict resolution and any strictly necessary baseline repair.

## Task 2: Implement bounded relay session writers and 32/256 admission

Change the relay service and session transport so slow WebSocket writes cannot run under the global service mutex or stall unrelated sessions.

- Raise default admission to 32 streams per Client and 256 per Agent.
- Replace direct serialized WebSocket writes with one writer loop per Agent or Client session.
- Provide separate bounded control and data admission using the exact Global Constraints limits.
- Prioritize required controls. Schedule data round-robin by stream and hold no more than two queued frames per stream.
- Ensure open/close/error forwarding does not perform network writes while the service mutex is held.
- On a full data lane, fail only that stream with existing `agent_unavailable` behavior. On required-control saturation or writer failure, terminate the affected session.
- Replace close-notification goroutine fan-out with bounded enqueue/serialization.
- Increase relay tombstones to 1,024 without allowing unbounded maps or queues.

Tests must first fail and then pass for:

- One Client opens 32 streams and its 33rd receives the existing Client-limit error.
- Eight Client identities open 32 streams each and aggregate stream 257 receives the existing Agent-limit error.
- Closing a stream immediately makes its slot reusable.
- A blocked Agent or Client writer does not hold the global mutex or block routing for another session.
- Data is round-robin across ready streams, one saturated stream fails without terminating peers, and required-control saturation terminates only its session.
- 1,024+ late-close/reject/duplicate-close churn remains bounded.
- Mass teardown creates no goroutine-per-frame fan-out and passes focused race tests.
- A 32 KiB data frame is accepted; an over-limit payload retains existing rejection behavior.

## Task 3: Raise and unify Windows headless Client capacity

Update the portable Go headless Client used by EC2 nodes.

- Raise relay-session admission from 4 to 32 and late-frame tombstones from 16 to 128.
- Raise the SOCKS listener's local connection admission to 32.
- Confirm HTTP forwarding, HTTPS CONNECT, and SOCKS all acquire capacity from the same relay session; do not introduce independent 32-slot pools that permit 64 total.
- Retain two idle HTTP destination streams and ensure a pooled stream holds its relay-session slot until closed or evicted.
- Emit preferred outbound data chunks no larger than 16 KiB while accepting inbound frames through the existing 32 KiB protocol maximum.
- Preserve listener authentication, IPv4 loopback binding, destination policy, and all current timeouts.

Tests must first fail and then pass for:

- Relay session streams 1 through 32 succeed and stream 33 is rejected locally.
- Mixed SOCKS, ordinary HTTP, CONNECT, and pooled-idle streams share one 32-stream total.
- Closing or evicting streams releases slots exactly once, including abandoned opens and cancellation races.
- Late-frame churn stays bounded at 128 tombstones.
- Outbound reads are framed at 16 KiB even when the source provides more data.

## Task 4: Replace Android blocking target I/O with a selector reactor

Refactor Android Agent target connections around one nonblocking `SocketChannel`/`Selector` reactor.

- Open a channel, bind its still-unconnected `Socket` to the chosen cellular `Network`, switch it to nonblocking mode, initiate connect, and register connect/read/write interest.
- Bridge Agent-session commands to the reactor through a bounded 512-entry queue and selector wakeups.
- Maintain per-stream connection state, partial-write offsets, connect deadlines, idle deadlines, closure, and exactly-once terminal signaling.
- Perform at most one 16 KiB read and one write chunk per selected key per cycle.
- Use two-frame per-stream target write queues and fail only the affected stream when its data queue saturates.
- Remove the blocking target-reader coroutine and blocking socket write path; do not compensate by increasing `Dispatchers.IO` parallelism.
- Raise admission to 256, tombstones/cancelled-stream bounds to 1,024, controls to 512, aggregate outbound data to 256, and per-stream outbound/inbound data to two.

Tests must first fail and then pass for:

- 256 loopback target channels connect and exchange data; stream 257 is rejected.
- Cellular binding happens before connect.
- Immediate and deferred connect completion, partial reads/writes, EOF, cancellation, timeout, and selector shutdown each emit one correct terminal outcome.
- One blocked target does not prevent other targets from reading, writing, opening, or closing.
- Command, inbound, outbound, and tombstone bounds enforce the documented stream-vs-session failure policy.
- 256 ready outbound streams are served round-robin without per-stream thread growth.

## Task 5: Raise iOS Agent capacity with bounded deque mailboxes

Keep the iOS Agent's asynchronous `NWConnection` transport and apply the shared capacity contract.

- Raise admission to 256 and tombstones to 1,024.
- Set outbound control/data/per-stream limits to 512/256/2 and target-bound per-stream data to two.
- Prefer 16 KiB target reads and outbound data frames while accepting valid inbound payloads up to 32 KiB.
- Replace hot `Array.removeFirst` queues in session mailboxes with a bounded ring/deque implementation whose removal is O(1).
- Preserve `NWConnection` cancellation, cellular-path requirements, deadlines, and exactly-once terminal frames.
- Set iOS marketing version to 1.1.0 and build number to 2.

Tests must first fail and then pass for:

- 256 opens succeed and open 257 is rejected.
- Queue bounds, control priority, data round-robin, cancellation, tombstone churn, and stream-vs-session saturation policy.
- Ring/deque wraparound, full/empty transitions, removal ordering, and memory bounds.
- 16 KiB preferred chunks and 32 KiB inbound acceptance.

## Task 6: Add the authenticated capacity harness and update acceptance documentation

Add a developer-only capacity runner that is excluded from release binaries.

- Use normal certificate issuance and authenticated protocol sessions; never add an auth bypass or hard-coded credential.
- Run eight ephemeral Client identities with 32 streams each against a dedicated non-production bridge, plus explicit 33rd-per-Client and 257th-aggregate rejection attempts.
- Each stream sends and verifies at least one 16 KiB random echo payload and can remain held open for a configurable 15-minute acceptance run.
- Support a temporary TLS echo target protected by a one-time in-memory token. Its Tailscale Funnel lifecycle remains foreground/non-persistent and outside release automation.
- Read secrets only from existing protected stores or explicit non-logged input. Never log identities, credentials, destinations, tokens, or payloads.
- On macOS, reuse the signed integration-host convention needed for Keychain access rather than weakening Keychain ACLs.
- Expose bounded, non-secret results: attempted/open/verified/closed counts and failure categories.

Update architecture, platform, deployment, operations, status, and physical-acceptance documentation:

- Describe 32-per-Client and 256-per-Agent limits, first-come behavior, bounded backpressure, and 16 KiB preferred frames.
- State that normal browser-style HTTP/CONNECT and SOCKS activity is application opt-in on the same EC2 node, not a controller-host or system-wide proxy.
- Record the Android physical gate twice: Windows-hosted bridge/controller and macOS-hosted bridge/controller, 256 streams for 15 minutes, all streams verifying a 16 KiB echo, with no throughput floor.
- Mark physical iOS 256-stream acceptance `unverified—no device` and defer TestFlight promotion without blocking Android/Windows/macOS v1.1.0.
- Bump Android to versionName 1.1.0/versionCode 16 and Windows/macOS release metadata to v1.1.0 without changing manifest schema.

Tests must cover harness CLI/input validation, exact 32/256 admission behavior, echo verification failure, cancellation/cleanup, secret-free output, and exclusion from normal release builds.

## Task 7: Cross-platform verification and branch review

- Run focused race/stress tests for the relay and Go Client, then `scripts/test-all.ps1` for the complete Windows/Android gate.
- On the Mac build server, run Swift package/XCTest, iOS simulator, unsigned generic iOS-device build, macOS Go/controller tests, and the available signed integration-host checks from a separate checkout of this branch.
- If a physical Android device is available, run the non-release 256-stream acceptance harness. Otherwise leave the physical gate explicitly pending; do not fabricate evidence.
- Do not promote TestFlight, publish release artifacts, push, or merge this branch without explicit authorization.
- Run a whole-branch code review against the branch base and resolve all Critical/Important findings before handoff.
