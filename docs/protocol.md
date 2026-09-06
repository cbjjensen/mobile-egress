# Protocol

All public relay traffic is TLS 1.3 authenticated under the relay-local CA. Funnel raw TCP forwarding does not terminate Mobile Egress TLS. Roles are encoded in client certificates and enforced per endpoint/message.

## Relay commands

- `bootstrap-owner --state-dir ... --public-name ... --public-url ... --owner-csr-file ...` initializes empty state and signs the supplied Owner CSR. Output contains only the Owner certificate chain, CA certificate, serial, and role.
- `rotate-endpoint --state-dir ... --public-name ... --public-url ...` rotates the relay leaf key/certificate under the existing CA and updates the stored origin.
- `serve --state-dir ... --listen 127.0.0.1:8443` runs foreground or through the separate Windows SCM path.
- `--version` prints the release version.

The bundled macOS LaunchDaemon uses the private `daemon` mode with no flags or extra arguments. Its state directory, admin socket, group, and loopback listener are fixed by the signed application; they are not production command-line settings and `daemon` is intentionally omitted from public usage text.

## macOS relay-admin IPC

This is local privileged administration only. It is carried on `/var/run/com.cbjjensen.mobile-egress.relay.sock`, owned `root:admin` with mode `0660`, and is never exposed through Funnel or public relay TLS.

Version 1 uses one strict JSON request and one strict JSON response per bounded frame:

```text
AdminRequest  = version + requestId + operation + optional typed setup/rotate payload
AdminResponse = version + requestId + ok + allowlisted errorCode + optional typed result/status
```

Only `status`, `setup`, `rotate`, and `repair` are accepted. Frames are at most 512 KiB; request IDs carry 128 bits of entropy and the response ID must exactly match; each operation has a five-minute deadline. Unknown versions, operations or fields, malformed/oversized payloads, mismatched IDs, conflicting request-ID reuse, and unauthorized peers fail closed. An exact completed retry receives the cached response and does not repeat the operation.

The daemon authenticates the kernel-reported peer UID. First setup requires a nonzero member of macOS `admin` and records that UID. Later management accepts only the recorded UID or root; root becomes recovery authority only after binding. Responses contain typed public status/results and finite error codes. Owner private keys, AWS credentials, node metadata, relay CA private keys, raw native/daemon errors, proxy secrets, destinations, and traffic payloads are prohibited.

## Control APIs

| Method/path | Authentication | Purpose |
|---|---|---|
| `GET /healthz` | none | Aggregate readiness/session/stream counters only |
| `POST /v1/enroll` | one-use capability | Agent enrollment and retained compatibility paths |
| `POST /v1/clients` | Owner mTLS | Sign a validated Client CSR; no Client private key is accepted or returned |
| `POST /v1/pairing-codes` | Owner mTLS | Issue short-lived Agent enrollment capability |
| `POST /v1/revoke` | Owner mTLS | Revoke a known certificate serial |
| `POST /v1/endpoint-migrations` | Owner mTLS | Issue one-use migration payload for the current endpoint |
| `POST /v1/endpoint-migrations/consume` | Agent mTLS plus capability | Consume migration and confirm the new relay URL |
| `GET /v1/session` | Client or Agent mTLS | Binary WebSocket tunnel session |

Control JSON is strict, bounded, and rejects unknown/trailing fields. Capabilities are high entropy, stored only as hashes, expire after ten minutes, and are atomically one-use.

## QR formats

QR values are unpadded base64url of strict JSON.

Agent enrollment is version 1 and includes `relayUrl`, `caCertificatePem`, `capability`, `role: "agent"`, and `expiresAt`.

Endpoint migration is intentionally distinct:

```json
{
  "version": 1,
  "type": "agent-endpoint-migration",
  "relayUrl": "https://name.ts.net:8443",
  "caCertificatePem": "...",
  "capability": "...",
  "expiresAt": "..."
}
```

Android and iOS require the QR CA to byte-match the stored CA, then use the existing mTLS identity over a cellular-bound connection to consume the capability. Only the stored endpoint changes. Android retains its Android Keystore identity; iOS retains its Secure Enclave identity and shared-Keychain certificate material.

## Sealed node configuration

The node's durable X25519 public key is returned at bootstrap. The controller creates an ephemeral X25519 key, derives a 32-byte key with HKDF-SHA256 using protocol context, and encrypts strict configuration JSON with AES-256-GCM and a fresh 96-bit nonce.

```json
{
  "version": 1,
  "ephemeralPublicKey": "<base64url X25519>",
  "nonce": "<base64url 12 bytes>",
  "ciphertext": "<base64url AES-GCM ciphertext+tag>"
}
```

Plaintext contains version, a monotonically increasing configuration generation, relay URL, Client role/serial/certificate chain/CA, and SOCKS credentials/port. The node persists the highest accepted generation and a bounded window of accepted-envelope fingerprints for that generation. It rejects replays in that window plus all stale, skipped, or reordered generations. Any valid current-generation envelope outside that window is an idempotent no-op only when its authenticated plaintext exactly matches the persisted configuration; this keeps ambiguous SSM/service-restart retries recoverable without permitting content changes or unbounded state growth. Non-canonical encoding, wrong keys, GCM failure, changed identity material, and invalid certificates fail closed. Endpoint-only updates advance the generation and change only the relay URL while retaining identity and credentials.

## Client application adapters

The EC2 Client exposes authenticated loopback-only SOCKS5 and ordinary-HTTP/HTTPS-CONNECT adapters on the same node. A browser or application opts in locally; these adapters are not controller-host, system-wide, VPN, public, UDP, or QUIC protocol behavior. SOCKS streams, ordinary HTTP requests, HTTPS CONNECT tunnels, and the ordinary-HTTP pool's at most 16 idle destination streams (four per host, expiring after 60 seconds) all multiplex over the Client identity's one relay session without a fixed active-stream ceiling.

## Tunnel session

### Negotiated transport extensions

New Clients and Agents request `/v1/session?transport=2`. A supporting relay queues a v1 JSON `ping` as the first application message, with its payload containing base64url-encoded ASCII `mobile-egress.transport.v2`. A peer sends legacy frames until it receives that advertisement, then sends raw binary `data` frames. An old relay ignores the query and never advertises the capability, so the peer remains on v1. A relay accepts raw data only on sessions that requested transport 2. Controls remain the existing v1 JSON envelopes; supporting sessions also accept legacy data. Mixed-version forwarding translates at the relay only when required.

Each raw data message contains `02 04`, a two-byte unsigned big-endian stream-ID byte length, the stream ID, then the raw payload. IDs contain 1–128 ASCII letters, digits, underscores or hyphens. Payloads contain 0–32,768 bytes. Truncation, invalid IDs, oversize payloads, and raw data before negotiation fail the session closed. Legacy frames whose IDs cannot be represented by the binary format retain their JSON representation when forwarded, preserving each receiver's existing ID policy without triggering a relay writer failure. A 16 KiB payload with a 32-byte ID uses 16,420 application-message bytes, versus 21,932 for the equivalent compact v1 JSON envelope. WebSocket and TLS overhead are additional.

The relay keeps raw data as raw bytes through routing and queueing; it does not base64-decode data merely to update traffic counters. Mailbox byte reservations count the retained representation: base64 string bytes for legacy frames, raw payload bytes for binary frames. Existing per-stream/frame/byte limits and completion accounting remain enforced.

For a transport-2 Agent, an `open` payload includes `ip`, `port`, and `ips`, a list of up to eight distinct public IP literals with `ip` first. The relay validates **all** resolver results before selecting candidates, removes duplicates, and alternates address families while preserving the resolver's first-family preference. Legacy Agents receive only `ip` and `port`. Both mobile Agents validate the entire candidate list before any connection is attempted. They try candidates in order within the existing total connection deadline: failed attempts advance immediately; attempts with an alternative remaining receive at most three seconds, and the last candidate gets the remaining time. Single-address timeouts stay unchanged. Cancellation closes the current attempt; established streams are never replayed onto another target. DNS caching remains the system resolver's responsibility; no application cache invents TTLs.

An `agent_unavailable` stream rejection is scoped to that request and no longer changes the Windows Client's global Agent availability. Actual health polls and transport shutdown still gate new opens. This prevents a resolver-capacity rejection from disabling unrelated requests until the next health poll.

### Common session limits

Binary WebSocket envelopes have finite types: `ping`, `pong`, `open`, `opened`, `rejected`, `data`, and `close`. Clients request public destinations; the relay resolves and rejects private, loopback, link-local, multicast, reserved, and otherwise disallowed addresses before forwarding `open` to the Agent. The Agent independently validates the resolved target.

Relay, Windows Client, Android and iOS admission has no fixed active-stream count ceiling. At most 256 concurrent DNS workers may run with no extra waiting queue; established streams consume no resolver permits. Excess resolution work rejects with `agent_unavailable`. Live ownership remains separate from bounded historical records. Older peers may still reject using the recognized `client_stream_limit` and `agent_stream_limit` codes. Outbound senders prefer 16 KiB data frames and receivers accept valid data frames up to 32 KiB. Every retained data lane allows 32 frames per stream and at most 8,192 aggregate frames plus 64 MiB; Client-to-Agent and Agent-to-Client data use separate directional budgets. Relay Agent-to-Client accounting is shared across all Client sessions. Reservations include queued and in-flight data and refund only after completion or discard. Required controls retain their separate 512-entry session bound, and closed-stream tombstones are capped at 1,024. Live session maps track all admitted connections until cleanup; frame sizes, open timeouts, and idle timeouts remain bounded. Agent outbound data is scheduled fairly across ready streams. Close processing is idempotent. Per-stream, aggregate-frame, or aggregate-byte data saturation closes only the contributing stream with its existing finite behavior; required-control saturation or writer failure closes the affected session.

This is a protocol-v1-compatible capacity change. Deterministic unit/component tests and ordinary build checks cover the larger values; load, soak, memory, authenticated-harness, and physical-device validation remain pending and were not run for this change.
