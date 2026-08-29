# Operations

Read [deployment and release](deployment.md) before initializing a relay or creating an installer/APK. It contains the mandatory physical-device checks and release-signing rules. The preflight scripts only detect prerequisites; they do not install tools or expose secrets.

## Relay bootstrap

Deploy the relay container with a mounted state directory and public TLS endpoint. Initialize once with its reachable host and exact public HTTPS origin; capture the owner pairing bundle only in a password manager. The bundle contains the public relay address, private relay CA certificate, owner role, expiry, and one-time capability. Enroll an owner Windows app first, then use its pairing screen for the Android agent and ordinary Windows clients.

For the included Compose deployment, copy `deploy/.env.example` to `deploy/.env`, set `RELAY_PUBLIC_NAME` and `RELAY_PUBLIC_URL`, then initialize once from the repository root:

```text
docker compose -f deploy/docker-compose.yml --profile init run --rm relay-init
```

Save the single printed Owner pairing bundle immediately and transfer it to the owner only over an authenticated confidential channel. The Windows client verifies the enrollment TLS peer against the CA inside that owner-supplied bundle before it sends the one-time capability or CSR; the enrollment response must return the same CA. Start the TLS relay with `docker compose -f deploy/docker-compose.yml up -d relay`. The bind mount is `deploy/data:/var/lib/mobile-egress`; initialized CA, relay certificate, and SQLite files live in its `state` subdirectory. Port 8443 is the encrypted relay endpoint, not a SOCKS listener.

## Normal use

1. Start the Android agent from its visible UI and confirm it reports Cellular / Connected.
2. Start the Windows tray app's local proxy.
3. Copy its generated SOCKS5 line into the specific browser, HTTP client, or program that should use mobile egress. Do not configure the Windows system proxy or default route; only explicitly configured software uses the phone's carrier path.
4. Stop the Windows proxy or Android agent to remove the path immediately.

Keep Wi-Fi enabled while verifying the cellular-only guarantee: after abrupt cellular loss, existing streams must close and new SOCKS requests must fail closed rather than use Wi-Fi. An offline Agent is an unavailable egress path, not an alternate route.

## Health and rollback

Health is limited to relay readiness, connected identities, active stream counts, and aggregate counters. If the agent is offline, new SOCKS requests fail; no fallback route occurs. To revoke access, revoke the affected Windows or Android certificate identity from owner mode; revocation rejects new sessions and closes active sessions for that identity. To fully halt service, stop the relay container and Android foreground service.

## Release handling

Release APK signing material, local relay state, certificates, and generated pairing codes remain outside Git. Before updating the relay, back up the mounted state directory; rolling back an image must reuse the same state directory so enrolled identities remain valid. The Android release command refuses tracked signing properties and never displays its values; see [deployment and release](deployment.md) for the exact owner-only release and rollback sequence.
