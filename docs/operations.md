# Operations

## Normal use

`MobileEgressRelay` and each `MobileEgressClient` run as automatic LocalSystem services. Closing the controller window leaves it in the tray; quitting the controller does not stop those services. Start/stop the Android Agent only through its visible UI or foreground notification.

For a best-effort cellular address change, use **Rotate cellular IP** in the running Android Agent. Confirm stream disconnection, turn Airplane Mode on in the system screen the app opens, wait for the notification countdown, then turn it off. The Agent verifies and reconnects automatically. An unchanged result means the carrier reused the address; retry with the offered 30-second reset. Do not record the displayed addresses in acceptance evidence.

Refract running on a managed EC2 node opts in with that node's **Copy proxy line** value: `127.0.0.1:1081:<username>:<password>`. The listener forwards ordinary HTTP and uses CONNECT to carry end-to-end HTTPS without Mobile Egress decrypting it. SOCKS-aware software can instead use **Copy SOCKS5 URL** at `127.0.0.1:1080`. Both values work only on the same EC2 node; never configure an EC2 security group, Windows system proxy, or public proxy listener.

For ordinary HTTP, the Client reuses healthy destination connections instead of opening a new phone/relay stream for every request. It keeps no more than two idle destination streams and expires them after 15 seconds. These pooled and active connections still count toward the existing four-stream Client limit; there is no additional capacity tier.

## Controller actions

- **Install Client** validates signed manifest v2 and its embedded publisher certificate, then verifies the GitHub artifact SHA-256 and exact Authenticode signer before installing a LocalSystem service and provisioning a node identity.
- **Update** repeats the same release/trust checks and replaces the executable while retaining node keys, certificate, and proxy credentials. Nodes older than Client `1.0.24` must be updated before the full HTTP/HTTPS proxy line is enabled.
- **Repair** performs the signed update and sends a fresh sealed copy of the existing configuration.
- **Copy proxy line** reveals the authenticated HTTP forward/CONNECT line for Refract; **Copy SOCKS5 URL** reveals the alternate SOCKS form. Neither copied value is written to activity logs.
- **Rotate endpoint safely** appears when the Tailscale Funnel origin differs from the encrypted Owner origin. Connect AWS first whenever managed nodes exist.
- **Repair local relay** re-verifies the signed sibling relay, reapplies protected state ACLs, repairs the LocalSystem service configuration, and starts it without changing the CA or identities.

Client installation reserves one of the ten encrypted controller slots before provisioning. The controller writes a recoverable `configuring` record before sending the sealed configuration and commits it to `installed` only after the node service restarts successfully. Endpoint rotation uses the same write-before-apply rule for its desired URL and generation. If either action times out and the node appears in the managed list, use **Repair**; it safely reapplies that desired generation and credentials. If the controller itself exited before the node appeared, choose **Install Client** for that same instance again to resume its durable reservation. A different instance cannot consume the reserved slot.

Only one controller process may run for a Windows user/machine installation; launching it again activates the existing window. If a reserved EC2 instance was terminated or can no longer be recovered, use **Interrupted install reservations → Cancel reservation**, read the warning, and confirm explicitly. Cancellation releases only the local capacity reservation; it does not terminate an instance, revoke a certificate, or mutate AWS.

## EC2 publisher trust bootstrap

The signed controller is the authority for node-release manifest v2. It validates the embedded public certificate and release metadata locally before sending SSM any command. On the node, a bounded machine-global mutex serializes the complete release transaction; lock timeout fails before download, and an abandoned mutex is safely acquired. SSM then performs this fixed order: download the exact GitHub HTTPS artifact; verify its pinned SHA-256; reconstruct and hash the embedded public DER; require the pre-trust Authenticode signer to carry those exact certificate bytes; inspect every same-thumbprint store entry; add that certificate to `LocalMachine\Root` and `LocalMachine\TrustedPublisher` only where absent; require post-trust Authenticode `Valid` with the same certificate; then install or update the service. Windows Server 2019's pre-trust `UnknownError` is accepted only after the artifact hash and exact signer bytes match; it remains a hard failure after trust. ACL, PowerShell service-management, and Client bootstrap failures are checked immediately.

An already trusted exact certificate is a normal idempotent case. While holding the mutex, the command records which stores were initially absent and confirms each import. If a later step or partial import fails, it removes exact DER only from those initially absent stores. It never removes a pre-existing entry or a different certificate. Do not manually import a certificate downloaded beside the release, bypass the checks, run overlapping manual installers, or clear either certificate store. Publisher replacement or compromise requires a separately reviewed trust-removal operation on every node.

The public publisher DER/fingerprints and signed-release URL/hash may appear in SSM input or logs. Private signing material, its password, proxy credentials, pairing values, private keys, and plaintext sealed configuration must not. Success output remains bounded public bootstrap JSON or fixed update JSON. Failed release commands return only an allowlisted stage marker, which the controller maps to a fixed user-facing label; raw SSM stdout/stderr remains redacted.

## Endpoint rotation runbook

1. Restore Tailscale login. The controller verifies that Funnel has an enabled `*.ts.net:8443` raw-TCP mapping to `127.0.0.1:8443`; use **Repair Funnel and local relay** if that mapping was reset.
2. Connect AWS in the controller if any nodes are managed.
3. Choose **Rotate endpoint safely** and approve UAC. The helper rotates the relay leaf certificate under the existing CA and restarts `MobileEgressRelay`.
4. Review the returned updated/failed node list. Use **Repair** for failures after SSM is online.
5. On the existing Android app, stop the Agent, choose **Scan QR**, and scan the displayed endpoint-migration QR. Restart the Agent.
6. Confirm workloads reconnect. No device key, Client serial, Agent certificate, or SOCKS credential should change.

The QR is one-use and expires after ten minutes. It is distinct from enrollment and cannot migrate an Agent belonging to a different CA.

## Troubleshooting

| Symptom | Check | Safe response |
|---|---|---|
| Tailscale not installed | The controller cannot find the fixed official CLI path | Choose **Install Tailscale**, approve UAC, then use **Connect Tailscale** if sign-in is still required. |
| Tailscale installed but not connected | The controller found Tailscale but its CLI did not report an online `*.ts.net` identity | Choose **Connect Tailscale**. Complete browser sign-in and check internet/service health if it remains offline. Do not rerun the MSI. |
| Windows Installer code 1603 while installing Tailscale | First confirm whether `C:\Program Files\Tailscale\tailscale.exe` already exists | If it is already installed, use **Connect Tailscale**; current controllers suppress duplicate MSI installation. Investigate MSI/UAC only when Tailscale is genuinely absent. |
| New SSM profile cannot be attached | Activity remains in **Preparing SSM** while AWS propagates the new IAM profile | Wait for the controller's automatic bounded retries. It rechecks the instance before each attempt and stops rather than replacing an unrelated profile. If it reports AWS permission denied, verify `iam:PassRole` and `ec2:AssociateIamInstanceProfile`; do not delete or replace profiles manually. |
| Bridge setup required after Tailscale is online | Check the automatically opened Funnel approval page, WebView2, and signed sibling binaries | Approve the official `login.tailscale.com/f/funnel` page and subsequent relay UAC prompt. If no page opens, preserve the app error and controller version for diagnosis; do not run a manual Funnel script or expose port 8443. |
| Tailscale reports Windows Installer code 1632 | Verify the user/System temp directories and `%windir%\Installer` exist and are writable; preserve the MSI verbose log | Treat a missing Windows Installer cache as an operating-system repair issue. Do not have Mobile Egress silently recreate or repopulate it; cached packages are machine-specific and require supported recovery or system-state restoration. |
| Rotation required | Current `*.ts.net` name differs from stored Owner endpoint | Connect AWS, rotate, repair failed nodes; Repair reuses the persisted desired endpoint/generation. Scan the migration QR. |
| Interrupted reservation | Controller exited before recoverable node metadata was committed | Retry Install on the same instance, or explicitly cancel the reservation only if that instance is gone/unrecoverable. |
| Agent offline | Android foreground service, cellular availability, battery restrictions | Start from visible UI; restore cellular. Wi-Fi is intentionally not a fallback. |
| Cellular IP unchanged | Rotation completed but the carrier reused the comparable IPv4/IPv6 address | Use the offered 30-second retry. Reassignment is carrier-controlled and cannot be guaranteed. |
| Rotation waiting for Airplane Mode | System settings is open but cellular never disappeared | Turn Airplane Mode on manually or return to the Agent and cancel. It cancels automatically after two minutes. |
| Rotation waiting for cellular | Airplane Mode remains on, mobile data is unavailable, or carrier attachment is slow | Turn Airplane Mode off and restore cellular. After three minutes the Agent returns to normal waiting behavior and reconnects whenever cellular appears. |
| IP result unverified | ipify was unreachable or no address family succeeded both before and after | Proxy traffic still remains cellular-only. Retry later; do not treat the result as proof that the address changed. |
| Node missing | Region, running Windows Server 2019 x86-64 image, AWS authorization | Use `us-east-1`; the app intentionally filters other nodes. |
| SSM remains unregistered after profile setup | An already-running SSM Agent can retain its earlier no-credential state after a profile is attached | Allow the 30-second passive check. If the card offers **Restart EC2 and continue**, confirm only when a brief server interruption is acceptable. The controller reboots only the selected instance, waits for a fresh Agent ping, then installs automatically. If it still times out, inspect the sanitized registration/Agent-version/last-ping events and check the Windows `AmazonSSMAgent` service plus outbound HTTPS/DNS. Do not open inbound ports. |
| EC2 restart request denied | The AWS identity lacks `ec2:RebootInstances` | Add that action to the dedicated Mobile Egress IAM policy or use an authorized identity, then retry the explicit recovery button. The app never silently reboots, stops, terminates, or recreates an instance. |
| Install/update rejected before SSM | Manifest v2 shape; certificate DER bound/parse; self-signature; Code Signing EKU; CA=false; validity; SHA-1/SHA-256 consistency | Rebuild from the established tracked CER and current signing identity. Do not edit the manifest or initialize a replacement identity. |
| Install/update rejected on node | Banner's sanitized stage; GitHub artifact hash; pre-trust exact signer; `LocalMachine\Root` and `TrustedPublisher`; post-trust `Valid` | Use the stage label to identify download, trust, service, or bootstrap failure. Publish/use the exact signed artifact and retry after correcting the release or SSM health; rollback is attempt-scoped. Never bypass verification or clear certificate stores. |
| Refract rejects the proxy line | Refract and Client run on the same EC2 node; Client version is `1.0.24` or later; both an `http://` and an `https://` test work through loopback port `1081` | Choose **Update**, then **Copy proxy line** again. Use `IP:PORT:USERNAME:PASSWORD` exactly and do not expose port 1081 through AWS or Windows Firewall. |
| Proxy authentication fails | Credentials copied for the same node; Client service running | Copy the appropriate HTTP line or SOCKS5 URL again, or use **Repair**. Do not put credentials in SSM commands. |
| Stream rejected | Four-stream per-Client or 32-stream aggregate bound, target policy, Agent loss | Reduce concurrency or restore Agent/cellular. Do not increase queues ad hoc. |

## IAM behavior

With no instance profile, the app creates a deterministic dedicated SSM role/profile and associates it only after a second read confirms the instance still has no profile. If the deterministic profile already contains any unexpected role, it stops without changing it. With an existing role lacking SSM, it requires explicit confirmation and attaches only `arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore`. It never replaces or detaches an existing profile.

## Revocation and replacement

Owner control can revoke a known certificate serial. Revocation blocks new authenticated sessions but does not recover compromised relay state or delete provider/cloud resources. Replacing an EC2 node identity is a deliberate reinstall/reprovision operation and should be followed by revoking the previous serial. Routine update, repair, reboot, and endpoint migration preserve identity.

## Backup and incident boundary

Relay state lives in `C:\ProgramData\MobileEgress\Relay`; node state lives in `C:\ProgramData\MobileEgress\Client`. Both directories are ACL-restricted. Only the relay directory should be backed up centrally, as one quiescent unit while the service is stopped. Node identities can be deliberately reprovisioned.

If relay state/CA, Owner DPAPI state, or the signing key may be compromised, stop affected services, preserve a restricted incident copy, revoke/publish artifacts as appropriate, and perform a reviewed full trust reset. Tailscale logout, leaf endpoint rotation, or stale-state restore is not sufficient.
