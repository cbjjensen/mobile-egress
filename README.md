# Mobile Egress

Mobile Egress lets selected applications on Windows Server 2019 EC2 instances use an Android phone's cellular connection. One Windows 10/11 PC runs the private relay and controller; Tailscale Funnel carries raw Mobile Egress TLS to that loopback-only relay.

```text
EC2 application -> authenticated SOCKS5 127.0.0.1:1080 -> MobileEgressClient --+
                                                                              +-> Tailscale Funnel -> local Windows relay -> Android Agent -> cellular Internet
EC2 application -> authenticated SOCKS5 127.0.0.1:1080 -> MobileEgressClient --+
```

There is no relay EC2 instance, inbound EC2 security-group rule, Elastic IP, router change, local port-forward, or public SOCKS listener. The local PC and phone must remain powered on and connected.

## Friend quick start

Each friend self-hosts a separate bridge:

1. Download the signed `mobile-egress-windows-<version>.zip` and signed Android APK from the project's official [GitHub Releases](https://github.com/cbjjensen/mobile-egress/releases). Get the publisher's SHA-256 certificate fingerprint through a separate trusted channel and extract the Windows ZIP. Malicious substitution of that download source before first trust is outside this convenience-oriented setup boundary, so do not use mirrors, forwarded attachments, or repackaged archives.
2. You may independently compare the signer certificate Windows shows for the exact `MobileEgressSetup.exe` with the SHA-256 fingerprint received through the separate channel. **Properties → Digital Signatures** and trusted system Windows PowerShell are optional checks described in the [Windows friend quick start](windows-client/README.md#friend-quick-start), not required launchers. Directly double-clicking setup remains supported; confirm its displayed fingerprint reminder with **Yes**, approve one UAC prompt, and let it transactionally install and launch the controller.
3. In **Bridge**, choose **Install Tailscale** only when the status says **Not installed** and approve UAC. When it says **Installed · not connected**, choose **Connect Tailscale** and finish browser login; this never reruns the MSI. Once it says **Online**, choose **Set up local bridge**. On first use the controller opens Tailscale's official Funnel approval page automatically; approve it, then approve relay UAC when prompted. The app installs `MobileEgressRelay` as LocalSystem and enables raw TCP Funnel on port 8443.
4. In **Phone**, generate the short-lived Agent QR. Install/open the Android app, scan it, and tap **Start**. Android uses cellular only and does not fall back to Wi-Fi.
5. When you want a best-effort new carrier address, tap **Rotate cellular IP** in Android, manually turn Airplane Mode on, wait for the notification countdown, and turn it off. The Agent compares the cellular public address and reconnects automatically.
5. In **AWS Login**, use IAM Identity Center browser login (recommended) or the encrypted access-key fallback. The controller is fixed to `us-east-1`.
6. In **EC2 Nodes**, select up to ten running x86-64 Windows Server 2019 instances. Choose **Prepare SSM** where needed, wait for **SSM online**, then choose **Install Client**.
7. For a managed node, choose **Copy credentials** and configure only the intended application with the returned authenticated SOCKS5 URL. The listener is `127.0.0.1:1080` on that EC2 instance.

Friends do not clone the repository, run Docker, execute setup scripts, open inbound EC2 ports, or handle Owner invitations. They do need permission to approve Tailscale, use the selected AWS account, and install the Android APK.

## Capacity and safety boundaries

- At most ten managed EC2 Clients per controller.
- At most four active streams per Client and 32 active streams through the one Android Agent.
- Bounded, fair per-stream and aggregate queues; overload fails individual streams closed.
- Mobile Egress mTLS authenticates Owner, Client, and Agent identities. Tailscale supplies ingress, not application identity.
- EC2 Client private keys and configuration private keys are generated on-node and never returned through SSM.
- SSM receives only signed-install commands, public CSR/bootstrap output, and sealed configuration ciphertext. SOCKS credentials and raw certificate/configuration values are not placed in SSM input, output, or logs.
- The app never creates or terminates EC2 instances, changes public IPs, or opens security-group ingress.
- This is for light, personal, interruption-tolerant traffic. Tailscale Funnel availability and bandwidth limits apply.

## Documentation

- [Architecture](docs/architecture.md)
- [Release, deployment, and step-by-step physical acceptance](docs/deployment.md)
- [Physical acceptance record template](docs/templates/physical-acceptance-record.md)
- [Operations](docs/operations.md)
- [iOS build server over SSH](docs/ios-build-server.md)
- [Security model](docs/security-model.md)
- [Protocol](docs/protocol.md)
- [Current status](docs/status.md)
- [Windows controller and Client](windows-client/README.md)
- [Android Agent](android/README.md)

Developers can run `& .\scripts\test-all.ps1` from Windows PowerShell. Use `release-windows.ps1` for controller/setup/relay/EC2 Client changes, `release-android.ps1 -ReleaseVersion ...` for Android-only changes, and `release-all.ps1` only for cross-component or protocol changes. Each deterministic path runs its component gate, reuses the established signer, verifies remote asset digests, and leaves stable promotion to the documented physical-acceptance process.
