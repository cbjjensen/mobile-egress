# Local transport latency benchmarks

These benchmarks measure local transport behavior with controlled peers. They do
not measure cellular RTT, deployed relay latency, Internet destination response
time, or user browsing performance.

## Negotiated binary framing and mixed traffic (2026-09-05)

The permanent deployment requirement is Funnel ingress to the owner's personal
computer relay. Hosted/cloud relay alternatives and their benchmarks are
prohibited. These measurements use a temporary **local loopback** relay, not an
external hosted service, and leave the production connection topology unchanged.

`BenchmarkRelayMixedContention` uses one authenticated Client WebSocket and one
shared Agent WebSocket, carrying a 64-byte probe echo alongside zero, one, or
eight 32 KiB bulk echo streams. Each bulk stream has at most one echo outstanding.
The Agent is a controlled echo fixture, with no target sockets or external DNS.
Both modes use the same current relay implementation; Transport1 uses legacy
JSON/base64 and Transport2 negotiates raw data. Setup and negotiation are outside
the measured interval. Samples include fixture codecs, TLS, scheduling, routing,
and all shared-writer contention. This does not simulate packet loss or prove
cellular/Funnel head-of-line behavior.

Run from the repository root:

```powershell
& C:/Users/Chad/AppData/Local/Programs/Go/bin/go.exe test ./relay/internal/service -run '^$' -bench '^BenchmarkRelayMixedContention$' -benchtime=1000x -count=3
```

Windows/amd64, Ryzen 7 3700X, Go 1.26.3, default GOMAXPROCS=16. The final run
was performed after concurrent build/test jobs stopped. Each value is
the median of three 1,000-probe reports. p95 uses sorted index
`floor((N-1)*0.95)`. This Windows clock reports approximately 0.5 ms granularity;
zero median samples are reported as a resolution bound, not zero latency.

| Data transport | Bulk streams | Probe median (ms) | Probe p95 (ms) | Bidirectional bulk payload (MiB/s) |
| --- | ---: | ---: | ---: | ---: |
| Legacy JSON/base64 | 0 | <0.5 | 0.501 | 0 |
| Raw binary | 0 | <0.5 | 0.501 | 0 |
| Legacy JSON/base64 | 1 | 1.501 | 2.502 | 23.48 |
| Raw binary | 1 | 0.500 | 1.002 | 79.23 |
| Legacy JSON/base64 | 8 | 8.000 | 11.000 | 53.57 |
| Raw binary | 8 | 2.500 | 3.500 | 184.00 |

The eight-bulk-stream p95 was about 68% lower with binary framing. Bulk rates
count completed echoed payload bytes in both directions; they exclude wire
overhead and are not deployed throughput estimates. Idle measured allocations
fell from 273 to 30 per echo, including fixture work. A 16 KiB payload with a
32-byte stream ID uses 16,420 application-message bytes instead of 21,932,
about 25% fewer bytes before WebSocket/TLS overhead.

These results support simplifying the data framing before adding more transport
connections. Keep the single-session topology. Physical mixed-traffic and
packet-loss evidence remains unmeasured; do not infer a cellular latency gain
from this fixture.

## Relay fixture and measurements

`relay/internal/service/latency_benchmark_test.go` starts an initialized relay
with its normal on-disk SQLite database in a temporary directory. It enrolls
clients and an Agent, then connects them to a local TLS/WebSocket test server.
The fixture Agent acknowledges opens and echoes 64-byte data payloads. It never
opens destination connections. DNS is stubbed to return the public IP `1.1.1.1`;
there are no external DNS requests or target connections.

The scenarios are:

* `Idle`: one established client stream, one echo outstanding at a time.
* `DelayedDNS`: the same client first sends another stream open for
  `benchmark.invalid`. After the resolver stub signals that resolution has
  started, the client sends data on its established echo stream. The stub delays
  resolution by 20 ms. Each iteration waits for both the echo and open result,
  then closes the temporary stream before starting the next iteration.
* `Concurrent4`: four client sessions with one outstanding echo each share one
  Agent session and the same disk-backed relay store.

`median-us` and `p95-us` describe echo RTT from immediately before the client
writes data until it reads and validates the echoed payload. Samples include
JSON/base64 framing, TLS/WebSockets, scheduling, routing, and the fixture Agent.
The p95 uses the sorted sample at index `floor((N-1)*0.95)`; the median uses
index `floor(N/2)`. Setup, enrollment, and initial stream opens are excluded.

`echoes/s` is completed iterations divided by timed benchmark duration.
`ns/op`, `B/op`, and `allocs/op` are Go benchmark measurements for the whole
iteration, including fixture work and background activity during that interval.
They are not allocations attributable solely to relay production code. The DNS
scenario includes the full DNS/open/close cycle in throughput and allocations,
even when the echo arrives before DNS completes. `MB/s` counts 128 payload bytes
per echo (64 each direction), excluding wire overhead. These are small-message
latency benchmarks, not saturation bandwidth benchmarks.

## Reproduction

Run the identical benchmark source against baseline `68da2e8` and the transport
latency implementation using the same machine, Go toolchain, and storage:

```powershell
Set-Location C:/Users/Chad/workspace/mobile-egress-latency/relay
& C:/Users/Chad/AppData/Local/Programs/Go/bin/go.exe test ./internal/service -run '^$' -bench '^BenchmarkRelayEchoLatency$' -benchtime=3s -count=3 -benchmem
```

For an independent baseline checkout:

```powershell
git worktree add --detach C:/Users/Chad/workspace/mobile-egress-latency-baseline 68da2e8
Copy-Item -LiteralPath relay/internal/service/latency_benchmark_test.go -Destination C:/Users/Chad/workspace/mobile-egress-latency-baseline/relay/internal/service/latency_benchmark_test.go
Set-Location C:/Users/Chad/workspace/mobile-egress-latency-baseline/relay
& C:/Users/Chad/AppData/Local/Programs/Go/bin/go.exe test ./internal/service -run '^$' -bench '^BenchmarkRelayEchoLatency$' -benchtime=3s -count=3 -benchmem
```

The baseline worktree commands assume the current directory initially is the
implementation repository root, and that the baseline path does not exist.
The added benchmark file uses no production APIs introduced by the optimization.

## Results

Recorded on Windows 10 Pro 10.0.19045, AMD Ryzen 7 3700X, Go 1.26.3
windows/amd64, default `GOMAXPROCS=16`, using the workstation's C: storage.
Each row below is the median of three benchmark reports with a three-second
target duration per run; p95 values are medians of the three independently
computed p95s. The sustained duration covers the one-second statistics flush
interval in the optimized implementation. Go calibrates iteration counts, so
sample counts differ between revisions.

Baseline production source is commit `68da2e8`. Implementation measurements use
the production source recorded in `788addb` on `perf/transport-latency`, containing
asynchronous stream-open DNS resolution, in-memory relay counters with periodic SQLite persistence, and
immediate proxy reader cancellation. The relay benchmark file was identical
in both worktrees (SHA-256
`DE756C5984ACE24FE07E74E2E9CBE17FA724C1C2C90EC911B401944B40232344`).

| Revision | Scenario | Echo median (ms) | Echo p95 (ms) | Echoes/s | B/op | Allocs/op |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| `68da2e8` | Idle | 7.000 | 18.501 | 100.0 | 14,453 | 355 |
| `68da2e8` | DelayedDNS | 27.502 | 49.986 | 25.70 | 37,018 | 900 |
| `68da2e8` | Concurrent4 | 29.003 | 52.998 | 117.6 | 14,740 | 360 |
| Implementation | Idle | <0.5 (clock resolution) | 0.501 | 5,866 | 11,706 | 276 |
| Implementation | DelayedDNS | 0.500 | 0.503 | 48.71 | 31,099 | 715 |
| Implementation | Concurrent4 | <0.5 (clock resolution) | 0.502 | 16,888 | 11,799 | 276 |

The final implementation runs completed without concurrent build/test jobs.
Their Idle and Concurrent4 median samples were below this Windows host's
observed approximately 0.5 ms clock granularity, rather than literally zero.
The delayed-DNS echo p95 fell from about 50 ms to about 0.5 ms, while the
iteration rate remains limited by the intentional 20 ms lookup delay. This
shows established-stream data progressing while a separate open resolves on
the same session. Concurrent forwarding likewise avoids SQLite counter writes
on each frame, with 276 versus 360 allocations per measured iteration.

The baseline concurrent p95 varied from 49.999 to 145.999 ms across runs.
Windows scheduling, disk flush timing, background processes, and the small
sample count make tail measurements noisy. Repeat with a larger fixed count
(for example `-benchtime=1000x -count=5`) for a stronger estimate. Do not treat
these local results as cellular performance promises or use the fixture's
throughput as a deployed capacity estimate.

## Local HTTP CONNECT acknowledgment

`windows-client/internal/httpconnect/ack_bench_test.go` starts the actual local
TCP proxy and sends authenticated HTTP CONNECT requests. Its controlled opener
yields once with `runtime.Gosched()` so the pending request reader can run, then
returns an in-memory `net.Pipe` tunnel. Each tunnel is closed before the next
sample. There is no relay, mobile device, DNS lookup, or destination connection.
This measures the proxy acknowledgment path independently of relay latency.

The custom latency sample starts just before writing CONNECT and ends when the
client reads its HTTP 200 response. Standard Go `ns/op` and allocation metrics
also include local TCP connection establishment and teardown. The benchmark
uses the lower middle sample as its median and nearest-rank p95.

Run from each revision's `windows-client` directory with identical source:

```powershell
& C:/Users/Chad/AppData/Local/Programs/Go/bin/go.exe test ./internal/httpconnect -run '^$' -bench '^BenchmarkConnectAcknowledgment$' -benchtime=100x -count=3 -benchmem
```

Median of three reports, 100 CONNECT requests per report, on the same machine:

| Revision | Ack median (ms) | Ack p95 (ms) | Whole iteration (ms/op) | B/op | Allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: |
| `68da2e8` | 50.500 | 51.000 | 47.927 | 63,761 | 121 |
| Implementation | <0.5 (clock resolution) | 0.502 | 0.601 | 64,083 | 124 |

The optimized proxy's median samples were reported as zero by `time.Now()` on
this Windows host, whose observed measurement granularity was approximately
0.5 ms. The table reports a resolution bound; zero does not mean instantaneous
acknowledgment. The whole-iteration metric measures aggregate elapsed time and
has a different boundary from acknowledgment RTT, so it is not interchangeable
with the latency percentiles. This fixture demonstrates removal of the local
roughly 50 ms reader polling delay; it does not predict end-to-end tunnel open
latency over cellular service.

## Validation

The implementation passed `go test ./...`, `go vet ./...`, and `go build ./...`
from the repository root, plus race-enabled tests for relay service, Windows
relay client, HTTP CONNECT, and SOCKS. Both baseline and final sustained relay
benchmarks completed successfully. The HTTP CONNECT benchmark completed all
three baseline and implementation runs; proxy package tests also passed.

Race-check reproduction on this workstation, from the repository root:

```powershell
$env:CGO_ENABLED = '1'
$env:CC = 'C:/Users/Chad/AppData/Local/Temp/codex-go-race-gcc-16.2.0/mingw64/bin/gcc.exe'
$env:Path = 'C:/Users/Chad/AppData/Local/Temp/codex-go-race-gcc-16.2.0/mingw64/bin;' + $env:Path
& C:/Users/Chad/AppData/Local/Programs/Go/bin/go.exe test -race ./relay/internal/service ./windows-client/internal/relayclient ./windows-client/internal/httpconnect ./windows-client/internal/socks -count=1
```

The GCC location is a temporary workstation toolchain path; substitute an
installed compatible C compiler when reproducing elsewhere. Race instrumentation
was used for correctness checks, not for the reported benchmark measurements.
