# Personal PC transport optimization implementation plan

**Goal:** Reduce framing and stream-opening overhead while keeping Funnel and the owner's personal computer in the traffic path.

**Approved scope:** Raw binary data with legacy compatibility; stream-local overload handling; multiple validated destination addresses with bounded fallback; contention measurements before any connection-topology redesign. Cloud relay hosting and cloud-relay benchmarks are prohibited by the owner.

**Architecture:** Keep one authenticated WebSocket per Client/Agent. New peers request `/v1/session?transport=2`. A supporting relay sends the first v1 JSON `ping` with base64url payload `mobile-egress.transport.v2`; until that advertisement is received new peers send legacy frames. Old relays ignore the query and continue v1. Supporting relays accept legacy frames alongside negotiated binary frames and translate for legacy peers. Controls remain v1 JSON.

**Binary data contract:** Bytes `02 04`, a big-endian uint16 stream-ID byte length (1–128), ASCII `[A-Za-z0-9_-]` stream ID, then 0–32768 raw payload bytes. Reject truncated headers, invalid IDs, oversize payloads, and binary data before negotiation. Preserve existing frame and byte budgets, counting the retained payload representation.

**Address contract:** For a transport-2 Agent, the relay sends `{"ip":"first","port":443,"ips":["first","alternate"]}` with at most eight unique validated public IP literals, interleaving families. Legacy Agents receive only `ip` and `port`. Both mobile platforms validate the complete list before dialing. Try candidates in order, moving on immediately after failure or after at most three seconds for an individual candidate when alternatives remain, within the existing overall connection timeout. Single-address behavior keeps its existing timeout. Cancel failed attempts; never retry after a stream opens. Rely on existing OS DNS caching rather than introduce a cache that invents DNS TTLs.

## Work and validation

- [x] Go relay/Client: Add literal binary framing fixtures and negotiation/mixed-version tests; observe failures; implement codec and routing without base64 on negotiated data paths. Preserve queued/in-flight accounting and avoid decoding solely for metrics.
- [x] Windows Client overload: Add a regression proving `agent_unavailable` rejection does not disable other opens; observe failure; remove rejection-driven global-health mutation. Keep actual health-poll/transport failure gating.
- [x] Relay resolution: Test validated alternate emission, family ordering, legacy output, invalid-answer rejection, and cancellation. Implement candidate emission without changing DNS admission bounds.
- [x] Android: Implement negotiation, binary codec and bounded candidate dialing in existing reactor; test malformed frames, compatibility, alternate success, timeouts, cancellation, and policy rejection. Run unit tests, lint, debug build.
- [x] iOS: Implement equivalent negotiation, binary codec and bounded candidate dialing using native transport; test corresponding behaviors with native Swift on the existing Mac build server. Full Xcode gate is part of integration below.
- [x] Documentation: Record the permanent routing requirement in AGENTS.md, README and architecture; document negotiated transport and update mobile parity evidence.
- [x] Contention: Add/run a controlled local mixed small-message/bulk benchmark, record limits honestly, retain current topology pending physical evidence.
- [ ] Integration: Run relevant Go tests/race checks, build/vet, mobile gates, review changes and record measured framing/latency results. Do not publish or replace installed apps.

## Verification before exact-commit Xcode gate

`go test ./...`, `go vet ./...`, `go build ./...` passed. Go race checks passed for relay service/protocol and Windows relay Client; HTTP CONNECT/SOCKS race checks also passed. Android: 223 tests, zero failures/errors, lint passed with 17 warnings in unchanged UI/dependency/resource/security files, debug assembly passed. Native Mac Swift tests with warnings-as-errors: 308 tests, zero failures, two expected device/entitled-Keychain skips. Mobile manifest validation passed. Cross-platform review issues were fixed and regression-tested. The final three-run local contention result is recorded in `docs/latency-benchmarks.md`; physical cellular/Funnel performance remains unmeasured.
