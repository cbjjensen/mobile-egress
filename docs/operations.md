# Operations

## Relay bootstrap

Deploy the relay container with a mounted state directory and public TLS endpoint. Initialize once with its reachable host and port; capture the owner pairing capability only in a password manager. Enroll an owner Windows app first, then use its pairing screen for the Android agent and ordinary Windows clients.

## Normal use

1. Start the Android agent from its visible UI and confirm it reports Cellular / Connected.
2. Start the Windows tray app's local proxy.
3. Copy its SOCKS5 line into the specific browser, HTTP client, or program that should use mobile egress.
4. Stop the Windows proxy or Android agent to remove the path immediately.

## Health and rollback

Health is limited to relay readiness, connected identities, active stream counts, and aggregate counters. If the agent is offline, new SOCKS requests fail; no fallback route occurs. To revoke access, revoke the Windows identity from owner mode. To fully halt service, stop the relay container and Android foreground service.

## Release handling

Release APK signing material, local relay state, certificates, and generated pairing codes remain outside Git. Before updating the relay, back up the mounted state directory; rolling back an image must reuse the same state directory so enrolled identities remain valid.
