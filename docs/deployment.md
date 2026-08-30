# Deployment and release

Related documents: [operations](operations.md), [current status](status.md), [architecture](architecture.md), [security model](security-model.md), [Windows client](../windows-client/README.md), and [Android Agent](../android/README.md).

This runbook covers the supplied source-checkout Compose deployment and owner-controlled Windows and Android artifacts. It does not publish a public SOCKS service or provide an automatic update channel. [Current status](status.md) is the canonical record of automated validation and known product limitations.

## Relay host readiness

Complete these checks before initialization:

- Use a maintained host with Docker Engine and the Docker Compose plugin. From the repository root, `& .\scripts\preflight.ps1 -Components Docker` is the supplied read-only prerequisite check; it does not install Docker.
- Restrict interactive and backup access to the host. The bind-mounted relay state contains a private CA key, relay key, certificates, and SQLite identity/capability state. These are filesystem-protected operational secrets, not encrypted-at-rest application data.
- Choose the public endpoint before initialization. `RELAY_PUBLIC_NAME` is the DNS name or IP placed in the relay certificate. `RELAY_PUBLIC_URL` is the exact HTTPS origin placed in invitations. Its hostname must equal `RELAY_PUBLIC_NAME`, and its explicit port, when present, must be the port clients actually reach.
- Make the public name resolve to this relay ingress. Permit the selected TCP TLS port through host and network firewalls. With the supplied environment, public TCP 8443 maps to container TCP 8443.
- Preserve relay-generated TLS end to end. An ingress may TCP-forward the connection, but substituting a different TLS certificate breaks the invitation-pinned relay CA trust used by Windows and Android.
- Do not route SOCKS traffic to the relay port. Port 8443 carries the relay's HTTPS and WebSocket TLS protocol. SOCKS5 exists only on the authenticated Windows listener at `127.0.0.1`.
- Prepare an access-controlled, versioned backup destination for the complete `deploy/data` directory. A backup is as sensitive as the live state and must not be placed in a general sync folder or source control.

The container command listens on `RELAY_LISTEN`, while Compose publishes `${RELAY_PORT}:8443`. The supplied configuration therefore keeps the container listener at `0.0.0.0:8443`; changing only one side of that mapping makes the relay unreachable.

## Secure relay initialization

The supplied Compose file builds both relay services from the current repository checkout. It does not select a pinned relay image or release tag.

1. Run `& .\scripts\preflight.ps1 -Components Docker` from the repository root.
2. Copy `deploy/.env.example` to the ignored `deploy/.env`. Set the reviewed `RELAY_PUBLIC_NAME` and `RELAY_PUBLIC_URL`; leave the container listener aligned with the Compose mapping. Do not place credentials, pairing bundles, certificates, or relay state in this file.
3. Confirm DNS, the public TCP port, and the exact external HTTPS origin before creating the CA. Initialization binds the generated relay certificate and every invitation to those values.
4. Initialize the empty state once:

   ```powershell
   docker compose -f deploy/docker-compose.yml --profile init run --rm relay-init
   ```

5. Capture the single printed Owner pairing bundle directly into an owner-controlled password manager. The bundle contains the CA trust anchor and a high-entropy, one-use capability and expires after ten minutes. Do not paste it into shell history, tickets, chat, logs, screenshots, or source control.
6. Start the relay:

   ```powershell
   docker compose -f deploy/docker-compose.yml up -d relay
   ```

7. Confirm the container becomes healthy and the public `GET /healthz` origin returns aggregate JSON with `readiness: true`. `agentConnected: false` is expected before the Android Agent starts. Health does not prove physical ingress, device pairing, or cellular egress; complete the acceptance checklist below.

The Owner invitation is an initialization output, not a renewable UI credential. If the only bundle is lost or expires before the first Owner enrollment, rerunning initialization is not a supported renewal procedure. Recovery requires relay-administrator intervention; the repository does not supply a safe in-place Owner renewal command.

The first Windows installation pastes the Owner invitation and then automatically enrolls a separate local Client identity. The shipped UI does not create or import an additional Windows Client identity, so multi-Windows enrollment is not a supported app-first deployment. Do not invent or manually transfer a Client invitation outside a maintained recovery procedure.

## State and CA custody

Compose mounts `deploy/data` at `/var/lib/mobile-egress`; initialized CA, relay TLS, and SQLite files are under `deploy/data/state` on the host. Protect and back up the complete mounted directory as one unit. Do not copy, restore, or rotate individual CA, key, certificate, or database files independently.

For a consistent backup:

1. Schedule an outage and stop selected-application traffic, the Windows proxy, and the Android foreground Agent.
2. Stop the `relay` Compose service so SQLite and key state are quiescent.
3. Use the host's approved protected backup mechanism to copy the complete `deploy/data` directory with its access controls. Do not print or inspect secret file contents.
4. Record the backup timestamp, deployment source commit, public origin, and backup identifier in the private operations record. Keep the backup under access controls equivalent to the live host.
5. Start the same `relay` service and confirm `/healthz` reports `readiness: true`.
6. Open Android and tap **Start**.
7. Start the Windows loopback proxy.
8. Wait for Windows to show **Agent ready**.
9. Verify the selected application uses carrier egress and an unconfigured application's route remains unchanged.

Restoring a known-good backup is a relay-administrator operation: stop the relay, preserve the current state as a restricted incident copy, restore the complete matching `deploy/data` set, and retain its protections. Then verify the restore in this order:

1. Start the `relay` service and confirm `/healthz` reports `readiness: true`.
2. Open Android and tap **Start**.
3. Start the Windows loopback proxy.
4. Wait for Windows to show **Agent ready**.
5. Verify the selected application uses carrier egress and an unconfigured application's route remains unchanged.

A stale state restore can reverse revocations or capability consumption, so it is not an application-code rollback mechanism.

Do not improvise a state/CA cutover. If state is lost, the CA key is unavailable, the CA or database may be compromised, or a sole Owner identity is lost, revoked, or expired, the UI cannot recover the trust root. Keep the relay stopped when compromise is suspected, preserve a restricted forensic copy, and escalate to the relay administrator and maintainer. A reviewed cutover must create an empty state/CA, bootstrap a new Owner, and re-enroll every identity; no shipped command safely merges or rotates the old trust state in place.

## Windows and Android releases

Build only from a reviewed source commit. For each artifact, the private release record must include the source commit, application/release version, filename, SHA-256 hash, build time, and acceptance result. Do not include signing passwords, certificates, pairing material, SOCKS credentials, or relay state in that record.

### Relay image

`& .\scripts\build-relay-image.ps1` runs the Docker prerequisite check and builds `mobile-egress-relay:local` unless a different tag is supplied. That locally tagged image is not referenced by `deploy/docker-compose.yml`; the supplied Compose deployment has its own source `build` stanza. Record which workflow produced a deployed image rather than assuming the local tag controls Compose.

### Windows installer

Run:

```powershell
& .\scripts\build-windows.ps1 -Installer
```

The script checks Go, Node.js, and WebView2, then asks the pinned Wails CLI to build an NSIS installer under `windows-client\build\bin`. The repository has no Windows code-signing or publishing workflow, and `wails.json` does not declare an application version. Treat the installer as an owner-controlled, manually distributed artifact; use an immutable release label plus the source commit as its version identifier, record its SHA-256, and exercise it on a non-primary Windows profile before acceptance. Do not substitute the frontend package version for an installer version. WebView2 remains a runtime prerequisite.

### Signed Android APK

Generate and back up a dedicated release keystore outside the checkout. Copy `android\keystore.properties.example` to the ignored `android\keystore.properties`, fill it locally, and confirm it remains untracked. Then use the guarded repository path:

```powershell
& .\scripts\release-android.ps1 -ValidateOnly
& .\scripts\release-android.ps1
```

`-ValidateOnly` checks only that required signing inputs exist and remain ignored. The full script also runs Android preflight, assembles the release APK, and verifies `android\app\build\outputs\apk\release\app-release.apk` with Build-Tools 35 `apksigner` without printing signing values.

Direct `gradlew.bat assembleRelease` is insufficient for a distributable artifact: it does not perform the repository's tracked/ignored signing-input guards, prerequisite flow, or final `apksigner` verification. Record the Android `versionName`/`versionCode`, APK SHA-256, and signing-key identity in the private release record. Losing the key prevents seamless updates; exposing it permits a malicious replacement APK.

Neither release script publishes an artifact. Transfer accepted artifacts only through an owner-controlled channel and verify the recorded filename and hash at the receiving device.

## Pairing and cross-device acceptance

1. Install the recorded Windows NSIS artifact and verified signed Android APK through owner-controlled channels.
2. On the first Windows installation, paste the confidential Owner invitation. Confirm **Setup** reports both Owner and local Windows Client ready.
3. In **Phone**, generate one short-lived Agent QR. Keep it on the trusted local display and scan it with Android **Scan QR**. Android has no invitation paste flow.
4. Tap Android **Start**, grant notification permission when requested, and wait for cellular available / relay connected.
5. Start the Windows loopback proxy and copy its generated `socks5://username:password@127.0.0.1:port` line only into the selected application. Do not set a Windows system proxy or change either device's default route.

**Replace QR code** replaces only the QR shown in the Windows UI. It does not revoke a previously issued unexpired code. Treat every displayed code as secret until it is consumed or expires; generating another code can leave both codes valid during their respective lifetimes.

Do not promise IP rotation. Reconnecting the Agent can restore a cellular-bound relay session, but the application cannot reset carrier data or guarantee a different carrier address.

## Required physical-device checklist (still required; not executed by automated verification)

Use an Android 10+ phone with working cellular data and an owner-controlled Windows 10/11 machine. Link the completed record to the artifact version/hash entry and to the validation boundary in [current status](status.md). Store only aggregate outcomes and redacted finite error classes.

- [ ] Install the recorded Windows installer and signed APK. Confirm the received filenames and SHA-256 hashes match the private release record.
- [ ] Complete Owner/Client setup, generate one fresh Agent QR, scan it on Android, tap **Start**, and confirm the foreground notification reports cellular available / relay connected.
- [ ] Keep Wi-Fi connected, configure the SOCKS line in exactly one test application, and compare an external IP check. Only the proxy-configured application must report the phone carrier address; an unconfigured application must retain its normal path.
- [ ] Disable cellular while Wi-Fi remains available. Active proxy streams must close and new requests must fail; no request may fall back to Wi-Fi. Restore cellular and wait for Agent reconnection before retrying.
- [ ] Confirm the one-Agent-session rule and the Agent-offline fail-closed state without treating re-pairing as revocation of an earlier Agent identity.
- [ ] Attempt private, loopback, link-local, multicast, and reserved destinations through SOCKS. Each must be denied without making a target connection.
- [ ] Record the current local Windows Client serial in **Owner**, revoke that serial with Owner authority, choose **Replace Client**, restart the proxy, and verify selected-application egress.
- [ ] Use a controlled public TCP destination that returns a response and EOF. Confirm the complete response arrives before the stream closes.
- [ ] Trigger local-Client and relay/Agent closes nearly together. Confirm close handling is idempotent and unrelated streams continue.
- [ ] From the single shipped Windows Client, run up to four local streams and confirm a fifth is rejected without starving the active streams. Eight streams is Agent/relay-wide capacity and cannot be exercised by one Windows Client, whose local limit is four.
- [ ] Stop and start the Android Agent from its visible UI/notification controls, and close and restore the Windows window from the tray. Confirm only tray **Quit** fully exits Windows and stops its proxy.
- [ ] Review Windows, Android, relay, and container logs. Verify they contain only aggregate state, counts, byte totals, and finite redacted errors—never destinations, payloads, credentials, pairing bundles, capabilities, certificates, or keys.

Lost-phone targeted revocation is not part of this checklist because it is not supported through the shipped UI: Android does not display its certificate serial and relay v1 has no identity-list endpoint. Routine Agent re-pairing does not revoke the earlier Agent identity. The relay/Agent-wide eight-stream ceiling likewise requires a specialized maintained harness or multiple Client sessions; the shipped UI provides neither additional-Windows Client enrollment nor a one-Client path above four local streams.

## Rollback and revocation

Before any relay update, create and verify a protected, quiescent backup as described above. Preserve the same uncompromised state/CA across a normal code update so enrolled devices retain trust.

The supplied source-checkout Compose file does not implement image-tag rollback. It builds from `..` and has no pinned `image:` reference, and the image built by `scripts/build-relay-image.ps1` is not selected by Compose. Therefore, “deploy the previous image” is not an executable procedure for this deployment. Plan and validate a source/release rollback mechanism outside this runbook before relying on one; do not substitute a stale state restore for a code rollback.

If relay state or its CA private key may have been exposed, do not restart from it, reuse its CA, or rely on revocation inside the compromised state. Stop service, preserve a restricted forensic copy, and invoke the reviewed relay-administrator/maintainer cutover described under [State and CA custody](#state-and-ca-custody). All Windows and Android identities must be re-enrolled under the new CA.
