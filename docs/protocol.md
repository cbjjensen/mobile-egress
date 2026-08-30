# Protocol v1 reference

Related documents: [architecture](architecture.md), [security model](security-model.md), [operations runbook](operations.md), and [current status](status.md).

This document describes the implemented relay control API and tunnel wire protocol. The public relay listener carries HTTPS and WebSocket traffic; it does not accept SOCKS. SOCKS5 exists only on the authenticated Windows IPv4 loopback listener described in the [architecture](architecture.md).

## TLS and authorization model

The relay requires TLS 1.3. Its TLS listener uses `VerifyClientCertIfGiven`: an absent client certificate is allowed so that health and enrollment can work, while a supplied certificate must verify against the relay CA.

Enrollment is the only capability-authenticated state-changing endpoint. Before sending its one-use capability or CSR, a shipped client pins TLS to the CA certificate in its invitation. It then verifies that the returned CA is byte-for-byte the invited CA, that the issued certificate chains to that CA, matches the locally generated key and response serial, and has the requested role.

After enrollment, Windows and Android pin the same relay CA and present their issued client certificate. TLS verification alone does not grant API access. Every protected HTTP request performs an application-level lookup of the certificate serial and rejects a missing, unknown, or revoked identity; the handler then enforces the endpoint's role. Session admission repeats the active role check immediately before WebSocket upgrade. While a session is open, the relay rechecks the identity status before processing every inbound binary message. A successful Owner revocation also detaches and closes the affected active session immediately.

## HTTPS endpoints

| Method and path | Authentication and role | Purpose and success response |
| --- | --- | --- |
| `GET /healthz` | Anonymous TLS; no identity role required | Read-only aggregate health. HTTP 200 normally, or 503 if persisted aggregate metrics cannot be read. Returns `readiness`, `agentConnected`, `connectedClients`, `activeStreams`, `totalStreams`, `byteCount`, and `errorCounts` as JSON. This is not Prometheus exposition. |
| `POST /v1/enroll` | Anonymous TLS plus a valid, unexpired, one-use capability whose role matches the request | Redeems an Owner, Client, or Agent capability and creates the corresponding identity. HTTP 201 returns `certificatePem`, `caCertificatePem`, uppercase hexadecimal `serial`, and `role`. |
| `POST /v1/pairing-codes` | Active Owner certificate | Issues a one-use Client or Agent capability. HTTP 201 returns secret `code`, `role`, and RFC 3339 `expiresAt`. The code must not be logged or placed in documentation. |
| `POST /v1/revoke` | Active Owner certificate | Revokes the known hexadecimal certificate `serial` in the request and closes that identity's active session, if any. HTTP 204 has no response body. Relay v1 has no identity-list endpoint. |
| `GET /v1/session` | Active Client or Agent certificate; Owner is forbidden | Upgrades to the v1 tunnel WebSocket. Only one session per certificate serial and only one Agent session may be active. |

Control request bodies are JSON and are limited by the relay to 256 KiB. The decoder rejects unknown fields and trailing JSON. The implemented request shapes are:

| Endpoint | Request body |
| --- | --- |
| `/v1/enroll` | `{"code":string,"role":"owner|client|agent","csrPem":string}` or the same request with `publicKeyPem` instead of `csrPem`; exactly one public-key representation is required |
| `/v1/pairing-codes` | `{"role":"client|agent"}` |
| `/v1/revoke` | `{"serial":string}` where the value is 1–64 hexadecimal characters |

API errors are JSON objects with an `error` string. Authentication failure is HTTP 401; an authenticated but role-incompatible identity receives HTTP 403. Enrollment capabilities are checked for expiry, single use, and exact role. Operational API use and recovery constraints are documented in the [operations runbook](operations.md), not invented as additional control endpoints.

## WebSocket message framing

One WebSocket **binary message** contains one UTF-8 JSON envelope. WebSocket message boundaries provide the framing; there is no additional length prefix or concatenated-envelope format. A text message, malformed JSON, unsupported version or type, oversized message, invalid payload, or role-incompatible message is a protocol violation.

The relay and Windows readers limit each complete WebSocket message to 2 MiB; Android independently rejects a binary message over 2 MiB. The envelope has these four fields:

```json
{
  "version": 1,
  "type": "ping",
  "streamId": "",
  "payload": ""
}
```

| Field | v1 rule |
| --- | --- |
| `version` | Integer `1`. Other versions are rejected; there is no version negotiation. |
| `type` | One of `open`, `opened`, `rejected`, `data`, `close`, `ping`, or `pong`. |
| `streamId` | Empty for `ping` and `pong`; non-empty for stream messages. See the implementation-specific validation below. |
| `payload` | Unpadded base64url using the URL-safe alphabet. Empty means zero decoded bytes. The decoded value is at most 1 MiB. Standard base64 characters and `=` padding are rejected. |

The relay parser requires all four fields exactly once and rejects unknown fields and trailing JSON. Shipped encoders always emit all four fields. A protocol violation closes the affected session and all of its streams rather than returning an extensible or partially accepted v1 envelope.

## Stream-ID ownership and validation

The Client creates the stream ID before sending `open`; the relay never creates or rewrites it. The shipped Windows Client generates 16 cryptographically random bytes and encodes them as an unpadded, 22-character base64url string.

Validation is intentionally described per implementation because it is not uniform:

- the relay requires a non-keepalive ID whose trimmed value is non-empty, but imposes no 128-character or base64url-alphabet check;
- Windows accepts any non-empty ID on a non-keepalive inbound envelope; and
- Android accepts only `[A-Za-z0-9_-]{1,128}` for a non-keepalive inbound envelope.

Therefore, an interoperable Client must generate an Android-compatible opaque ID; consumers must not assume a UUID representation or derive meaning from it. The relay rejects reuse while an ID is active or retained in its terminal tombstone set with `stream_in_use`.

## Directional message rules

| Type | Direction | Payload and state rule |
| --- | --- | --- |
| `open` | Client → Relay | Strict UTF-8 JSON `{"host":string,"port":integer}`. `host` must be non-empty, already trimmed, at most 253 bytes, and contain no NUL; `port` is 1–65535. It starts the opening state. |
| `open` | Relay → Agent | Strict UTF-8 JSON `{"ip":string,"port":integer}`. The relay resolves the Client host, rejects the request if resolution fails or any returned address violates policy, and forwards the first approved IP. The Agent validates the literal public address again. |
| `opened` | Agent → Relay → Client | Empty decoded payload. Valid only for a tracked stream still in opening state and before its relay opening deadline. It moves the stream to open. |
| `rejected` | Relay → Client, or Agent → Relay → Client | A finite error code encoded as base64url. Shipped senders use it to terminate an opening attempt; the relay removes the tracked stream on receipt. Relay-originated rejection covers pre-forward validation, availability, capacity, and opening failures. |
| `data` | Client ↔ Relay ↔ Agent | Opaque decoded TCP bytes. The relay accepts it only after `opened`; ordering follows the single WebSocket connection in each direction. |
| `close` | Client or Agent → Relay; Relay → the stream peer(s) | A finite error code encoded as base64url. It is valid in opening or open state and is terminal. |
| `ping` | Client or Agent ↔ Relay | `streamId` is empty. Receipt causes a `pong`. Shipped senders use an empty payload; current parsers require only that the payload be valid bounded base64url, not that it decode to zero bytes. |
| `pong` | Client or Agent ↔ Relay | `streamId` is empty. It is accepted without a response. Shipped senders use an empty payload, with the same parser caveat as `ping`. |

The relay accepts Client stream traffic only as `open`, `data`, or `close`; it accepts Agent stream traffic only as `opened`, `rejected`, `data`, or `close`. Windows accepts only `opened`, `rejected`, `data`, or `close` from the relay, in addition to keepalives. Android accepts only `open`, `data`, or `close` from the relay, in addition to keepalives. A role-incompatible type closes that session.

## Finite stream error codes

The v1 error-code set is:

```text
agent_stream_limit
agent_unavailable
client_closed
client_stream_limit
dns_failure
idle_timeout
invalid_target
opening_timeout
policy_denied
protocol_error
revoked
session_closed
stream_in_use
stream_not_found
target_closed
target_failure
```

Relay and Android validators enforce this finite set on inbound stream errors. Windows validates an inbound code as 1–64 decoded bytes with no ASCII whitespace; because the trusted relay emits only the finite set, protocol implementations must still send only a listed value. An invalid error payload is a protocol violation.

## Lifecycle, limits, and time boundaries

The normal state path is `open` → `opened` → zero or more `data` messages → `close`. `rejected` replaces `opened` and terminates the attempt. SOCKS success is returned only after Windows receives `opened`.

Time limits are separate boundaries, not one end-to-end timer:

- the Windows SOCKS handler gives an opening attempt 30 seconds;
- relay DNS lookup has a 30-second context;
- after relay validation and stream admission, the relay gives the tracked opening another 30 seconds to receive `opened`; and
- Android gives the target TCP `connect` call 30 seconds.

The relay expires an opening stream with `opening_timeout`. Once tracked, a stream with no Client or Agent stream message for five minutes expires with `idle_timeout`; keepalive traffic does not refresh a stream's activity time. The relay limit is four streams for each Client session and eight total streams through the single Agent. The shipped Windows listener also admits at most four local streams, and Android independently admits at most eight.

Session loss removes its streams and notifies the surviving peer when available. Official revocation removes the affected session immediately; the per-message active-identity recheck also fails closed if persisted status changes before the next message.

## Backpressure and write failure

Queues are bounded so one stalled stream cannot grow memory without limit:

- Windows buffers four inbound `data` messages per stream. If that queue is full, it terminates that stream locally, sends `close(client_closed)`, and leaves the WebSocket session available for other streams.
- Android buffers four relay-to-target messages per stream. It also limits Agent-to-relay data to eight queued messages per stream and 64 total, with a separate 32-message required-control queue. Inbound or outbound data saturation fails the affected stream with `close(agent_unavailable)`. If a required control message cannot be queued, Android terminates the entire Agent session as backpressure failure.
- Relay WebSocket writes have a one-second writer-acquisition/write deadline and no unbounded per-stream queue. Failure while forwarding an `open` removes the tracked stream and rejects the Client as `agent_unavailable`. Other forwarded messages are not retried.

Windows serializes writes with a five-second deadline. A failed stream-data write explicitly closes its session, and any WebSocket read failure also closes the session; either path fails all remaining local streams. An `open` write failure rejects only that attempt as relay-unavailable and does not itself call the Windows session-close path. Android terminates its Agent session when the WebSocket sender reports failure.

## Terminal tombstones

Terminal-frame races are absorbed differently by each implementation; v1 does not promise one uniform tombstone policy:

- **Relay:** retains at most 128 closed IDs for 30 seconds, including the owning Client and Agent sessions. During that interval it rejects ID reuse and absorbs only a valid finite-code `close` from the same former owner. A non-`close` late message or a message from another session is a protocol violation.
- **Windows:** retains the 16 most recent locally closed IDs with FIFO eviction and no time expiry. For one of those IDs it ignores a later permitted inbound stream message after envelope validation. Remotely closed streams are not added to this local tombstone set.
- **Android:** retains the 32 most recent rejected or released IDs with FIFO eviction and no time expiry. It absorbs a later valid `close` when that ID is tombstoned or when an outstanding frame for the stream was canceled. Data for an unknown stream, or an unknown-stream `close` without either condition, is a protocol violation; a new `open` remains the normal stream-creation path.

Applications must treat `rejected` and `close` as terminal and must not depend on a tombstone lasting beyond the implementation-specific bounds above.

## Reconnect and health behavior

Android reconnects a terminated Agent session while the same selected cellular `Network` remains available. The delays are 2, 4, 8, 16, then 30 seconds, capped at 30 seconds for later attempts. A successful connection resets the attempt count. Cellular loss cancels the reconnect job, closes the old session and streams, resets the count, and waits for a new cellular network; it never selects Wi-Fi as fallback.

Windows fetches `/healthz` before opening `/v1/session`. A reachable, valid health response may report the Agent offline; Windows still opens the Client session but marks it unhealthy, so new SOCKS requests fail until the two-second health poll observes both `readiness` and `agentConnected`. Each poll has a five-second context. If the WebSocket session itself closes or fails protocol validation, Windows fails all streams and does not redial inside that session; stopping and starting the local proxy creates a new Client session.
