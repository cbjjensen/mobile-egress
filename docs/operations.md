# Operations

Related documents: [deployment and release](deployment.md), [current status](status.md), [architecture](architecture.md), [protocol](protocol.md), [Windows client](../windows-client/README.md), and [Android Agent](../android/README.md).

Use this runbook after a relay and its artifacts have passed [cross-device acceptance](deployment.md#pairing-and-cross-device-acceptance). [Current status](status.md) remains the canonical validation and limitations ledger. Ordinary device use requires no scripts, provider credentials, or cloud credentials.

## Relay bootstrap

Initialization is the one-time deployment procedure in [Secure relay initialization](deployment.md#secure-relay-initialization), not a daily operation. The printed Owner invitation is the only shipped bootstrap path. If it is lost or expires before use, or the sole enrolled Owner later becomes lost, revoked, or expired, preserve relay and Windows state and escalate: the shipped UI has no Owner renewal flow and recovery requires relay-administrator intervention. Do not rerun initialization or delete state as an attempted renewal.

## Daily startup and shutdown

Start in dependency order:

1. Confirm the relay service is running and its public `/healthz` reports `readiness: true`. A ready relay can correctly report `agentConnected: false` before the phone starts.
2. Open Android and tap **Start**. The Agent must be paired, and its visible UI/foreground notification must reach cellular available / relay connected. It binds its own relay, DNS, and target sockets to cellular; it does not install a VPN or change unrelated phone traffic.
3. Open Windows and start the loopback SOCKS5 proxy. Copy its credential-bearing line only into the selected application.
4. Wait for Windows to show **Agent ready**, then test the selected application. Unconfigured applications keep their normal route.

Android starts only from the visible app action. Its foreground service is `START_NOT_STICKY` and there is no boot receiver, so a phone reboot or OS service termination requires the owner to open the app and tap **Start** again. After an explicit start, closing the Android activity does not itself stop the foreground service; use Android **Stop** or the notification's **Stop** action.

Closing the Windows window hides it to the notification tray. The local proxy and Client session continue. Use **Show Mobile Egress** to restore the window, **Stop proxy** to stop only the local listener/session, or tray **Quit** to stop the proxy, close active streams, and exit the application.

For a planned full shutdown, stop selected-application traffic, stop the Windows proxy, stop the Android Agent, and then stop the relay. Starting devices before the relay is safe but produces offline/retry states rather than usable egress.

## Health and rollback

Relay and desktop health answer different questions:

| Signal | Meaning | Does it prove usable cellular egress? |
| --- | --- | --- |
| Relay `/healthz` HTTP 200 with `readiness: true` | The TLS service can read aggregate persisted metrics. | No. `agentConnected` can still be false. |
| Relay `/healthz` HTTP 503 with `readiness: false` | The running service could not read aggregate persistence/metrics state. | No; relay-administrator investigation is required. |
| `agentConnected: true` | One Agent session is attached to the relay. | Not alone; the Windows Client and target stream must also succeed. |
| `connectedClients` | Number of active relay Client sessions. Owners are not counted. | No. |
| `activeStreams` | Relay-wide active streams across Clients. | No. It is not the single Windows listener's local count. |
| Windows **Relay connected · agent ready** / **Agent ready** | Its current Client tunnel is connected and the relay health poll sees readiness plus an Agent. | It is the correct precondition for a selected-application check, not proof of target reachability. |
| Windows **Relay connected · agent offline** | Its Client tunnel is connected but the relay reports no Agent. | No; new SOCKS requests fail closed. |
| Windows **Relay offline** | The current Windows Client tunnel is absent or disconnected. | No. When the proxy is stopped there is no Client tunnel, so this label does not independently prove the relay host is down. |

The public health response contains only aggregate readiness, connection/stream counts, cumulative byte/stream totals, and finite error counts. It is not an identity inventory, destination log, Prometheus endpoint, or SOCKS endpoint. Keep health output and screenshots free of adjacent secret material.

## Troubleshooting decisions

Use the row that matches the visible state. Do not erase state or reinitialize merely to clear an error.

| State | Supported checks and action | Boundary |
| --- | --- | --- |
| Relay down or unreachable | Confirm the reviewed public name still resolves to the relay ingress, the expected TCP TLS port is permitted, the exact HTTPS origin matches initialization, and the Compose relay/container is running with its state mount. Inspect only redacted runtime errors. | Port 8443 is TLS relay traffic, not SOCKS. Host, ingress, TLS, or state repair requires relay-administrator access. |
| Relay ready, Agent offline | Open Android, confirm it is paired, tap **Start**, and wait for cellular available / relay connected. If another phone owns the single active Agent session, stop it before retrying the intended phone. | Pairing a new Agent does not revoke an earlier Agent certificate. |
| Cellular unavailable | Restore working cellular data and leave Wi-Fi only as the deliberate no-fallback control. Wait for Android to select a new cellular network and reconnect before retrying traffic. | Wi-Fi is never an Agent fallback. The app cannot reset the carrier or guarantee IP rotation. |
| Windows shows relay offline | First confirm the proxy is actually started. If it is, verify the relay/public-origin checks above, then stop and start the proxy to create a new Client session. | The Windows session does not redial internally after that session closes; a stopped proxy also appears offline. |
| Owner ready, Client missing | In **Setup**, choose **Retry Windows client setup**. This asks the retained Owner to issue and consume a fresh local Client invitation. | **Replace Client** requires an existing local Client; retry is the supported partial-bootstrap action. Repeated failure with an invalid Owner needs escalation. |
| Local Client revoked, expired, or being rotated | Follow [Local Windows Client recovery](#local-windows-client-recovery) while the Owner identity is still valid. | The Owner identity never substitutes for the Client tunnel. |
| Agent revoked, expired, or otherwise unusable | With a valid Owner, generate a fresh QR and pair the intended phone, then tap **Start** and repeat device acceptance. | Routine re-pairing does not revoke the earlier Agent identity. Lost-phone targeted revocation is unavailable in the shipped UI. |
| Sole Owner revoked, expired, lost, or unusable | Stop making enrollment/revocation changes, preserve Windows and relay state, and contact the relay administrator/maintainer. | The UI has no Owner renewal/import recovery after bootstrap. Recovery requires relay-administrator intervention; no safe command is supplied here. |
| Owner invitation lost or expired before bootstrap | Preserve the initialized relay state and escalate rather than rerunning initialization or deleting state. | The initialization bundle has no self-service renewal flow and requires relay-administrator intervention. |
| Relay state/CA missing or suspected compromised | Stop the relay, preserve a restricted incident copy, and follow the reviewed cutover boundary in [deployment](deployment.md#state-and-ca-custody). | The UI cannot rotate or merge a trust root. Every identity must be re-enrolled after an approved cutover. |

If an identity error is ambiguous, do not guess certificate serials in the Owner revoke form. Relay v1 has no identity-list API, and the only certificate serial the shipped Windows UI exposes is its current local Client serial.

## Local Windows Client recovery

This is the complete shipped recovery path for the local Windows Client when the retained Owner still works:

1. Stop selected-application traffic and open **Owner** in the same Windows installation.
2. Record the displayed **Current local Client certificate serial** in the private incident record. This is only the local Client serial, not an Owner or Android Agent serial.
3. Enter that exact serial in **Revoke certificate**. Revocation uses the retained Owner authority and closes the old Client session. If the request fails, the form retains the serial so it can be checked and retried.
4. Choose **Replace Client**. The app uses Owner authority to issue and consume a new Client invitation in memory, persists the new Client, and stops any old proxy resources. It does not expose the invitation or use Owner credentials for SOCKS.
5. Start the proxy again, recopy the current SOCKS line if needed, and verify the SOCKS path produces selected-application carrier egress while an unconfigured application's route remains unchanged.

A failed replacement leaves the previously stored Client selected. If **Setup** shows Owner ready but no Client, use **Retry Windows client setup** instead. If Owner-authorized control calls fail because the sole Owner itself is revoked or expired, this flow cannot recover it and requires relay-administrator intervention.

## Agent re-pairing, QR exposure, and lost phones

For routine re-pairing while the Owner works, generate an Agent QR, scan it with Android **Scan QR**, and repeat foreground/cellular acceptance. Android commits its new local identity, but this does not revoke the prior certificate at the relay.

The Windows **Replace QR code** action changes only the displayed image. Each issuance creates another short-lived one-use capability; an earlier unconsumed code remains valid until consumed or expired. If a QR may have been exposed, do not assume replacement invalidates it. Stop displaying codes, allow all possibly exposed codes to expire, and escalate if an unknown party may have consumed one.

Lost-phone targeted revocation is **not supported through the shipped UI**. Android does not display its certificate serial, Windows displays only its local Client serial, and relay v1 has no identity-list endpoint. Routine Agent re-pairing does not revoke the old phone's Agent identity. A lost or stolen phone therefore needs relay-administrator/maintainer incident handling; this repository does not invent a serial-discovery or revocation command for that gap.

## Capacity and additional Windows devices

One shipped Windows Client admits **four local streams**. Its UI reports `Active / 4`, and a fifth local stream is rejected without increasing that limit. The relay allows four streams per Client session and eight streams relay-wide through the single Agent; Android independently caps the Agent at eight.

Eight is therefore not a single-Windows acceptance target. Reaching the relay/Agent-wide limit requires multiple Client sessions or a specialized maintained harness. The shipped UI does not create or import an additional Windows Client identity, so additional-Windows enrollment is not supported through the shipped UI and must not be presented as a normal operator procedure.

## Backup, update, and release decisions

Follow [State and CA custody](deployment.md#state-and-ca-custody) for quiescent backups and restores. A state backup includes the CA keys and identity/revocation database; protect it like live state. Do not restore individual files or use a stale state backup to roll back application code.

The supplied Compose file builds the relay from the current source checkout and has no pinned `image:` selection. Image-tag rollback is not implemented. `scripts/build-relay-image.ps1` creates a local tag that Compose does not reference. Any source/release rollback requires a separately reviewed deployment mechanism; there is no honest Compose rollback command to run from this repository.

For artifacts, use `scripts/release-android.ps1` for the guarded, signed, and verified Android release. Direct Gradle `assembleRelease` is insufficient for distribution. `scripts/build-windows.ps1 -Installer` produces an NSIS installer, but the repository has no Windows signing or publishing workflow. Link every artifact's version/hash and manual device result to the private acceptance record, and use [current status](status.md) rather than claiming automated end-to-end release proof.

## Explicit limitations and escalation boundary

| Condition | Shipped support | Escalation |
| --- | --- | --- |
| Additional Windows Client enrollment | Not supported through the shipped UI. | Maintainer-owned workflow required. |
| Lost-phone Agent serial discovery/revocation | Not supported through the shipped UI; re-pair does not revoke the old identity. | Relay administrator/maintainer incident handling required. |
| Sole Owner renewal or recovery | No self-service UI or relay command is documented. | Requires relay-administrator intervention. |
| State/CA rotation or compromise cutover | No UI cutover or in-place merge. | Requires relay-administrator intervention and full re-enrollment. |
| Source-checkout Compose image rollback | No pinned image tag or rollback selection. | Establish a reviewed release mechanism before relying on rollback. |
| Eight streams from one Windows Client | Unsupported; one Client is limited to four local streams. | Use a maintained multi-Client/harness acceptance environment, not the shipped UI. |
| Automated publishing or end-to-end release proof | No CI publishing, Android instrumentation run, Wails runtime test, or physical-device automation. | Complete and record the manual acceptance checklist. |
