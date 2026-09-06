# Browser load harness

This isolated development tool drives Chromium through the authenticated local Windows HTTP CONNECT proxy into the selected phone Agent and relay. It uses a controlled synthetic retail fixture; it never visits real retailer sites. It adds no application runtime dependency.

Requires Node 20+, the explicit local proxy credentials, and three publicly reachable HTTPS fixture origins with trusted certificates. Playwright is locked to **1.63.0**, which selects Chromium **153.0.8010.12 (revision 1243)**; the lockfile and actual runtime version are recorded with every run. No TLS verification bypass or interception is used.

```powershell
cd tools/browser-load
npm ci
npx playwright install chromium
npm test
node test/browser-smoke.mjs
node run.mjs --plan
```

The smoke command uses temporary loopback HTTP listeners and renders the real fixture twice. It does not measure proxy, TLS, phone, relay, or production throughput. The regular test suite separately sends real HTTPS H1 and H2 requests to the fixture and checks reused connection identities. Those Node clients explicitly trust only the disposable localhost test certificate; normal certificate/hostname verification stays enabled and no trust store is changed.

## Controlled fixture

Configure `fixture.example.json` as an ignored `fixture.local.json` on an approved test host. Supply its existing trusted certificate and key paths. Set `bindHost` to the intended interface; the example binds only loopback. Launch with `node fixture.mjs fixture.local.json`. All three origins must match the runner configuration. Different ports on a single certificate hostname provide distinct origins and avoid conflating HTTP/2 coalescing with the number of origins. Provisioning DNS, certificates, firewall access, and publication are separate operator steps; this tool performs none of them.

Every navigation requests six scripts with SRI hashes, 24 product images, and six validated small JSON API responses across the origins. The page reports completion only after scripts, image decoding, and API checks pass. `Cache-Control: no-store` creates repeat traffic while HTTPS connections can remain pooled. The HTTPS server negotiates HTTP/2 or HTTP/1.1 with the browser. H1 runs disable HTTP/2; both protocols disable QUIC so traffic uses CONNECT/TCP. The runner verifies observed response protocols and fails a protocol mismatch.

## Paired runs

Copy `config.example.json` to `config.local.json`, set fixture origins, phone/build identity and installed variant. Load `PROXY_USERNAME` and `PROXY_PASSWORD` from your normal local secret mechanism; the tool does not write them to artifacts. The example uses the Windows Client HTTP proxy at `127.0.0.2:1081`; use the configured listener if yours differs. Loopback IPv4, localhost, and IPv6 loopback proxy addresses are accepted; there is no automatic direct fallback.

```powershell
node run.mjs config.local.json baseline-h1-10-p1
# Install/select the corresponding updated build on the SAME phone, update buildId/variant.
node run.mjs config.local.json updated-h1-10-p1
```

The plan contains 1, 10, 25, 50, and 100 concurrent browser contexts for H1 and H2, with three baseline/updated pairs per combination: 60 individual runs. Each uses 60 seconds warmup and 180 seconds measured traffic. Start with the lower profiles and record stable connectivity, device temperature, power state, carrier/network, relay identity and build hashes. Follow the plan order, which alternates baseline/updated order in pair two. Browser contexts are fresh initially and after every ten navigations, with repeat navigations in between to exercise retained HTTPS connections. Concurrency counts contexts, not Agent streams.

After reviewing all three pairs and external telemetry, use the highest qualifying profile for a 30 minute paired soak. Set `soakContexts`, `soakProtocol`, and `qualificationEvidence` (path to reviewed qualification evidence) in the local config, then run `node run.mjs config.local.json soak` once per build. The evidence file is hashed into metadata; the runner does not infer that a supplied file proves qualification. Overload probes belong in separate, clearly labeled result sets and are not qualifying profiles.

## Artifacts and measurement schema

Each invocation creates a unique directory and emits these files:

| File | Meaning |
| --- | --- |
| `metadata.json` | Schema version, run/task/phone/build/fixture identity, runtime browser version, fixture/lock hashes, origins, nonsecret proxy address, timestamps, optional external telemetry/evidence hashes. |
| `navigations.jsonl` | Navigations finishing at or after measurement start, including warmup crossings and measured-start tails: worker/context generation/iteration, fresh-context flag, success, error, duration, start/finish offsets from measurement start. Each navigation has a total 30 second deadline. |
| `connections.jsonl` | Browser CDP response observations: connection ID, reuse, HTTP protocol and status. These are observed browser connections, not active proxy tunnels or Agent streams. |
| `request-failures.jsonl` | CDP network failures during the measured wall-clock interval. |
| `runner-memory.jsonl` | One second samples of the Node runner's RSS/heap. These do not measure phone, browser, or relay memory. |
| `summary.json` | Completed pages/minute, completed requests/second, encoded response bytes, nearest-rank median/p95 successful navigation latency, failures, success fraction, observed connection count/protocols and qualification state. |

Throughput counts successful page completions and completed requests inside the 180 second measured interval, including pages started during warmup that finish inside the window. Qualification and navigation latency use all navigations started in that interval, allowing up to 30 seconds to finish afterward, so unfinished tail requests do not disappear from the denominator. Warmup starts are excluded from navigation qualification even when their completion contributes to throughput; measured-start tails contribute to qualification but not window throughput. Bytes are CDP `loadingFinished.encodedDataLength`, not claimed TCP or radio wire bytes. Connection IDs are deduplicated across the one browser instance. HTTP failures and network failures are reported separately from page failures.

The navigation threshold is at least 99% successful navigations within 30 seconds. Passing it leaves overall qualification **unverified** until stable connectivity, actual active proxy/Agent streams, queue occupancy, avoidable session resets, phone/browser/relay memory and absence of sustained leaks are verified from external telemetry. These fields are explicitly null, never guessed from context count. Provide optional `telemetryPath` to hash the matching capture into metadata; preserve the capture next to the result directory. Use timestamps/run identity to correlate telemetry. No passing claim is generated solely because a telemetry file exists.

Suggested external JSONL sample fields are `at`, `runId`, `source` (`windows-client`, `relay`, `android`, `ios`, or `fixture`), `activeConnections`, `activeAgentStreams`, `queuedFrames`, `queuedBytes`, `rssBytes`, `sessionResets`, `connectivityStable`, and `reason`. Collect the same counters and interval for both builds, with start/end and post-soak idle memory samples. Fixture logs give actual target-side connection opens/closes, negotiated ALPN and request bytes, but cannot substitute for Agent/relay stream telemetry.

API assumptions: [Playwright proxy and context options](https://playwright.dev/docs/api/class-browser), [Chromium CDP Network response and completion events](https://chromedevtools.github.io/devtools-protocol/tot/Network/). Run artifacts are measurements of the configured path only; local smoke output is not a device capacity result.
