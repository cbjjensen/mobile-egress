# Windows and Mac workload Clients

AWS is optional. A workload machine uses the installed Mobile Egress Client to reach the owner's personal-computer relay through Tailscale Funnel, then the Android or iOS Agent and cellular Internet. The workload machine needs outbound HTTPS to the Funnel endpoint; it needs neither AWS credentials nor Tailscale. EC2 remains an optional workload/SSM management path. The Mac build server is development infrastructure, never a traffic relay.

This describes the implemented source and next guarded Desktop release. It does not claim that new signed installers have been published or physically accepted. See the [implementation and validation record](superpowers/plans/2026-10-03-aws-optional-windows-mac-clients.md).

## Install and pair

Supported targets are x64 Windows 10/11 or Windows Server 2019+, and Apple Silicon macOS 13+. Linux and Intel Macs are deferred.

1. On the personal controller, complete **Set up this computer**, pair the Agent, and start cellular sharing. Leave this controller open during Client pairing.
2. On the workload machine, obtain the accepted `MobileEgressClientSetup.exe` or `mobile-egress-client-macos-<version>-arm64.pkg` from the official release. These are different from the controller installers. Use the existing Windows publisher verification or normal macOS Developer ID/Gatekeeper checks. The Mac workload owner must be logged in for the first graphical installation so the installer can bind that account.
3. On the controller, open **Clients → Add Windows/Mac Client**, name the machine, and create an invitation. Paste it into the workload's **Mobile Egress Client** app within ten minutes. Invitations are secret, one-use capabilities; ordinary status never includes them.
4. Wait for the Client app to finish pairing and the controller to acknowledge its configuration. Both apps distinguish saved installation/configuration from a live relay connection. Keep the personal relay and phone available.
5. Copy the HTTP proxy line or SOCKS5 URL into the intended application on that same workload machine. Send a request and verify the connection. No system-wide proxy, default route, inbound firewall rule, or remote proxy listener is installed.

| Workload platform | HTTP / CONNECT | SOCKS5 |
|---|---|---|
| Windows | `127.0.0.2:1081` | `127.0.0.2:1080` |
| Mac | `127.0.0.1:1081` | `127.0.0.1:1080` |

Copy actions use the selected workload's address, regardless of the controller platform. Credentials appear only during explicit copy actions. An occupied proxy port produces an error: stop the conflicting local process and retry; the Client never silently chooses another address or port.

## Background operation and repair

The Windows Client runs as the `MobileEgressClient` LocalSystem service. Its keys and configuration use service-account DPAPI under the protected Client state directory. The GUI accesses narrow operations over a local-only named pipe restricted to SYSTEM and the recorded installing user. It verifies the pipe's SYSTEM owner before sending an invitation. The standalone installer rejects an existing AWS-managed installation instead of overwriting its service.

The Mac Client runs as root LaunchDaemon `com.zfnf.mobile-egress.client`. It uses a distinct Client namespace in `/Library/Keychains/System.keychain`, explicitly selecting the file-based implementation required for daemons outside user sessions. Its item ACL restricts access to the signed Client daemon; unavailable storage fails closed. The GUI uses a restricted Unix socket and verifies the root service peer. The daemon verifies the recorded workload owner UID or root. There is no login-Keychain or plaintext fallback. See [Apple TN3137](https://developer.apple.com/documentation/technotes/tn3137-on-mac-keychains).

Once the workload Mac has booted and networking is available, the Client is designed to start without its GUI and continue after logout. FileVault/unattended boot policies still belong to macOS. This does not change the personal Mac relay's existing dependency on its controlling user and per-user Tailscale remaining available.

Install an accepted signed update on the workload machine to update or repair the standalone Client. Existing pairing, keys, credentials, and owner binding are retained. No remote software maintenance or SSH provisioning is provided. AWS-managed Clients retain their existing **Update** and **Repair** operations through SSM. Never delete protected state or reset a Keychain to repair a connection.

Revocation closes active traffic and prevents reconnecting with that identity. The service fails closed even if it cannot persist its revocation receipt. This version deliberately has no destructive identity-reset UI; installer repair does not undo revocation.

## Interrupted pairing

The controller reserves a stable Client ID before requesting an invitation. The relay and workload durably retain the public bootstrap/key binding, and approval issues one identity. The controller saves its encrypted metadata and the exact initial sealed configuration before delivery. Only a configuration receipt marks installation complete. Retrying lost responses or restarting either process reuses these records.

Use **Show invitation** to resume a saved attempt and **Cancel pairing** to release a canceled attempt after the relay confirms cancellation. Unredeemed invitations expire after ten minutes and cannot issue a new identity. An already approved delivery can be retried for its bound key; an acknowledgement lost after durable installation can be resent. Neither retry occupies a second slot.

If the Funnel origin changes during setup, first rotate the bridge in the controller. For an unfinished bootstrap, show and paste the refreshed invitation again: only its endpoint changes, while its capability, CA, ID, expiry, bootstrap, and local keys remain the same. After the original configuration arrives, import the latest connection update from the controller. If that attempt has expired before identity approval, cancel it and create a fresh invitation.

For an approved attempt, **Show invitation** can reveal a resume-only invitation after its original deadline. It works only in the Client that already persisted the exact bound bootstrap and keys; the service polls the existing enrollment rather than making a new submission. The original deadline is never extended and other Clients cannot redeem it.

## Funnel changes and AWS outages

**Rotate endpoint safely** saves the desired origin and generation for the entire fleet before issuing the Agent migration QR. Scan that QR using the existing Android/iOS migration flow. Standalone Clients then use **Copy connection update** in the controller and **Import connection update** in the Client app. The sealed package is bound to the Client's key and enrollment; it changes only the relay endpoint/generation and rejects changed credentials, certificates, CA, identity, stale generations, and incompatible formats. A Client that missed several changes can import the latest generation directly. Status remains pending until the relay receives its applied-generation report.

AWS delivery runs separately with bounded attempts and a retry delay. Missing or expired AWS credentials do not block pairing, status, proxy copies, or exporting/importing a standalone update. Reconnect AWS to resume EC2 management. Older EC2 Clients require consecutive generations, so the controller retains and replays their exact endpoint history in order through SSM, saving each receipt before advancing. Lost command responses are retried idempotently. The encrypted queue is bounded to 256 updates/64 KiB per Client; recover or revoke a permanently unavailable Client before exhausting that history.

If an older controller already overwrote several unresolved generations before this feature was installed, that missing history cannot be reconstructed. Such a legacy Client remains pending rather than silently resetting its identity or credentials.

## Registry and control interfaces

Encrypted controller schema v3 preserves existing EC2 instance IDs, certificates, credentials, generations, and reservations. Each Client has a stable ID, display name, platform, architecture, and `aws-ssm` or `paired` management method. Installed state and live connection state are separate. The combined maximum is ten Clients, including reservations and unfinished pairings; legacy and standalone relay admission share that limit.

Generic desktop bindings are `IssueClientInvitation`, `ClientInvitation`, `CancelClientInvitation`, `RefreshClients`, `ClientProxyLine`, `ClientSOCKSProxyURL`, `ExportClientEndpointUpdate`, and `RevokeClient`. Existing EC2 and proxy bindings remain compatible. New bounded relay control requests use Owner mTLS or the scoped enrollment capability; status reports use the enrolled Client identity. The relay stores public bootstrap material and sealed ciphertext, never workload private keys or plaintext proxy configuration.

The Agent enrollment/migration formats and traffic wire protocol are unchanged. Each Client still uses one tunnel session. The mobile feature manifest is validated without changing Android/iOS feature evidence for this controller/Client-only feature.

## Release and acceptance

The guarded Desktop release adds the standalone Windows setup and signed/notarized Mac Client PKG while preserving the raw `mobile-egress-client.exe`, existing publisher identity, and immutable historical assets. Build support is not publication or physical acceptance.

Before accepting a release, record no-AWS graphical installation and HTTP/CONNECT/SOCKS traffic on a non-AWS Windows workload and Apple Silicon Mac. Exercise boot/logout, signed upgrade/repair, unavailable secure storage, port conflicts, invitation expiry/reuse, interrupted pairing/lost receipts, concurrent admission at ten, revocation, mixed EC2 fleets with AWS unavailable, and several missed endpoint changes. Repeat traffic with Android, iOS, and older EC2 Clients. Live System Keychain ACL/upgrade acceptance must use the signed root daemon, not an unsigned test substitute.
