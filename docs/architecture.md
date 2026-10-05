# Architecture

## Workload-to-phone topology

Mobile Egress consists of a native workload Client service/app and a phone Agent. The Agent initiates a TLS 1.3 connection to each Client and carries application streams over one authenticated WebSocket per Client. The Client’s HTTP/CONNECT and SOCKS5 proxies remain local to the workload machine; target TCP connections originate from the phone’s cellular interface.

Gateway mode is the default for new setup. Both peers connect outbound on TCP 443 to Inevitable's existing gateway machines. A separate small service carries the phone's TLS bytes unchanged through the workload's authenticated outbound yamux connection. The workload, never the gateway, terminates the pinned phone TLS and admits its paired key. Direct mode remains an explicit Advanced choice and requires reachable workload ingress. Existing direct installations remain direct until switched. There is no automatic fallback, personal-computer controller, AWS management, Tailscale or Funnel dependency.

The separate hosted service follows the existing proxy infrastructure's deployment, signed configuration and heartbeat patterns. Mobile-only PostgreSQL records identify the current route/node/process/session owner; a private authenticated TLS bridge reaches that owner when the load balancer chooses another node. Existing Core listeners, health ownership, commercial routing and usage accounting are unchanged except the separately committed buffered CONNECT/half-close repairs. Mobile Egress has no traffic-usage ingestion, quota, byte billing or usage dashboard. Operational connection/error/resource health remains available.

## Workload service

Windows uses a LocalSystem service and service-account DPAPI. Apple Silicon Mac uses a root LaunchDaemon and dedicated file-based System Keychain namespace. Existing authenticated named-pipe/Unix-socket IPC restricts GUI administration to the installation owner. GUI processes do not read private service storage.

In hosted mode the TLS server accepts virtual connections from the outbound gateway attachment and opens no public workload socket. Browser activation binds a scoped device credential to an eligible Inevitable account; the protected service owns that credential and the PKCE proof. The phone needs no account. Initial access uses admin pilot grants; paid checkout is deferred. Activation, gateway attachment, pairing and authenticated phone connection are separate states.

In Advanced direct mode the TLS listener defaults to `:8443`; its advertised HTTPS origin is configured separately because forwarding can change the public port. Server certificate SANs match either selected endpoint and chain to the Client’s own authority. Only an authenticated live phone session completes connection verification.

Local proxy endpoints remain Windows `127.0.0.2`, Mac `127.0.0.1`, ports HTTP 1081/SOCKS 1080. The public listener has no general proxy or Owner administration API. Applications opt into the local proxy; no default routes or system proxy settings are changed.

The existing Tunnel abstraction connects proxies to an admitted phone session. The workload resolves destination names through bounded DNS admission and validates every candidate against the public-address policy. The phone repeats validation before opening targets.

## Pairing and recovery

Each Client generates independent authority/server credentials in protected storage. A ten-minute one-use invitation binds its identity, advertised endpoint, CA and capability. Each phone pairing has a separate non-exportable key. Redemption binds the invitation to the CSR key; both peers persist state before reporting success. Lost delivery and acknowledgements retry the same identity.

The phone registry admits ten records, including pending and disabled records. Each Client accepts one paired phone. Revocation persists before disconnecting streams and denying reconnects. Signed updates bind transport mode, endpoint, Client/pairing identity and monotonic generation. Capable connected phones receive updates through the authenticated session; polling remains supported. Offline/older phones import QR/file recovery. Mode switching preserves authority, pairing and proxy credentials; it reissues the hostname-bound server certificate. Updates remain pending until acknowledgement. See [hosted wire additions](hosted-transport-contract.md).

Client QRs use a compact bounded zlib presentation envelope; phone import restores the original bytes before trust validation. Copyable text and exported update files retain the original format. This adds no network service or pairing-state migration. See [QR compatibility](protocol.md#user-scanned-codes) and the [wire contract](direct-protocol-v2.md) for exact schemas and paths.

## Phone runtime and capacity

One supervisor owns all Client sessions, cellular observation, Start/Stop, and rotation. Peer errors/retries are independent; stream ownership includes Client and session generation. Removed/disabled peers cannot be resurrected by stale callbacks or rotation recovery.

The outbound and inbound retained-data lanes each share an 8,192-frame / 64-MiB phone-wide allowance. Each stream retains at most 32 data frames. Accounting includes queued and native/transport-owned debt, refunded exactly once on completion/cancellation. Idle saved Clients do not reserve a fixed tenth of the capacity. Per-stream read pause/resume and bounded fair peer admission preserve progress. Aggregate data overload closes the contributing stream; required-control failure closes the affected session. No fixed live-stream or Mbps limit is added.

Senders prefer 16-KiB data frames and accept valid frames through 32 KiB. Binary framing, ordered EOF, cancellation and validated destination alternatives reuse the proven transport semantics. Buffer limits do not equal total process memory: target sockets, stream metadata and native buffers also consume resources.

## Platform lifecycle

Android uses an owner-started foreground service, remains cellular-only with Wi-Fi present, and reconnects eligible peers when cellular returns. Reboot/force-stop requires Start again.

iOS runs the Agent in the main app while its aggregate scene is active. Inactive/background transitions close sessions/targets; active return reconnects only with retained Start intent. Stop clears that intent. Keep screen awake while sharing defaults on and disables only the idle timer while active sharing is requested, including temporary retries. Stop, disabling the option, inactive/background, or a terminal sharing failure restores the idle timer. Brightness and manual locking are unchanged.

Foreground-only iOS is an owner-approved lifecycle parity exception. The old VPN extension cannot serve traffic; only fail-closed migration access remains to remove app-owned profiles. Other VPN configurations are untouched.

## Upgrade boundary

Version 2 requires fresh pairing. Old ClientAuth relay certificates cannot serve as direct server identities. Local Client migration preserves protected credentials/ownership and marks migration required. Old QR/update formats reject with guidance; there is no mixed 1.x/2.x relay runtime. Historical release artifacts and old benchmark evidence retain their original meaning.
