# Architecture

Related documents: [protocol reference](protocol.md), [security model](security-model.md), [operations runbook](operations.md), and [current status](status.md).

## Product boundary

Mobile Egress provides selective mobile egress. A Windows application exposes an authenticated SOCKS5 `CONNECT` listener on IPv4 loopback. Only applications explicitly configured with that local listener use the Android phone's cellular path; the Windows default route and other application traffic are unchanged.

The public relay is an HTTPS and WebSocket service. It is never a public SOCKS endpoint.

```text
Selected Windows application
  -> 127.0.0.1 SOCKS5 listener (Windows application)
  -> Client-authenticated WebSocket
  -> relay stream router
  -> Agent-authenticated WebSocket
  -> Android socket bound to the selected cellular Network
  -> public TCP target
```

## Identities and authority

The Windows application holds two separate relay identities:

- **Owner** is the privileged control identity. It may issue one-use Agent or Client pairing capabilities and revoke an identity by certificate serial. It cannot open a tunnel session.
- **Client** is the tunnel identity used by the local SOCKS proxy. It may establish a Client WebSocket session and request streams. It cannot issue pairing capabilities or revoke identities.
- **Agent** is the Android identity. It may establish the single Agent WebSocket session and supply cellular-bound target connections. It has no Owner authority.
- **Relay administrator** is an operational role with access to the relay host and persisted state. It is distinct from certificate roles even when the same person is also the Owner.

The relay TLS listener requests a client certificate but permits certificate-free TLS for bootstrap and health. Protected endpoints then check the verified certificate serial, active/revoked state, and permitted role in application code. See the [protocol endpoint matrix](protocol.md#https-endpoints) and [security model](security-model.md) for the authentication and trust boundaries.

## Component responsibilities

| Component | Responsibilities | Explicit boundary |
| --- | --- | --- |
| Relay | Terminate TLS 1.3; redeem enrollment capabilities; issue and revoke certificates; authorize active identities; resolve and validate Client targets; match Client and Agent streams; enforce capacity and timeouts; retain aggregate counters | Does not expose SOCKS, originate target traffic, retain payloads, or provide identity-list or destination-history APIs |
| Windows application — Owner side | Retain the Owner identity; issue Agent/Client pairing capabilities; revoke a known certificate serial | Owner credentials never authenticate the SOCKS tunnel |
| Windows application — Client side | Retain a separate Client identity; maintain the Client relay session; host an authenticated, `CONNECT`-only SOCKS5 listener on `127.0.0.1`; generate stream IDs | Does not change the Windows system proxy or default route |
| Android Agent | Retain the Agent identity and AndroidKeyStore private key; acquire a cellular `Network`; keep the Agent session visible in a foreground service; validate relay-supplied target IPs; open and relay target TCP sockets | Does not install a VPN, change the phone's default route, control unrelated phone traffic, accept inbound Internet traffic, or fall back to Wi-Fi |

## Stream data flow

1. An authenticated local SOCKS user sends a `CONNECT` request to the Windows loopback listener.
2. The Windows Client creates a random stream ID and sends the requested `{host,port}` to the relay.
3. The relay resolves the host, applies the public-TCP destination policy to every returned address, and forwards the first approved `{ip,port}` to the Agent under the same stream ID.
4. The Agent applies its own public-address check, creates the target socket from the selected Android cellular `Network`, and reports `opened` only after the TCP connection succeeds.
5. The relay returns no SOCKS success until `opened`. Subsequent `data` payloads are opaque TCP bytes routed by stream ID until a terminal `rejected` or `close` transition.

The wire schemas, directional state rules, limits, timeouts, and backpressure behavior are normative in the [protocol reference](protocol.md). Destination trust decisions are described in the [security model](security-model.md).

## Android cellular boundary

Android obtains a `Network` with cellular transport and Internet capability. Enrollment and Agent-session HTTP/TLS clients use that network's socket factory and DNS resolver. Target TCP sockets are also created from that network's socket factory. Consequently, the Agent's relay DNS, relay connection, enrollment connection, and target connection are individually cellular-bound.

This is per-socket routing, not an Android VPN or device-wide routing change. Losing the selected cellular network tears down the Agent session and streams; it does not authorize Wi-Fi fallback.

## Trust and persistence

Relay initialization creates a private CA, relay certificate, and SQLite database in the state directory. A device generates its own key pair, submits only a CSR or public key during enrollment, and retains the issued identity locally. Android keeps the private key in AndroidKeyStore. Windows persists Owner and Client state with Windows-current-user DPAPI protection.

The relay database retains capability hashes, identity role/status and last-seen state, aggregate byte/stream counters, and finite error counts. Relay CA/key files and SQLite state are filesystem-protected operational secrets. Detailed custody, local-adversary, and revocation assumptions belong to the [security model](security-model.md); deployment, backup, and recovery steps belong to the [operations runbook](operations.md).

## Implemented defaults and runtime inputs

The following service behavior is hard-coded in the current implementation rather than exposed as runtime tuning knobs:

- one active Agent session;
- at most four active streams for one Client session;
- at most eight active streams relay-wide through the Agent;
- a 30-second relay opening deadline and a five-minute stream idle timeout; and
- public TCP targets only.

The relay `serve` command accepts the state directory and TLS listen address. Relay initialization accepts the state directory, public DNS name, and exact public HTTPS origin used in invitations and certificates. Compose supplies those command inputs from its deployment environment. Changing stream capacities or timeout constants requires a code change and a new build.

## Health and observability

`GET /healthz` is anonymous and read-only. It returns one aggregate JSON object containing:

- `readiness`;
- `agentConnected`;
- `connectedClients`;
- `activeStreams`;
- cumulative `totalStreams` and `byteCount`; and
- cumulative `errorCounts` keyed by finite protocol error code.

The response contains neither Prometheus exposition text nor per-identity, destination, credential, or payload detail. Readiness is false, and the relay returns HTTP 503, if aggregate metrics cannot be read; otherwise a running service returns HTTP 200 even when no Agent is connected. Operational interpretation and restart procedures are in the [operations runbook](operations.md).
