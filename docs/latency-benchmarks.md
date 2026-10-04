> Historical 1.x relay architecture evidence. Superseded for current product behavior by the approved direct Client-to-phone plan; measurements below are not direct-mode acceptance.

# Transport measurements

Each section identifies whether it measures local transport behavior or public
Funnel throughput. None measures the complete phone/cellular traffic path or
user browsing performance.

## Public Funnel throughput (2026-10-03)

Six sequential, completed transfers through the public Funnel ingress measured
approximately **4–5 Mbps** of payload goodput. These are observations of this
particular path. Tailscale documents
[non-configurable Funnel bandwidth limits](https://tailscale.com/docs/features/tailscale-funnel)
without publishing a numerical limit, so these results do not establish its
policy cap.

| Direction, from test client | Run 1 (Mbps) | Run 2 (Mbps) | Run 3 (Mbps) | Median (Mbps) | Median (decimal MB/s) |
| --- | ---: | ---: | ---: | ---: | ---: |
| Download | 4.028 | 4.832 | 4.188 | 4.188 | 0.524 |
| Upload | 4.604 | 4.367 | 4.591 | 4.591 | 0.574 |

The temporary client and authenticated TLS backend both ran on the owner's
personal Windows PC, with Tailscale 1.102.3 and Go 1.26.3. The client explicitly
dialed a public ingress IP on port 10000, verified against Google and Cloudflare
DNS-over-HTTPS responses. Every socket's actual destination was checked, and
TLS used the real Funnel hostname with a pinned temporary CA. Windows routed
the public connection through Ethernet with no Tailscale exit node; HTTP
proxies, redirects, and compression were disabled. This avoids MagicDNS
silently selecting a direct tailnet connection.

Traffic left the PC through public Funnel and returned to a loopback backend
on that same PC. Both test directions therefore used the home's WAN uplink
and downlink, as well as Funnel's transport. The test cannot separate a Funnel
limit from home connection limits or congestion. It includes neither the
Mobile Egress relay protocol nor a phone Agent or cellular connection. It also
does not distinguish limits per connection from limits shared across Clients.

Each invocation discarded a warmup capped at 512 KiB, then produced data for
up to ten seconds with a 256 MiB safety maximum. A bounded 60-second grace
allowed buffered data to drain. Downloads measured verified bytes received by
the client and matched the server's final byte count; uploads measured bytes
received and verified by the server, then checked its acknowledgement. All six
transfers completed with matching bytes and no byte cap reached. The receive
windows were 20.43–30.04 seconds for downloads and 10.08–10.51 seconds for
uploads: the ten-second setting bounds production, not the total receiving
interval. Rates use the actual receiving intervals, including the tail.

Matching loopback controls completed 256 MiB at 5.92 Gbps download and 4.30 Gbps
upload, each in under one second. They establish ample local test capacity,
not sustained WAN performance. Earlier incomplete public attempts used an
insufficient eight-second drain grace; their partial results are excluded.

The existing production mapping on port 8443 remained intact. The temporary
TCP mapping and its AllowFunnel permission were removed, the test server was
stopped, and the original Funnel configuration was verified exactly restored.
The installed relay and Tailscale services remained running.

[Sanitized results and interval samples](benchmarks/funnel-2026-10-03.json)
include the harness source hash and cleanup confirmation. The temporary
harness, private routing audit, and credentials remain in the ignored local
benchmark directory; private endpoints and credentials are excluded from this
record.

## WAN isolation follow-up (2026-10-03)

The follow-up demonstrates a constraint in the **shared home upstream path**:
roughly **5.3 Mbps of available payload throughput even when Funnel is
bypassed**. This explains why the earlier public-Funnel path could only deliver
about 4–5 Mbps. The measurements do not identify an ISP plan, router setting,
or competing household traffic, and do not exclude an additional Funnel limit.

| Test | Download (Mbps) | Upload (Mbps) |
| --- | ---: | ---: |
| Personal Windows PC directly to Cloudflare, two runs | 421–493 | 5.32–5.52 |
| Mac through its existing VPN to Cloudflare, two runs | 91–103 | 4.42–4.71 |
| Mac through public Funnel, one transfer | 4.59 | 4.15 |
| Mac through public Funnel, two overlapping transfers combined | 4.40 | 3.88 |

Simultaneous direct Internet uploads from the PC and Mac delivered **5.28 Mbps
combined**. The Mac and PC have different public IPv4 exits, but share the
physical home connection. Adding the second source split the available rate
instead of doubling it. To control for default IPv6 selection on the PC, a
further test forced IPv4: the PC alone uploaded at **5.49 Mbps**, the Mac alone
at **4.52 Mbps**, and both together at **5.34 Mbps combined**. The Mac's IPv4
route to both Cloudflare and the selected Funnel ingress used its existing VPN.
The VPN and all network settings were left unchanged.

The WAN controls used Cloudflare's
[official speed-test endpoints](https://github.com/cloudflare/speedtest).
Downloads checked exact payload length. Uploads sent generated test bytes with
a known Content-Length, required the full request to finish before the response
began, and required a complete successful response. That public API provides no
exact server byte-count echo. Reported WAN rates include connection and response
time; the simultaneous rates divide both payloads by the entire batch wall time,
including SSH/process overhead. Earlier default-family upload tests used
16–32 MiB per transfer; the forced-IPv4 controls used 8 MiB per transfer.

The Mac ran only temporary client executables. The authenticated test backend
remained on the owner's personal Windows PC behind public Funnel port 10000.
The Mac is a different machine and Internet exit, **not an independent off-site
host**. Six complete Funnel transfers passed TLS, destination, payload, and byte
acknowledgement checks. The scratch server admitted exactly two simultaneous
test transfers. Combined rates use verified bytes over the union of their
actual receiving intervals, not the sum of individual average rates. The two
download windows overlapped for 21.74 seconds; the upload windows overlapped
for 9.33 seconds. No production transport connection count was changed.

The present evidence supports addressing the available home upload bandwidth
before attributing this observed ceiling to Mobile Egress or a Funnel policy.
Funnel's capacity above the constrained upstream path remains unmeasured, as
does complete phone/cellular throughput.

[Sanitized follow-up evidence](benchmarks/funnel-isolation-2026-10-03.json)
contains 14 completed WAN control samples, six complete Funnel samples,
aggregation details, source hashes, and cleanup confirmation. The original
Funnel configuration was restored, the temporary PC listener stopped, and the
Mac's temporary test executables and credentials were removed. The installed
relay and Tailscale services remained running.

## Bridge correctness and scheduling review (2026-10-03)

The review baseline is `04d8c4c`. A deterministic relay reproduction queued two
16 KiB data frames, then an orderly target close, while its writer was paused.
Both legacy and binary forwarding discarded all 32 KiB and delivered only the
close. The regression now requires complete data delivery before the close,
in both directions and both framing modes, while unrelated controls can pass.
Aborts retain immediate discard behavior and all retained data stays charged.

A separate reproduction held the relay's sole SQLite connection while revoking
an unrelated identity. Established-stream routing waited until the connection
was released, approximately 150.6 ms later. That delay was injected, not a
measurement of typical SQLite latency. Regression tests require routing to
finish while the database is still blocked, for both revocation and admission.
Identity coordination remains serialized to prevent admission/revocation races.

The Android selector reproduction queued three commands with a two-command
batch limit and no socket readiness. It took 1.046 seconds because the selector
slept with a command still queued. The regression requires the queued work to
finish within 350 ms without another wakeup; the scheduler uses a nonblocking
select while commands remain. This is a controlled reactor test, not a phone
latency measurement.

The scoped slow-consumer fix pauses native target reads at the existing
32-frame per-stream outbound limit on Android and iOS. Tests stall transport
completion, then require an exact response and ordered EOF after draining.
The 8,192-frame/64-MiB aggregate overload fallback remains a stream-local close;
this is not end-to-end receive-credit flow control. An experimental iOS native
readiness layer was rejected because real Network.framework loopback tests
exposed EOF overtaking paused data. The final approach preserves the existing
native receive path and adds a wait before the next receive.

The same local `BenchmarkRelayMixedContention` fixture described below was run
on the review baseline with `-benchtime=1000x -count=3 -benchmem`, on Windows
amd64 / Ryzen 7 3700X / Go 1.26.3 / GOMAXPROCS=16. Median p95 was 0.501 ms
for idle traffic, 10.000 ms with eight legacy bulk streams, and 3.001 ms with
eight binary bulk streams. Binary bulk payload throughput was 211.2 MiB/s.
Each bulk stream has only one echo outstanding, so these measurements do not
exercise saturation, a queued EOF tail, or a slow WAN consumer. They do not
justify changing the personal-computer relay or single-session topology.

The final relay source was measured after local build/test jobs stopped, with
the same command and fixture. Each value is the median of three reports:

| Transport | Bulk streams | Baseline p95 (ms) | Fixed p95 (ms) | Baseline bulk (MiB/s) | Fixed bulk (MiB/s) |
| --- | ---: | ---: | ---: | ---: | ---: |
| Legacy | 0 | 0.501 | 0.501 | 0 | 0 |
| Binary | 0 | 0.501 | 0.500 | 0 | 0 |
| Legacy | 1 | 2.500 | 2.498 | 23.83 | 25.15 |
| Binary | 1 | 1.000 | 1.000 | 97.05 | 94.30 |
| Legacy | 8 | 10.000 | 8.502 | 59.69 | 61.08 |
| Binary | 8 | 3.001 | 3.001 | 211.2 | 197.3 |

The mixed-traffic fixture shows similar latency and variable bulk throughput,
not a general speedup. Binary eight-stream p95 ranged from 3.000 to 3.879 ms
before and 3.001 to 3.542 ms after. The fixes address reproducible stalls and
lost data; three local runs do not establish a throughput change or predict
cellular performance.

Validation of the working-tree implementation on `main`:

- Windows gate passed: all Go tests, installer contracts, vet/build, 53 frontend
  tests, typecheck/build, and release-orchestration checks.
- Android gate passed: 231 tests, lint, debug assembly, and parity validation.
  Regressions include pending command batches, per-stream pause/resume in both
  framing modes, paused-stream cancellation, EOF with a full aggregate lane,
  and short frames that fit the remaining byte budget. A real selector delivers
  an exact 128 KiB + 17-byte response across a deliberately stalled writer.
- Native Go race checks passed on macOS arm64 / Go 1.26.7 for the relay service,
  Client relay transport, HTTP CONNECT, and SOCKS using a hashed source snapshot.
- Native Swift, warnings-as-errors, and Xcode package suites each passed with
  314 tests, two existing device/entitled-Keychain acceptance skips, and no
  failures. Unsigned iPhoneOS and Simulator app/extension builds also passed.
  The native snapshot's 106 source hashes matched the working tree and remote
  files. Real Network.framework tests verify exact 2 MiB + 37-byte delivery and
  EOF, including the runtime with a stalled relay sender.
- Mobile feature manifest validation and independent cross-platform review
  passed. No installed application was replaced or release published.

Physical phone/cellular/Funnel validation, sustained soak, and full end-to-end
flow control remain outside this evidence. Existing aggregate overload handling
is retained deliberately; these tests do not establish deployed capacity.

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

Validation for source commit `50802d2`:

- `go test ./...`, `go vet ./...`, and `go build ./...`: passed.
- Go race checks for relay service/protocol and Windows relay Client: passed;
  HTTP CONNECT/SOCKS race checks also passed during the change.
- Android: 223 tests, zero failures/errors; lint completed with zero errors and
  17 warnings in unchanged files/dependencies; debug assembly passed.
- Exact-commit Mac gate: native Swift tests and warnings-as-errors tests passed
  (308 tests, two expected device/entitled-Keychain skips), followed by unsigned
  iPhoneOS and iOS Simulator app/extension builds.
- The final Xcode package test runner failed with exit 65 because the Mac's
  `com.apple.testmanagerd.control` service was unavailable. Its built-in retry
  failed for the same reason. The full iOS gate is therefore **not passing**;
  this is the previously recorded Mac test-runner infrastructure issue.
- Mobile feature manifest validation and cross-platform code review completed.
  No installed apps were replaced and no release was published.

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
