# Mobile Egress Capacity Relaxation — 256 Streams / 32-Frame Queues

**Date:** 2026-09-02

**Status:** Approved for implementation on `codex/capacity-256-queues-32`

**Base:** Clean `origin/main`

## Purpose

Mobile Egress currently permits 32 simultaneous streams from one authenticated Client even though an Agent can already carry 256 streams in total. This change lets one Client consume all 256 Agent slots and expands the small per-stream data mailboxes from two or four frames to 32 frames. Aggregate frame and byte ceilings keep the larger per-stream queues finite.

This is a capacity-only change. It does not change protocol v1, authentication, destination policy, timeouts, version metadata, proxy behavior, or release behavior.

## Capacity Contract

| Layer | Active streams | Per-stream data | Aggregate frames | Aggregate bytes | Controls | Tombstones |
|---|---:|---:|---:|---:|---:|---:|
| Relay, Client→Agent | 256 total | 32 | 8,192 | 64 MiB | 512 | 1,024 |
| Relay, Agent→Clients | 256 per Client, 256 total | 32 | 8,192 shared | 64 MiB shared | 512/session | Shared 1,024 |
| Windows EC2 Client inbound | 256 combined | 32 | 8,192 | 64 MiB | Existing synchronous path | 1,024 |
| Android/iOS outbound | 256 | 32 | 8,192 | 64 MiB | 512 | 1,024 |
| Android/iOS target-bound | 256 | 32 | 8,192 | 64 MiB | Platform-native | 1,024 |

The following meanings are normative:

- `256 per Client, 256 total` means one Client identity may take every Agent slot. Ten authenticated Client identities remain permitted, but they compete first-come for the same 256 Agent-wide streams.
- The 64 MiB ceilings are independent by direction. A mobile Agent may therefore retain up to 64 MiB in the relay-bound lane and 64 MiB in the target-bound lane, plus protocol objects, queue structures, socket buffers, runtime overhead, and data already owned by other layers.
- The frame and byte ceilings both apply. Admission succeeds only when the per-stream frame limit, aggregate frame limit, and aggregate byte limit all have room.
- Accounting includes queued and in-flight data. A poll, dequeue, or selector handoff does not refund capacity. The reservation remains charged until the write/emission/read finishes, or until cancellation/teardown discards it.
- A zero-length protocol-valid data payload still consumes one frame reservation and zero data-byte reservations.
- Control frames do not consume data-frame or data-byte reservations; they remain protected by their own fixed bounds.

## Byte Accounting

The byte ceiling guards the representation actually retained by each lane rather than estimating a hypothetical maximum:

| Lane | Charged bytes |
|---|---|
| Relay outbound mailboxes | The byte length of the retained base64url `Envelope.Payload` string for a `data` envelope. JSON/object overhead is separately bounded by the frame ceiling. |
| Windows inbound mailbox | The decoded payload byte-slice length retained for the application reader. |
| Android/iOS relay-bound mailbox | The complete encoded frame byte-array/`Data` length retained for WebSocket emission. |
| Android/iOS target-bound mailbox | The decoded payload byte-array/`Data` length retained until the target write completes or is discarded. |

Reservations must be exact for the retained value and idempotently refunded. Tests use small injected budgets to prove the boundary and refund paths without allocating 64 MiB or creating 256 network connections.

## Shared Capacity Values

Go components consume one canonical contract from `internal/capacity` while keeping `relayclient.MaxConcurrentStreams` as the public compatibility name used by the HTTP/CONNECT and SOCKS packages. The canonical Go values are:

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

Kotlin and Swift retain platform-native constants with the same numeric values. Unit tests and the mobile parity manifest provide drift detection across languages.

## Relay Design

The relay retains one writer loop per WebSocket session, control priority, and round-robin data scheduling.

- The Agent session mailbox carries Client→Agent data and owns a separate 8,192-frame/64-MiB budget.
- Every Client session mailbox has a 32-frame per-stream limit and 512 control slots.
- All Client mailboxes attached to the active relay share one Agent→Clients 8,192-frame/64-MiB budget. A Client disconnect, stream discard, write completion, writer failure, and service teardown each refund their outstanding reservations exactly once.
- The Agent-wide admission ceiling stays 256 and the per-Client admission ceiling rises to 256.
- A full per-stream, aggregate-frame, or aggregate-byte data budget rejects the contributing stream with the existing `agent_unavailable` stream behavior. It does not terminate unrelated streams or sessions.
- A required-control queue overflow or writer failure still terminates only the affected WebSocket session.
- Network writes remain outside the global service lock and mailbox locks.
- Closed-stream history remains bounded at 1,024 entries.

## Windows EC2 Client Design

The headless Client keeps one relay `Session` admission gate shared by ordinary HTTP forwarding, HTTPS CONNECT, SOCKS5, and the two retained idle HTTP streams.

- `relayclient.MaxConcurrentStreams` becomes an alias of the canonical 256-stream value; SOCKS does not receive a second independent pool.
- Each inbound stream mailbox holds at most 32 frames.
- One session-wide inbound budget holds at most 8,192 frames and 64 MiB across all streams.
- The currently-read partial frame remains reserved until the application consumes its last byte.
- Remote close preserves the existing drain-before-EOF behavior. Local close, cancellation, error, session failure, and teardown discard unread data and refund all reservations exactly once.
- Inbound saturation closes only the contributing stream with the existing `client_closed` behavior.
- Outbound data stays synchronously backpressured under the existing writer serialization; no second Windows outbound queue is introduced.
- The 64 KiB pre-open guards, 16 KiB preferred outbound chunks, 32 KiB accepted inbound data frames, two idle HTTP streams, authentication, and timeouts are unchanged.
- Closed-stream history rises from 128 to 1,024.

## Android Design

Android keeps the existing single nonblocking `SocketChannel`/`Selector` target reactor.

- Relay-bound outbound data uses 32 frames per stream, 8,192 frames per session, and 64 MiB of retained encoded-frame bytes. Reservations survive dequeue and are refunded by emission, cancellation, failure, or close.
- Target-bound data uses 32 frames per stream, 8,192 frames per session, and 64 MiB of decoded payload bytes. Reservations survive command dequeue and partial socket writes, then refund after the final byte or discard.
- The reactor command queue remains one FIFO so `data`, `release`, `cancel`, and `close` ordering cannot be inverted. It admits at most 8,192 data commands and 9,216 commands total, thereby reserving 1,024 slots for control commands.
- The selector consumes at most 512 commands per cycle before returning to selected-key I/O. It continues to perform at most one read and one write per selected key per cycle.
- No per-stream reader/writer threads or blocking target I/O are added.
- Admission remains 256 and retained cancellation/tombstone histories remain 1,024.

## iOS Design

iOS keeps asynchronous `NWConnection`, the bounded O(1) deque, and state-machine ownership of target-bound accounting.

- The outbound mailbox charges encoded `Data` through emission/cancellation and applies 512 controls, 32 frames per stream, 8,192 aggregate frames, and 64 MiB.
- The target-bound state machine applies 32 frames per stream, 8,192 aggregate frames, and 64 MiB to decoded `Data`, including partially written/in-flight data.
- Cancellation, connection failure, terminal state, and teardown refund retained data exactly once.
- Admission and tombstone limits remain 256 and 1,024 respectively.
- `NWConnection` lifecycle, cellular requirements, deadlines, 16 KiB preferred target reads, and 32 KiB accepted data frames remain unchanged.

## Harness and Documentation

The non-release authenticated capacity harness definition changes from eight identities holding 32 streams each to:

1. one legitimate Client identity holding 256 streams; and
2. a second legitimate Client identity probing aggregate stream 257 and receiving the existing Agent-limit rejection.

Only harness definitions and unit tests are changed. The harness is not executed. Current README/runbook/status/architecture/protocol/deployment text changes to 256-per-Client/256-total and explicitly says the expansion is unit-tested but not load-, soak-, memory-, or physical-device-validated. Historical plans remain unchanged.

Both Android and iOS entries for `agent.stream-capacity` in `docs/mobile-feature-manifest.json` must point at the source and unit-test evidence introduced or strengthened by this change.

## Compatibility and Saturation

- Protocol v1 and its existing message schema/error codes remain unchanged.
- Mixed protocol-v1 versions remain wire-compatible, but a mixed deployment operates at the lowest capacity enforced by any participating component.
- Admission remains first-come.
- Data saturation is stream-local. Required-control saturation and writer failure are session-fatal.
- Authentication, one active Agent, ten Client identities, 30-second connect/open timeout, five-minute idle timeout, sealed configuration, public-destination policy, and loopback proxy behavior remain unchanged.
- No capability negotiation, migration, matched-version UI, UDP, QUIC forwarding, VPN behavior, or public proxy is introduced.

## Verification Policy

Verification is deliberately limited to deterministic unit/component tests and ordinary compile, lint, and build checks:

- Relay: in-memory 256/257 admission; 32/33 per-stream frames; small injected aggregate frame/byte boundaries; cross-Client shared budget; exact refunds; control priority; round-robin scheduling; writer failure; teardown.
- Windows: fake relay streams and in-memory readers for mixed-proxy admission, per-stream/session inbound limits, partial reads, close/cancel/failure refunds, tombstones, 16 KiB framing, and HTTP reuse.
- Android: in-memory admission/mailbox tests and fake-selector/reactor tests for data/control command reserves, 512-command fairness, partial writes, cancellation, and shutdown.
- iOS: state-machine and mocked-connection tests for admission, deque wraparound, aggregate/per-stream limits, cancellation, emission, partial writes, and stream-local saturation.
- Harness: unit tests assert the one-by-256 holder and second-identity aggregate probe without running authenticated acceptance.
- Ordinary Go, Android, iOS, frontend, lint, and compile/build checks may run.

The following are expressly prohibited for this implementation:

- real 256-connection tests;
- authenticated capacity-harness execution;
- load or soak tests;
- 15-minute holds;
- benchmarks or memory profiling;
- physical Android/iOS acceptance;
- version changes, merging, pushing, publishing, or release creation.

The final handoff must state that the larger limits are unit-tested and not load-validated.
