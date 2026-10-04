# Direct Client capacity and performance acceptance

The direct product has one phone-initiated tunnel per Client, one paired phone per Client, and ten saved Clients per phone including disabled and pending records. These are identity/session limits, not fixed active-stream or throughput caps.

Directional phone data debt is globally bounded at 8,192 frames / 64 MiB, with 32 frames per stream. Queued and native in-flight work both count until completion/cancellation; ten peers do not multiply the budget. Idle saved entries reserve no static share. Control processing is bounded separately. Under contention, peers and streams must make progress without one failure stopping the others.

## Measurement procedure

Use accepted physical Android and iOS builds with a direct Windows or Mac workload endpoint, old relay offline, and no AWS/Tailscale dependency. Keep destinations and payloads private; use lab labels in evidence.

Measure single-Client downloads, uploads and browser-shaped short/long HTTP, CONNECT and SOCKS traffic. Then repeat with ten Clients and mixed fast/slow readers. Record transfer sizes, concurrency, duration, throughput in Mbps, latency distribution, CPU, resident memory, stream/frame debt, control latency, per-Client shares and phone thermal behavior. Distinguish application goodput from network bytes.

Include duplicate stream IDs across independent peers, abrupt peer loss/reconnect, target EOF with a queued tail, cancellation while a native write is in flight, and repeated Start/Stop. After each run verify exact byte counts/hashes, orderly EOF, zero leaked debt and no unwanted reconnection. Apply sustained load long enough to observe thermal changes. Do not set a target Mbps before measuring the devices/network.

On iOS, keep the dashboard active with keep-awake enabled during sustained trials. Separately test manual lock, app switching, auto-lock preference off and Stop; those must pause sharing as documented. Do not count foreground iOS results as background serving.

Record new results in a dated direct-mode report using [the acceptance template](templates/physical-acceptance-record.md). Historical relay measurements in [latency benchmarks](latency-benchmarks.md) and [browser measurements](browser-throughput-measurements.md) are comparison context only. Do not carry their pass/fail or rates into the new topology.
