# Operations

## Normal use

`MobileEgressRelay` and each `MobileEgressClient` run as automatic LocalSystem services. Closing the controller window leaves it in the tray; quitting the controller does not stop those services. Start/stop the Android Agent only through its visible UI or foreground notification.

An EC2 application opts in with its node-specific `socks5://<user>:<password>@127.0.0.1:1080` value. Never configure an EC2 security group, Windows system proxy, or public listener for SOCKS.

## Controller actions

- **Install Client** validates signed manifest v2 and its embedded publisher certificate, then verifies the GitHub artifact SHA-256 and exact Authenticode signer before installing a LocalSystem service and provisioning a node identity.
- **Update** repeats the same release/trust checks and replaces the executable while retaining node keys, certificate, and SOCKS credentials.
- **Repair** performs the signed update and sends a fresh sealed copy of the existing configuration.
- **Copy credentials** is the only normal action that reveals SOCKS authentication.
- **Rotate endpoint safely** appears when the Tailscale Funnel origin differs from the encrypted Owner origin. Connect AWS first whenever managed nodes exist.
- **Repair local relay** re-verifies the signed sibling relay, reapplies protected state ACLs, repairs the LocalSystem service configuration, and starts it without changing the CA or identities.

Client installation reserves one of the ten encrypted controller slots before provisioning. The controller writes a recoverable `configuring` record before sending the sealed configuration and commits it to `installed` only after the node service restarts successfully. Endpoint rotation uses the same write-before-apply rule for its desired URL and generation. If either action times out and the node appears in the managed list, use **Repair**; it safely reapplies that desired generation and credentials. If the controller itself exited before the node appeared, choose **Install Client** for that same instance again to resume its durable reservation. A different instance cannot consume the reserved slot.

Only one controller process may run for a Windows user/machine installation; launching it again activates the existing window. If a reserved EC2 instance was terminated or can no longer be recovered, use **Interrupted install reservations → Cancel reservation**, read the warning, and confirm explicitly. Cancellation releases only the local capacity reservation; it does not terminate an instance, revoke a certificate, or mutate AWS.

## EC2 publisher trust bootstrap

The signed controller is the authority for node-release manifest v2. It validates the embedded public certificate and release metadata locally before sending SSM any command. On the node, a bounded machine-global mutex serializes the complete release transaction; lock timeout fails before download, and an abandoned mutex is safely acquired. SSM then performs this fixed order: download the exact GitHub HTTPS artifact; verify its pinned SHA-256; reconstruct and hash the embedded public DER; require the intact pre-trust Authenticode signature to carry those exact certificate bytes; inspect every same-thumbprint store entry; add that certificate to `LocalMachine\Root` and `LocalMachine\TrustedPublisher` only where absent; require post-trust Authenticode `Valid` with the same certificate; then install or update the service. Every `icacls.exe`, `sc.exe`, and Client bootstrap exit code is checked immediately.

An already trusted exact certificate is a normal idempotent case. While holding the mutex, the command records which stores were initially absent and confirms each import. If a later step or partial import fails, it removes exact DER only from those initially absent stores. It never removes a pre-existing entry or a different certificate. Do not manually import a certificate downloaded beside the release, bypass the checks, run overlapping manual installers, or clear either certificate store. Publisher replacement or compromise requires a separately reviewed trust-removal operation on every node.

The public publisher DER/fingerprints and signed-release URL/hash may appear in SSM input or logs. Private signing material, its password, SOCKS credentials, pairing values, private keys, and plaintext sealed configuration must not. Success output remains bounded public bootstrap JSON or fixed update JSON; controller errors remain redacted.

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
| Bridge setup required | Tailscale service/login, Funnel approval, WebView2, signed sibling binaries | Reopen the app, install/connect Tailscale, then retry with UAC. Do not manually expose port 8443. |
| Rotation required | Current `*.ts.net` name differs from stored Owner endpoint | Connect AWS, rotate, repair failed nodes; Repair reuses the persisted desired endpoint/generation. Scan the migration QR. |
| Interrupted reservation | Controller exited before recoverable node metadata was committed | Retry Install on the same instance, or explicitly cancel the reservation only if that instance is gone/unrecoverable. |
| Agent offline | Android foreground service, cellular availability, battery restrictions | Start from visible UI; restore cellular. Wi-Fi is intentionally not a fallback. |
| Node missing | Region, running Windows Server 2019 x86-64 image, AWS authorization | Use `us-east-1`; the app intentionally filters other nodes. |
| SSM offline | SSM Agent/service, outbound HTTPS/DNS, IAM policy propagation | Wait or repair SSM. Do not open inbound ports. |
| Install/update rejected before SSM | Manifest v2 shape; certificate DER bound/parse; self-signature; Code Signing EKU; CA=false; validity; SHA-1/SHA-256 consistency | Rebuild from the established tracked CER and current signing identity. Do not edit the manifest or initialize a replacement identity. |
| Install/update rejected on node | GitHub artifact hash; pre-trust exact signer; `LocalMachine\Root` and `TrustedPublisher`; post-trust `Valid` | Publish/use the exact signed artifact. Retry after correcting the release or SSM health; rollback is attempt-scoped. Never bypass verification or clear certificate stores. |
| SOCKS authentication fails | Credentials copied for the same node; Client service running | Copy credentials again or use **Repair**. Do not put credentials in SSM commands. |
| Stream rejected | Four-stream per-Client or 32-stream aggregate bound, target policy, Agent loss | Reduce concurrency or restore Agent/cellular. Do not increase queues ad hoc. |

## IAM behavior

With no instance profile, the app creates a deterministic dedicated SSM role/profile and associates it only after a second read confirms the instance still has no profile. If the deterministic profile already contains any unexpected role, it stops without changing it. With an existing role lacking SSM, it requires explicit confirmation and attaches only `arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore`. It never replaces or detaches an existing profile.

## Revocation and replacement

Owner control can revoke a known certificate serial. Revocation blocks new authenticated sessions but does not recover compromised relay state or delete provider/cloud resources. Replacing an EC2 node identity is a deliberate reinstall/reprovision operation and should be followed by revoking the previous serial. Routine update, repair, reboot, and endpoint migration preserve identity.

## Backup and incident boundary

Relay state lives in `C:\ProgramData\MobileEgress\Relay`; node state lives in `C:\ProgramData\MobileEgress\Client`. Both directories are ACL-restricted. Only the relay directory should be backed up centrally, as one quiescent unit while the service is stopped. Node identities can be deliberately reprovisioned.

If relay state/CA, Owner DPAPI state, or the signing key may be compromised, stop affected services, preserve a restricted incident copy, revoke/publish artifacts as appropriate, and perform a reviewed full trust reset. Tailscale logout, leaf endpoint rotation, or stale-state restore is not sufficient.
