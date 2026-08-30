# Operations

## Normal use

`MobileEgressRelay` and each `MobileEgressClient` run as automatic LocalSystem services. Closing the controller window leaves it in the tray; quitting the controller does not stop those services. Start/stop the Android Agent only through its visible UI or foreground notification.

An EC2 application opts in with its node-specific `socks5://<user>:<password>@127.0.0.1:1080` value. Never configure an EC2 security group, Windows system proxy, or public listener for SOCKS.

## Controller actions

- **Install Client** verifies the GitHub URL metadata, SHA-256, and Authenticode signature before installing a LocalSystem service and provisioning a node identity.
- **Update** replaces the executable with the current signed release while retaining node keys, certificate, and SOCKS credentials.
- **Repair** performs the signed update and sends a fresh sealed copy of the existing configuration.
- **Copy credentials** is the only normal action that reveals SOCKS authentication.
- **Rotate endpoint safely** appears when the Tailscale Funnel origin differs from the encrypted Owner origin. Connect AWS first whenever managed nodes exist.
- **Repair local relay** re-verifies the signed sibling relay, reapplies protected state ACLs, repairs the LocalSystem service configuration, and starts it without changing the CA or identities.

## Endpoint rotation runbook

1. Restore Tailscale login and make sure Funnel reports the intended `*.ts.net:8443` origin.
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
| Rotation required | Current `*.ts.net` name differs from stored Owner endpoint | Connect AWS, rotate, repair failed nodes, scan migration QR. |
| Agent offline | Android foreground service, cellular availability, battery restrictions | Start from visible UI; restore cellular. Wi-Fi is intentionally not a fallback. |
| Node missing | Region, running Windows Server 2019 x86-64 image, AWS authorization | Use `us-east-1`; the app intentionally filters other nodes. |
| SSM offline | SSM Agent/service, outbound HTTPS/DNS, IAM policy propagation | Wait or repair SSM. Do not open inbound ports. |
| Install/update rejected | GitHub release URL, manifest hash, Authenticode signer | Publish/use the exact signed artifact. Never bypass verification. |
| SOCKS authentication fails | Credentials copied for the same node; Client service running | Copy credentials again or use **Repair**. Do not put credentials in SSM commands. |
| Stream rejected | Four-stream per-Client or 32-stream aggregate bound, target policy, Agent loss | Reduce concurrency or restore Agent/cellular. Do not increase queues ad hoc. |

## IAM behavior

With no instance profile, the app creates a deterministic dedicated SSM role/profile and associates it only after a second read confirms the instance still has no profile. If the deterministic profile already contains any unexpected role, it stops without changing it. With an existing role lacking SSM, it requires explicit confirmation and attaches only `arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore`. It never replaces or detaches an existing profile.

## Revocation and replacement

Owner control can revoke a known certificate serial. Revocation blocks new authenticated sessions but does not recover compromised relay state or delete provider/cloud resources. Replacing an EC2 node identity is a deliberate reinstall/reprovision operation and should be followed by revoking the previous serial. Routine update, repair, reboot, and endpoint migration preserve identity.

## Backup and incident boundary

Relay state lives in `C:\ProgramData\MobileEgress\Relay`; node state lives in `C:\ProgramData\MobileEgress\Client`. Both directories are ACL-restricted. Only the relay directory should be backed up centrally, as one quiescent unit while the service is stopped. Node identities can be deliberately reprovisioned.

If relay state/CA, Owner DPAPI state, or the signing key may be compromised, stop affected services, preserve a restricted incident copy, revoke/publish artifacts as appropriate, and perform a reviewed full trust reset. Tailscale logout, leaf endpoint rotation, or stale-state restore is not sufficient.
