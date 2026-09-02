# Authenticated 256-stream acceptance

This runbook defines a future authorized run of the non-release `capacityharness` build only: one legitimate Client identity opens, verifies, and holds all 256 streams, then a second legitimate identity makes its only stream attempt as aggregate stream 257. The definition also requires a 15-minute hold and immediate slot reuse. It is not a throughput benchmark and it must never be used against a production relay.

**Status: `PENDING` / definition only.** Executing the authenticated harness, opening real 256-connection topology, collecting load/soak/memory results, or performing physical-device acceptance was prohibited for the 2026-09-02 capacity implementation. The 256-stream/32-frame expansion currently has deterministic unit/component and ordinary compile/build evidence only. Do not execute any command in this runbook as part of that change; retain these instructions for a separately authorized acceptance event.

Use a dedicated, resettable relay with one paired Agent and no connected Clients or active streams. The runner provisions temporary Client identities through the production Owner API and revokes every identity during bounded cleanup. An interrupted run can therefore leave acceptance-only state that an operator must discard with the dedicated relay.

## Secret handling

Create one fresh 32-byte cryptographically random token and encode it as unpadded URL-safe Base64. Transfer it between the target operator and runner operator through a protected one-time channel, then discard it after this run.

The commands accept secrets only as a strict JSON document on standard input. Build the tagged non-release executable before handling any secret, launch that executable directly, and wait until it emits exactly `{"phase":"input","attempted":0,"open":0,"verified":0,"closed":0,"failure":"none"}`. Only then type or paste the document and send EOF. Never paste while a build, profile check, signing operation, or launcher startup is in progress. Do not place the document, token, hostname, certificate paths, Owner material, or relay identity in command arguments, environment variables, shell history, a response file, a temporary file, CI output, or logs. Do not redirect the command output into an unsanitized record. The accepted input fields are:

| Command | Exact fields |
|---|---|
| `target` | `token` (43-character unpadded URL-safe Base64), `hostname` (lowercase public DNS name), `certificateFile` (absolute path), `privateKeyFile` (different absolute path) |
| `run` | `token` (the same one-time value), `targetHost` (the same lowercase public DNS name), `targetPort` (integer `443`) |

The target certificate must be currently valid for `hostname`, trusted by the platform WebPKI roots, usable for TLS server authentication, and accompanied by its full intermediate chain. Protect the certificate private key with the host's normal administrator-only controls. The harness rejects final certificate or key file entries that are symbolic links, oversized input, non-TLS-1.3 peers, and certificates that do not validate for the requested name.

The harness's only command output is bounded JSON containing `phase`, `attempted`, `open`, `verified`, `closed`, and a fixed failure category. A successful topology finishes with totals `258 / 257 / 257 / 257`: 256 initial successes, one rejected aggregate probe, and one successful replacement. Output never includes the token, identities, relay URL, target hostname, certificate data, destinations, or payloads. Preserve only the final aggregate harness result plus the sanitized resource observations defined below in the physical-acceptance record.

## Start the temporary echo target

On a controlled Windows or macOS target host, first build the ignored non-release executable without entering or loading any secret. From the repository root on Windows PowerShell, run:

```powershell
New-Item -ItemType Directory -Force -Path '.local\capacity-harness' | Out-Null
go build -tags capacityharness -trimpath -o '.local\capacity-harness\mobile-egress-capacity.exe' './windows-client/cmd/mobile-egress-capacity'
if ($LASTEXITCODE -ne 0) { throw 'Capacity target build failed before secret entry.' }
& '.\.local\capacity-harness\mobile-egress-capacity.exe' target
```

On macOS, run:

```bash
mkdir -p .local/capacity-harness
if go build -tags capacityharness -trimpath -o .local/capacity-harness/mobile-egress-capacity ./windows-client/cmd/mobile-egress-capacity; then
  ./.local/capacity-harness/mobile-egress-capacity target
else
  printf '%s\n' 'Capacity target build failed before secret entry.' >&2
fi
```

Wait for the exact protected-input event above before entering the target JSON. If the build or command exits first, enter nothing. Delete the platform-specific executable from `.local/capacity-harness` immediately after stopping the target; never distribute or attach it to a release.

The target binds only `127.0.0.1:9443`. In another foreground terminal, use the installed, trusted Tailscale CLI to publish raw TCP port 443 to `tcp://127.0.0.1:9443`. Keep this Funnel mapping foreground and non-persistent; do not add it to setup, service, release, or startup automation. Confirm the public Funnel name is exactly the WebPKI certificate name before entering the runner input. Stop Funnel and the target immediately after the run.

The target uses a challenge-response proof derived from the one-time token before accepting exactly one random 16 KiB echo payload per stream. An unauthenticated, malformed, or excess connection is isolated to that connection and cannot cancel already verified streams.

## Run from a Windows bridge

Log in as the same Windows user that owns the production controller identity and verify the dedicated relay and Agent preconditions. Build the ignored non-release executable before handling the token, then launch it directly from the repository root:

```powershell
New-Item -ItemType Directory -Force -Path '.local\capacity-harness' | Out-Null
go build -tags capacityharness -trimpath -o '.local\capacity-harness\mobile-egress-capacity.exe' './windows-client/cmd/mobile-egress-capacity'
if ($LASTEXITCODE -ne 0) { throw 'Capacity runner build failed before secret entry.' }
& '.\.local\capacity-harness\mobile-egress-capacity.exe' run -duration 15m
```

Wait for the exact protected-input event before taking the runner baseline or entering the run JSON. The runner loads the Owner through the production DPAPI repository. It does not accept identity files, certificates, relay URLs, destinations, or tokens through flags or environment variables. Delete `.local\capacity-harness\mobile-egress-capacity.exe` after the runner exits; never distribute or attach it to a release.

## Run from a macOS bridge

The macOS Owner is accessible only inside a correctly signed temporary app with the production private Keychain access group. Use the signed host described in [Signed macOS Keychain integration](macos-keychain-integration.md#signed-capacity-acceptance-host). Its capacity mode uses the fixed 15-minute run and forwards the original protected standard input directly to the signed child. Do not invoke the raw `run` command as a substitute.

## Required result and cleanup

A passing run must use exactly two freshly provisioned authenticated Client identities. The first identity establishes and verifies all 256 held streams. The second identity makes only the aggregate stream-257 attempt, which must reject with `agent_stream_limit`. After the 15-minute hold, the runner intentionally closes one holder stream and the holder opens and verifies one replacement. The final attempted/open/verified/closed totals must be `258/257/257/257`; both temporary identities must be revoked exactly once; and every session and successfully opened stream must close within the cleanup budget.

The capacity contract allows 32 retained data frames per stream and independently caps each directional lane at 8,192 frames and 64 MiB. Per-stream, aggregate-frame, or aggregate-byte data saturation must close only the contributing stream. Required-control saturation or writer failure must close the affected session. The authenticated run observes only finite external outcomes; deterministic tests provide the exact reservation/refund boundary evidence.

Treat cancellation, timeout, cleanup failure, any unexpected stream closure, corruption, restart, queue overflow, continuously growing memory, or leaked socket as a failed gate. A failure category is diagnostic only; rerunning is allowed only after resetting the dedicated relay and confirming zero connected Clients and zero active streams.

## Resource-stability evidence

Use trusted platform process and socket inspection tools on the relay/runner host, target host, and Agent device. If the platform cannot expose aggregate process memory and established-socket counts, leave the run `PENDING`; the harness result alone does not prove resource stability. Keep only numeric aggregates and categorical results. Never preserve raw process listings, `netstat`/`lsof` output, remote addresses, ports, hostnames, destinations, identities, tokens, certificate paths, or payloads.

For each Windows-hosted or macOS-hosted bridge run:

1. After the Agent and relay are warm but before the runner connects, require relay health to report zero connected Clients and zero active streams. Start the already-built runner and wait at its documented protected-input-ready point before entering the JSON. At that point record the relay, Agent, target, and runner process start markers; aggregate resident/private memory where the platform exposes it; and established-socket counts. A start marker may be a PID plus process start time, but do not record command lines.
2. Once all 256 streams are verified, sample the same process markers, memory values, and socket counts once per minute for the 15-minute hold. Also record whether any stream or session closed with `agent_unavailable` or another saturation-related error. This categorical observation is the externally visible queue-overflow check because data saturation closes its stream and required-control saturation closes its session.
3. Fail for a restart if any process start marker changes. Independently fail for continuously growing memory if the native reported memory value for any component is strictly greater than its preceding value in every one of the final five steady-state samples. Use the platform tool's native numeric resolution without rounding or a growth tolerance; an apparent sustained increase must be investigated and rerun, not waived as measurement noise.
4. After the runner emits its final cleanup result, wait up to 60 seconds for quiescence while sampling every 10 seconds. Require the runner to exit and leave no attributable socket. Require relay health to return to zero connected Clients and zero active streams. Require established sockets attributable to the acceptance run on the relay, Agent, and target to return to their pre-run counts, and independently fail if any surviving component's memory rises in every post-cleanup sample. Do not require memory to return exactly to its original baseline because allocator and garbage-collector retention is allowed. Any residual acceptance socket, continued growth, surviving runner, or missing cleanup result fails the gate.

Record the baseline, the 15 once-per-minute numeric samples, the surviving components' post-cleanup samples, runner-exit result, unchanged-process result, queue-overflow observation, and final relay health counts in the capacity section of the acceptance record. These sanitized measurements are required in addition to the final aggregate harness JSON.

Record Windows-hosted and macOS-hosted bridge results separately. Leave them `PENDING` unless the complete run was observed. Physical iOS capacity remains `unverified—no device`; it cannot be replaced with simulator, unsigned build, package, Archive, or upload evidence.
