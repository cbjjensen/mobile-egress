# Direct Client capacity and performance acceptance

For the hosted pilot, repeat these measurements through Inevitable Gateway with no customer inbound rule and preserve direct measurements separately. Run existing commercial proxy traffic with Mobile disabled, enabled idle, under one-/ten-Client load, overloaded, restarting and unavailable. Verify its usage submissions and billing fields remain unchanged; prove Mobile produces no usage submission. Acceptance requires no new correctness failure and at most 5% Core throughput/p95 setup regression under an agreed combined workload. Resource budgets are admission/backpressure bounds, not an advertised Mbps rate or data quota.

Exercise cross-node forwarding, owner replacement, scale-in/drain, config expiry/backend outage, device/grant revocation, TLS renewal and missed mode updates. Record phone thermal behavior and cellular IPv6/NAT64; synthetic loopback throughput does not establish handset performance. See [hosted acceptance](hosted-acceptance.md). Never overwrite historical relay/direct benchmark records.

The 2026-10-04 follow-up completed a short local transport/coexistence smoke, recorded in [hosted acceptance](hosted-acceptance.md) and the sibling Inevitable `local-capacity-report.md`. It confirmed exact bytes, Core accounting, fair progress and hundreds of Mbps to aggregate Gbps on loopback, while also showing contention when both local fixtures ran at maximum speed. This is sufficient evidence to continue local development, not a promise of equal Core/Mobile throughput or a substitute for the deployed/physical acceptance below.

The multi-phone Client has one tunnel per phone/Client pairing, up to ten phones per Client including pending pairing, and ten saved Clients per phone including disabled and pending records. Hosted transport retains one outer attachment per Client carrying those opaque phone TLS connections. These are identity/session limits, not fixed active-stream or throughput caps. Historical ten-Client measurements are not evidence for ten phones sharing one Client route; see the [multi-phone validation plan](superpowers/plans/2026-10-06-multiple-phones.md).

Directional phone data debt is globally bounded at 8,192 frames / 64 MiB, with 32 frames per stream. Queued and native in-flight work both count until completion/cancellation; ten peers do not multiply the budget. Idle saved entries reserve no static share. Control processing is bounded separately. Under contention, peers and streams must make progress without one failure stopping the others.

## Measurement procedure

Use accepted physical Android and iOS builds with a direct Windows or Mac workload endpoint, old relay offline, and no AWS/Tailscale dependency. Keep destinations and payloads private; use lab labels in evidence.

Measure single-Client downloads, uploads and browser-shaped short/long HTTP, CONNECT and SOCKS traffic. Then repeat with ten Clients and mixed fast/slow readers. Record transfer sizes, concurrency, duration, throughput in Mbps, latency distribution, CPU, resident memory, stream/frame debt, control latency, per-Client shares and phone thermal behavior. Distinguish application goodput from network bytes.

Include duplicate stream IDs across independent peers, abrupt peer loss/reconnect, target EOF with a queued tail, cancellation while a native write is in flight, and repeated Start/Stop. After each run verify exact byte counts/hashes, orderly EOF, zero leaked debt and no unwanted reconnection. Apply sustained load long enough to observe thermal changes. Do not set a target Mbps before measuring the devices/network.

On iOS, keep the dashboard active with keep-awake enabled during sustained trials. Separately test manual lock, app switching, auto-lock preference off and Stop; those must pause sharing as documented. Do not count foreground iOS results as background serving.

Record new results in a dated direct-mode report using [the acceptance template](templates/physical-acceptance-record.md). Historical relay measurements in [latency benchmarks](latency-benchmarks.md) and [browser measurements](browser-throughput-measurements.md) are comparison context only. Do not carry their pass/fail or rates into the new topology.
