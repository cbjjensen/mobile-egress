# Mobile Egress

Mobile Egress lets selected applications on Windows Server 2019 EC2 instances use an Android or iOS Agent device's cellular connection. The private relay/controller runs on Windows 10/11 or Apple Silicon macOS 13+; Tailscale Funnel carries raw Mobile Egress TLS to that loopback-only relay.

```text
EC2 Refract -> authenticated HTTP/CONNECT 127.0.0.1:1081 -> MobileEgressClient --+
                                                                                 +-> Tailscale Funnel -> local relay -> Agent -> cellular Internet
EC2 application -> authenticated SOCKS5 127.0.0.1:1080 -> MobileEgressClient -----+
```

Ordinary HTTP, HTTPS through CONNECT, and SOCKS are browser/application opt-ins on the same EC2 node as `MobileEgressClient`. They do not change the EC2 default route or Windows system proxy and are not controller-host, VPN, public, UDP, or QUIC proxy behavior. There is no relay EC2 instance, inbound EC2 security-group rule, Elastic IP, router change, local port-forward, or public proxy listener. The controller computer and Agent device must remain powered on and connected. Managed nodes remain x86-64 Windows Server 2019; there is no Mac headless Client. On macOS, the controlling administrator must remain logged in because the supported Tailscale GUI is per-user; logout makes traffic fail closed.

## Downloads

Use only accepted artifacts exposed through the managed **Downloads** links on the official [GitHub Releases](https://github.com/cbjjensen/mobile-egress/releases). Release notes are designed to list the Windows controller bundle, Windows EC2 Client, macOS controller PKG, and Android Agent APK, in that order. The first three are one indivisible **Desktop** release from the same tag; Android is independently selectable and may link another eligible release. The planned first Mac artifact is `mobile-egress-macos-1.1.0-arm64.pkg`; its verification JSON is mandatory private publisher evidence, not a GitHub asset. The signed Desktop candidate, Mac publication/acceptance, and iOS TestFlight build are still pending in the [current status](docs/status.md), so filenames and instructions here do not assert that those artifacts are available yet.

## Friend quick start

Each friend self-hosts a separate bridge and chooses one controller platform after that platform's accepted artifact has been published. Until the pending Mac and iOS gates are complete, use only an already-published Windows/Android release.

### Windows controller

1. Download the signed `mobile-egress-windows-<version>.zip` from the project's official [GitHub Releases](https://github.com/cbjjensen/mobile-egress/releases). Use the release APK for Android; use iOS/iPadOS 17+ only after an accepted TestFlight build has been separately distributed. Get the publisher's SHA-256 certificate fingerprint through a separate trusted channel and extract the Windows ZIP. Malicious substitution of that download source before first trust is outside this convenience-oriented setup boundary, so do not use mirrors, forwarded attachments, or repackaged archives.
2. You may independently compare the signer certificate Windows shows for the exact `MobileEgressSetup.exe` with the SHA-256 fingerprint received through the separate channel. **Properties → Digital Signatures** and trusted system Windows PowerShell are optional checks described in the [Windows friend quick start](windows-client/README.md#friend-quick-start), not required launchers. Directly double-clicking setup remains supported; confirm its displayed fingerprint reminder with **Yes**, approve one UAC prompt, and let it transactionally install and launch the controller.
3. In **Bridge**, choose **Install Tailscale** only when the status says **Not installed** and approve UAC. When it says **Installed · not connected**, choose **Connect Tailscale** and finish browser login; this never reruns the MSI. Once it says **Online**, choose **Set up local bridge**. On first use the controller opens Tailscale's official Funnel approval page automatically; approve it, then approve relay UAC when prompted. The app installs `MobileEgressRelay` as LocalSystem and enables raw TCP Funnel on port 8443.
4. In **Agent**, generate the short-lived Agent QR. Install/open the Android or iOS app, scan it, and start the Agent. Both implementations require cellular and do not fall back to Wi-Fi.
5. When you want a best-effort new carrier address, choose **Rotate cellular IP** in the Android or iOS/iPadOS Agent and confirm before any active streams are disconnected. Follow the platform guidance to turn Airplane Mode on, wait for the displayed hold interval, and turn it off. Android can guide you through its system Internet controls; on iOS/iPadOS, open Control Center manually. A local notification can cue the end of the hold interval when permission is granted, but notification permission is optional. Both Agents compare the available cellular public address families, reconnect automatically, and report **Changed**, **Unchanged**, or **Unverified** without copying the addresses. An unchanged normal attempt offers a longer retry.
6. In **AWS Login**, use the default **IAM user access key** path. The controller is fixed to `us-east-1`.
   - If the friend only has the AWS root login, they may use root in the browser to create an IAM user named `mobile-egress`. Root is for console setup only; never create or paste root access keys.
   - Create an access key for the `mobile-egress` IAM user and paste that access key into Mobile Egress.
   - IAM Identity Center remains available under **Advanced** for people who already know their Start URL. That URL looks like `https://d-xxxxxxxxxx.awsapps.com/start`, not the normal EC2 console URL.
7. In **EC2 Nodes**, select up to ten running x86-64 Windows Server 2019 instances. Choose **Prepare SSM** only when a node is not already SSM online. An attached profile is inspected without being replaced; the app asks before adding the SSM policy only when it is missing. For a new profile, AWS attachment propagation errors are retried automatically with bounded backoff for up to one minute. The controller then gives the already-running SSM Agent 30 seconds to refresh its credentials. If registration is still absent, choose **Restart EC2 and continue** and confirm the brief interruption; the controller requests a reboot only for that selected instance, waits for a fresh Agent ping, and installs the Client automatically. It never reboots without confirmation and never terminates or recreates the instance. On later runs, an online node shows **SSM ready** and skips profile setup entirely. **Install Client** remains available for retrying an interrupted install.
8. For a Client version `1.0.24` or later, choose **Copy proxy line** and paste the returned `127.0.0.1:1081:<username>:<password>` line into Refract running on that same EC2 instance. Port `1081` forwards ordinary HTTP requests and uses CONNECT for HTTPS destinations. Older managed nodes must be updated first. **Copy SOCKS5 URL** remains available for SOCKS-aware applications at `127.0.0.1:1080`. Configure only the intended application; do not set a Windows system proxy, default route, VPN, firewall exposure, or EC2 ingress rule.

Friends do not clone the repository, run Docker, execute setup scripts, open inbound EC2 ports, or handle Owner invitations. They do need permission to approve Tailscale, use the selected AWS account, and install the accepted Android APK or, once available, the accepted TestFlight iOS build.

### macOS controller

These steps apply only after the signed/notarized Mac PKG completes the pending release and physical-acceptance gates and is published. Then use an administrator account on an Apple Silicon Mac running macOS 13 or later.

1. Download the managed, quarantined `mobile-egress-macos-<version>-arm64.pkg` directly from GitHub Releases and install it normally with Apple Installer into `/Applications/ZFNF Mobile Egress.app`. Do not remove quarantine or bypass Gatekeeper. A production release must have the expected Developer ID Installer signature, notarization ticket, and staple.
2. Open ZFNF Mobile Egress. If Tailscale is absent, choose **Install Tailscale**. The controller verifies the official stable standalone PKG before opening Apple Installer. Approve the Tailscale system extension/VPN configuration when requested, then finish browser login. A correctly signed existing standalone or App Store Tailscale app is accepted; guided installation always uses standalone.
3. Choose **Set up local bridge**. If **Login Items approval required** appears, approve ZFNF Mobile Egress in **System Settings → General → Login Items**, return to the app, and wait for status to report **Relay service enabled**. No Owner key is created while approval is pending. Choose **Set up local bridge** again to complete setup and approve Tailscale Funnel. Then follow the same Agent, AWS Login, EC2 Nodes, and proxy steps above. Keep this controlling administrator logged in.

## Capacity and safety boundaries

- At most ten managed EC2 Clients per controller.
- At most 32 active streams per Client identity and 256 active streams through the one active Agent; admission is first-come within both limits.
- Bounded, fair per-stream and aggregate queues. Data saturation closes only the affected stream; required-control saturation or writer failure closes the affected session. Senders prefer 16 KiB data frames while accepting valid frames up to 32 KiB.
- Mobile Egress mTLS authenticates Owner, Client, and Agent identities. Tailscale supplies ingress, not application identity.
- EC2 Client private keys and configuration private keys are generated on-node and never returned through SSM.
- SSM receives only signed-install commands, public CSR/bootstrap output, and sealed configuration ciphertext. Proxy credentials and raw certificate/configuration values are not placed in SSM input, output, or logs.
- The app never creates or terminates EC2 instances, changes public IPs, or opens security-group ingress.
- A Mac logout, Tailscale loss, or Agent cellular loss fails proxy traffic closed; the product never changes a Mac default route and the Agent never falls back to Wi-Fi.
- `v1.1.0` is clean-install-only for a new Mac bridge: Windows private state is not migrated. Same-Mac signed PKG update/repair preserves identities/state. Intel/universal support, a Mac headless Client, ZFNF Mac App Store distribution, and automatic updates are out of scope.
- This is for light, personal, interruption-tolerant traffic. Tailscale Funnel availability and bandwidth limits apply.

## Documentation

- [Architecture](docs/architecture.md)
- [Release, deployment, and step-by-step physical acceptance](docs/deployment.md)
- [Authenticated 256-stream acceptance](docs/capacity-acceptance.md)
- [Physical acceptance record template](docs/templates/physical-acceptance-record.md)
- [Operations](docs/operations.md)
- [Mac build server over SSH](docs/ios-build-server.md)
- [Security model](docs/security-model.md)
- [Protocol](docs/protocol.md)
- [Current status](docs/status.md)
- [Signed macOS Keychain integration](docs/macos-keychain-integration.md)
- [Desktop controller and headless Windows Client](windows-client/README.md)
- [Android Agent](android/README.md)
- [iOS Agent](ios/README.md)

Developers can run `& .\scripts\test-all.ps1` from Windows PowerShell. The coupled Desktop entry point is `& .\scripts\release-desktop.ps1 -ReleaseVersion '1.1.0'`; `release-android.ps1` remains Android-only, and `release-all.ps1 -Components Desktop,Android` coordinates both. Running Desktop without `-Publish` still signs both platforms and freezes a local annotated tag; `-Publish` separately authorizes pushing source/tag state and changing GitHub. Follow the [deployment runbook](docs/deployment.md) before invoking a release.
