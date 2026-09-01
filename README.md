# Mobile Egress

Mobile Egress lets selected applications on Windows Server 2019 EC2 instances use an Android or iOS Agent device's cellular connection. One Windows 10/11 PC runs the private relay and controller; Tailscale Funnel carries raw Mobile Egress TLS to that loopback-only relay.

```text
EC2 Refract -> authenticated HTTP/CONNECT 127.0.0.1:1081 -> MobileEgressClient --+
                                                                                 +-> Tailscale Funnel -> local Windows relay -> Agent -> cellular Internet
EC2 application -> authenticated SOCKS5 127.0.0.1:1080 -> MobileEgressClient -----+
```

There is no relay EC2 instance, inbound EC2 security-group rule, Elastic IP, router change, local port-forward, or public proxy listener. The local PC and Agent device must remain powered on and connected.

## Friend quick start

Each friend self-hosts a separate bridge:

1. Download the signed `mobile-egress-windows-<version>.zip` from the project's official [GitHub Releases](https://github.com/cbjjensen/mobile-egress/releases). Use the release APK for Android, or the separately distributed TestFlight build for iOS/iPadOS 17+. Get the publisher's SHA-256 certificate fingerprint through a separate trusted channel and extract the Windows ZIP. Malicious substitution of that download source before first trust is outside this convenience-oriented setup boundary, so do not use mirrors, forwarded attachments, or repackaged archives.
2. You may independently compare the signer certificate Windows shows for the exact `MobileEgressSetup.exe` with the SHA-256 fingerprint received through the separate channel. **Properties → Digital Signatures** and trusted system Windows PowerShell are optional checks described in the [Windows friend quick start](windows-client/README.md#friend-quick-start), not required launchers. Directly double-clicking setup remains supported; confirm its displayed fingerprint reminder with **Yes**, approve one UAC prompt, and let it transactionally install and launch the controller.
3. In **Bridge**, choose **Install Tailscale** only when the status says **Not installed** and approve UAC. When it says **Installed · not connected**, choose **Connect Tailscale** and finish browser login; this never reruns the MSI. Once it says **Online**, choose **Set up local bridge**. On first use the controller opens Tailscale's official Funnel approval page automatically; approve it, then approve relay UAC when prompted. The app installs `MobileEgressRelay` as LocalSystem and enables raw TCP Funnel on port 8443.
4. In **Agent**, generate the short-lived Agent QR. Install/open the Android or iOS app, scan it, and start the Agent. Both implementations require cellular and do not fall back to Wi-Fi.
5. When you want a best-effort new carrier address, choose **Rotate cellular IP** in the Android or iOS/iPadOS Agent and confirm before any active streams are disconnected. Follow the platform guidance to turn Airplane Mode on, wait for the displayed hold interval, and turn it off. Android can guide you through its system Internet controls; on iOS/iPadOS, open Control Center manually. A local notification can cue the end of the hold interval when permission is granted, but notification permission is optional. Both Agents compare the available cellular public address families, reconnect automatically, and report **Changed**, **Unchanged**, or **Unverified** without copying the addresses. An unchanged normal attempt offers a longer retry.
6. In **AWS Login**, use the default **IAM user access key** path. The controller is fixed to `us-east-1`.
   - If the friend only has the AWS root login, they may use root in the browser to create an IAM user named `mobile-egress`. Root is for console setup only; never create or paste root access keys.
   - Create an access key for the `mobile-egress` IAM user and paste that access key into Mobile Egress.
   - IAM Identity Center remains available under **Advanced** for people who already know their Start URL. That URL looks like `https://d-xxxxxxxxxx.awsapps.com/start`, not the normal EC2 console URL.
7. In **EC2 Nodes**, select up to ten running x86-64 Windows Server 2019 instances. Choose **Prepare SSM** only when a node is not already SSM online. An attached profile is inspected without being replaced; the app asks before adding the SSM policy only when it is missing. For a new profile, AWS attachment propagation errors are retried automatically with bounded backoff for up to one minute. The controller then gives the already-running SSM Agent 30 seconds to refresh its credentials. If registration is still absent, choose **Restart EC2 and continue** and confirm the brief interruption; the controller requests a reboot only for that selected instance, waits for a fresh Agent ping, and installs the Client automatically. It never reboots without confirmation and never terminates or recreates the instance. On later runs, an online node shows **SSM ready** and skips profile setup entirely. **Install Client** remains available for retrying an interrupted install.
8. For a Client version `1.0.24` or later, choose **Copy proxy line** and paste the returned `127.0.0.1:1081:<username>:<password>` line into Refract running on that same EC2 instance. Port `1081` forwards ordinary HTTP requests and uses CONNECT for HTTPS destinations. Older managed nodes must be updated first. **Copy SOCKS5 URL** remains available for SOCKS-aware applications at `127.0.0.1:1080`.

Friends do not clone the repository, run Docker, execute setup scripts, open inbound EC2 ports, or handle Owner invitations. They do need permission to approve Tailscale, use the selected AWS account, and install the Android APK or TestFlight iOS build.

## Capacity and safety boundaries

- At most ten managed EC2 Clients per controller.
- At most four active streams per Client and 32 active streams through the one active Agent.
- Bounded, fair per-stream and aggregate queues; overload fails individual streams closed.
- Mobile Egress mTLS authenticates Owner, Client, and Agent identities. Tailscale supplies ingress, not application identity.
- EC2 Client private keys and configuration private keys are generated on-node and never returned through SSM.
- SSM receives only signed-install commands, public CSR/bootstrap output, and sealed configuration ciphertext. Proxy credentials and raw certificate/configuration values are not placed in SSM input, output, or logs.
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
- [iOS Agent](ios/README.md)

Developers can run `& .\scripts\test-all.ps1` from Windows PowerShell. Use `release-windows.ps1` for controller/setup/relay/EC2 Client changes, `release-android.ps1 -ReleaseVersion ...` for Android-only changes, and `release-all.ps1` only for cross-component or protocol changes. Each deterministic path runs its component gate, reuses the established signer, verifies remote asset digests, and leaves stable promotion to the documented physical-acceptance process.
