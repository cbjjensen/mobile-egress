# Operations

Read [deployment and release](deployment.md) before initializing a relay or creating an installer/APK. It contains the mandatory physical-device checks and release-signing rules. The preflight scripts only detect prerequisites; they do not install tools or expose secrets.

## Relay bootstrap

Deploy the relay container with a mounted state directory and public TLS endpoint. A relay administrator manually initializes it once with its reachable host and exact public HTTPS origin, then captures the Owner pairing invitation only in a password manager. The invitation contains the public relay address, CA certificate trust anchor, owner role, expiry, and high-entropy one-time capability. The private CA remains only in the protected relay state directory. Transfer that invitation only through an authenticated confidential channel to one Windows app; that app enrolls its Owner identity and automatically enrolls its separate local Client identity.

For the included Compose deployment, copy `deploy/.env.example` to `deploy/.env`, set `RELAY_PUBLIC_NAME` and `RELAY_PUBLIC_URL`, then initialize once from the repository root:

```text
docker compose -f deploy/docker-compose.yml --profile init run --rm relay-init
```

Save the single printed Owner pairing bundle immediately and transfer it to the owner only over an authenticated confidential channel. The Windows client verifies the enrollment TLS peer against the CA inside that owner-supplied bundle before it sends the one-time capability or CSR; the enrollment response must return the same CA. Start the TLS relay with `docker compose -f deploy/docker-compose.yml up -d relay`. The bind mount is `deploy/data:/var/lib/mobile-egress`; initialized CA, relay certificate, and SQLite files live in its `state` subdirectory. Port 8443 is the encrypted relay endpoint, not a SOCKS listener.

## First-time setup or re-pairing

Install the Windows application and signed Android APK manually through owner-controlled channels. These are installation responsibilities, not an automatic-update process. The relay administrator confidentially transfers the one-use Owner invitation only for the first Windows setup; the Windows app pastes it and automatically enrolls its separate local Client identity. When the Android Agent is first paired or needs re-pairing, create a short-lived Agent QR in that Windows app and use Android **Scan QR**. Android does not accept a pasted Agent invitation.

## Daily use

No scripts or AWS credentials are needed for ordinary use:

1. Start the Android agent from its visible UI and confirm it reports Cellular / Connected.
2. Start the same Windows app's local proxy, which uses its separate Client identity.
3. Copy its generated SOCKS5 line into the specific browser, HTTP client, or program that should use mobile egress. Do not configure the Windows system proxy or default route; only explicitly configured software uses the phone's carrier path.
4. Stop the Windows proxy or Android agent to remove the path immediately.

Keep Wi-Fi enabled while verifying the cellular-only guarantee: after abrupt cellular loss, existing streams must close and new SOCKS requests must fail closed rather than use Wi-Fi. An offline Agent is an unavailable egress path, not an alternate route.

IP rotation is intentionally unsupported. Reconnecting the Agent can restore its cellular-bound relay session, but the application cannot reset carrier data or guarantee a changed carrier IP.

## Local Windows Client recovery

The Owner screen displays the current local Client certificate serial because relay v1 has no identity-list endpoint. Record that serial before revocation. Revoke it from the same Owner screen, choose **Replace Client**, then restart the loopback proxy and verify the selected application's egress. Replacement authenticates the control request with the retained Owner, consumes the fresh Client invitation in memory, and replaces only the local Client after enrollment and protected persistence succeed. The SOCKS proxy never falls back to the Owner identity. If revocation fails, correct or retry the retained serial; if replacement fails, the previously stored local Client remains selected.

## Health and rollback

Health is limited to relay readiness, connected identities, active stream counts, and aggregate counters. If the agent is offline, new SOCKS requests fail; no fallback route occurs. To revoke access, revoke the affected Windows or Android certificate identity from owner mode; revocation rejects new sessions and closes active sessions for that identity. To fully halt service, stop the relay container and Android foreground service.

For ordinary image rollback without suspected state compromise, reuse the same state directory so current identities remain valid. If `deploy/data` or the relay CA private key might be exposed, do not reuse or merely revoke within that state: stop the relay, preserve a restricted forensic copy outside normal deployment, initialize a fresh state/CA, bootstrap a new Owner bundle, and re-pair every Windows and Android identity.

## Release handling

Release APK signing material, local relay state, certificates, and generated pairing codes remain outside Git. Before updating the relay, back up the mounted state directory; a non-compromise image rollback reuses the same state directory so enrolled identities remain valid. The Android release command refuses tracked signing properties and never displays its values; see [deployment and release](deployment.md) for the exact owner-only release and rollback sequence.
