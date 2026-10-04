# Architecture

## Direct workload-to-phone topology

Mobile Egress 2 consists of a native workload Client service/app and a phone Agent. The Agent initiates a TLS 1.3 connection to each reachable Client and carries application streams over one authenticated WebSocket per Client. The Client’s HTTP/CONNECT and SOCKS5 proxies remain local to the workload machine; target TCP connections originate from the phone’s cellular interface.

There is no controller, relay routing hop, AWS management, Tailscale, Funnel, or automatic fallback in the 2.x runtime. Workload machines behind NAT need explicit forwarding or another already-reachable public endpoint. Private connectivity traversal and hosted intermediary traffic services are outside this release.

## Workload service

Windows uses a LocalSystem service and service-account DPAPI. Apple Silicon Mac uses a root LaunchDaemon and dedicated file-based System Keychain namespace. Existing authenticated named-pipe/Unix-socket IPC restricts GUI administration to the installation owner. GUI processes do not read private service storage.

The TLS listener defaults to `:8443`; its advertised HTTPS origin is configured separately because forwarding can change the public port. Server certificate SANs match that origin and chain to the Client’s own authority. A listening socket is distinct from an externally reachable endpoint; only phone enrollment/connection proves cellular reachability.

Local proxy endpoints remain Windows `127.0.0.2`, Mac `127.0.0.1`, ports HTTP 1081/SOCKS 1080. The public listener has no general proxy or Owner administration API. Applications opt into the local proxy; no default routes or system proxy settings are changed.

The existing Tunnel abstraction connects proxies to an admitted phone session. The workload resolves destination names through bounded DNS admission and validates every candidate against the public-address policy. The phone repeats validation before opening targets.

## Pairing and recovery

Each Client generates independent authority/server credentials in protected storage. A ten-minute one-use invitation binds its identity, advertised endpoint, CA and capability. Each phone pairing has a separate non-exportable key. Redemption binds the invitation to the CSR key; both peers persist state before reporting success. Lost delivery and acknowledgements retry the same identity.

The phone registry admits ten records, including pending and disabled records. Each Client accepts one paired phone. Revocation persists before disconnecting streams and denying reconnects. Endpoint updates carry a signature from the existing pinned authority, exact Client/pairing binding and monotonic generation. Connected phones poll signed updates; offline phones import a QR/text update. Endpoint updates never replace trust or proxy credentials.

See [wire contract](direct-protocol-v2.md) for exact schemas and paths.

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
