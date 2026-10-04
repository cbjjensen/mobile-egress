# Project requirements

- Mobile Egress 2.x connects workload Clients directly to phone Agents. The phone initiates one authenticated cellular connection to each reachable Client; application traffic exits through cellular.
- The owner explicitly retired the personal-computer relay requirement on 2026-10-03. New product code must not depend on a central controller, AWS management, Tailscale, Funnel, or relay fallback. EC2 may run a locally managed workload Client.
- Local HTTP/CONNECT and SOCKS proxies remain loopback-only. The public Client listener is authenticated Agent transport, never a public proxy. Do not automatically change routers or cloud ingress.
- Preserve bounded framing, exact EOF/cancellation, and phone-wide resource accounting across at most ten saved Clients. Measure contention before adding connections or replacing protocols.
- Preserve Android/iOS feature parity except the explicitly approved lifecycle exception: Android foreground-service background operation; iOS active foreground sharing with optional keep-awake enabled by default. Never describe keep-awake as background execution.
- Follow the mobile-parity skill and maintain `docs/mobile-feature-manifest.json` with tracked evidence and the explicit exception. Direct 2.x requires fresh pairing and rejects legacy relay configuration; historical releases remain immutable.
- The Mac build server remains development infrastructure. Implement this approved migration on main and preserve the pre-existing uncommitted bridge fixes and benchmark evidence.
