# Desktop controller and headless Windows Client

The shared React/Wails and Go controller runs on Windows 10/11 or Apple Silicon macOS 13+ through thin platform composition roots under this existing `windows-client` tree. `MobileEgressClient` remains a Windows Server 2019 EC2 service; there is no Mac headless Client. A normal public **Desktop** release couples the Windows controller installer, EC2 Client, and macOS PKG at one version. Already published releases retain their ZIP artifacts. The explicitly approved v1.1.1 proxy hotfix is Windows-only, keeps Android on the published v1.1.0 APK, and has no macOS artifact.

## Friend quick start

### Windows

Releases built with simplified setup provide **MobileEgressSetup.exe** as a single download containing its signed payload. No extraction or adjacent folder is needed. Previously published ZIP releases remain unchanged and require full extraction. Setup displays installation progress and installs Microsoft's WebView2 runtime when missing. If runtime preparation fails, **Retry** repeats only that step. Later, launch **Mobile Egress** from Start; it can finish a missing runtime without reinstalling the application. See [Windows installer](../docs/windows-installer.md).

Follow the **Next step** card: **Set up this computer**, pair your Android or iOS Agent, start cellular sharing, connect AWS, install the selected EC2 Client, then verify an application request. The setup action skips completed stages and resumes after observed macOS Login Items approval. Cancellation stops subsequent stages without repeatedly opening permission prompts. Saved AWS credentials are checked automatically on launch. **Funnel active** alone is not completion: EC2 installation requires a fresh healthy bridge, and **Setup complete** requires your confirmation of a successful request through the EC2 application proxy. Connection diagnostics are expandable. An interrupted Client install can be retried for the same instance; use Repair when node configuration is already saved.

The prerequisite installer follows [Microsoft's Evergreen deployment guidance](https://learn.microsoft.com/microsoft-edge/webview2/concepts/distribution), verifies the downloaded bootstrapper's trusted Microsoft signature, and checks runtime availability before launching the app. Older flat ZIP layouts remain supported by Setup.

Download the managed Windows setup artifact only from the project's official GitHub Releases page; do not start an individual controller, relay, admin, or Client executable from a legacy ZIP. Obtain the publisher SHA-256 certificate fingerprint through a separate trusted channel. You may independently inspect the exact `MobileEgressSetup.exe` signer through **Properties → Digital Signatures** or trusted system **Windows PowerShell** and compare it with that separately shared identity. This optional PowerShell inspection rejects a damaged, unsigned, or differently signed setup and prints the certificate SHA-256 for comparison:

```powershell
$setupPath = (Resolve-Path '.\MobileEgressSetup.exe').Path
$expectedThumbprint = '85F220C1BF05A5D3A86B5DD408787EC1B122ECB7'
$expectedCertificateSha256 = '9FE214C350D7CE04C8EE7F71E169281B50FF0B2A7C5669A348AC10616FB7061F'
$signature = Get-AuthenticodeSignature -LiteralPath $setupPath
$status = [string]$signature.Status
if ($status -notin @('NotTrusted', 'Valid')) { throw "Reject setup: Authenticode status is $status." }
if ($null -eq $signature.SignerCertificate -or $signature.SignerCertificate.Thumbprint.ToUpperInvariant() -ne $expectedThumbprint) { throw 'Reject setup: signer thumbprint differs.' }
$sha256 = [Security.Cryptography.SHA256]::Create()
try { $certificateSha256 = ([BitConverter]::ToString($sha256.ComputeHash($signature.SignerCertificate.RawData))).Replace('-', '') } finally { $sha256.Dispose() }
if ($certificateSha256 -ne $expectedCertificateSha256) { throw 'Reject setup: signer certificate SHA-256 differs.' }
$certificateSha256
```

For the optional PowerShell inspection, Status must be exactly `NotTrusted` on a fresh PC or `Valid` if this publisher is already trusted; reject `HashMismatch`, `NotSigned`, `UnknownError`, or any other status. Do not run a verifier script from the ZIP. The expected SHA-1 and certificate SHA-256 must come from separately shared instructions. A value displayed by setup or shipped inside the ZIP is a reminder, not the separate identity check. Direct double-click setup remains supported without this independent inspection. Under that relaxed convenience model, malicious download-source substitution before first trust is outside the threat boundary; use only the official GitHub Releases source plus the separately shared fingerprint.

Double-click `MobileEgressSetup.exe`. The first launch can show **Unknown publisher** and may require **More info → Run anyway** in SmartScreen; self-signing does not establish SmartScreen reputation. Setup displays the tracked fingerprint and requires explicit **Yes**, then asks for one UAC approval. It holds its own exact executable against write/delete/replacement while checking its Authenticode signer, confirming, hashing, and waiting indefinitely for actual elevated-child completion. The child repeats digest/signature checks before trust, then holds a bounded machine-global setup mutex across trust, signed-sibling verification, install, and rollback; timeout fails before trust mutation and abandoned ownership is recovered. During an update, setup automatically stops only the controller running from the installed path after all staged files verify and before any installed file moves; failure to stop it leaves the existing installation untouched. Setup launches the newly installed controller unelevated only after child exit zero and a nonce/digest-bound success result. If installation rollback cannot restore prior files, setup preserves the restricted SYSTEM/Administrators-only recovery backup and reports redacted guidance not to rerun setup and to contact the publisher.

### macOS

Use an administrator account that will remain the controlling, logged-in account. On Apple Silicon macOS 13+, download `mobile-egress-macos-<version>-arm64.pkg` from the managed GitHub Releases link and install it normally with Apple Installer. The production PKG must have the expected Developer ID Installer signature, accepted notarization ticket, and staple. Do not remove quarantine, bypass Gatekeeper, or invent a signing identity from example text.

Open `/Applications/ZFNF Mobile Egress.app` and choose **Set up this computer**. It verifies and installs official standalone Tailscale only when absent, opens sign-in, and continues to the local relay. Approve its system extension and VPN configuration when requested. A correctly signed existing standalone or Mac App Store Tailscale app is accepted.

An unavailable Tailscale check is shown separately from a disconnected or missing installation. It does not offer reinstallation as a remedy. Each status/connect/setup operation verifies the app once, retains change checks before commands, and uses a Tailscale deadline independent of relay checks. Refreshes run one at a time and cannot overwrite the results or errors of an action. Official login and Funnel approval URLs open while their commands are waiting, after the complete URL has arrived.

If setup reports **Login Items approval required**, approve ZFNF Mobile Egress in the System Settings page that opens. Leave setup running: it observes the exact enabled helper and automatically finishes the bridge. No Owner key is created while approval is pending. Cancellation or a timeout stops automatic continuation; choose **Set up this computer** to resume. Keep this administrator logged in; logout makes the per-user Tailscale path unavailable and proxy traffic fails closed.

If setup is interrupted, retry **Set up this computer** from the same macOS account. Before contacting the relay, the controller saves the pending Owner key, exact certificate request, and request ID in Keychain. Retries, including after quitting and reopening the app, reuse the relay's existing durable response rather than create another Owner. A Keychain write failure leaves that pending setup available for retry. Errors identify the failing stage without displaying command output or secrets.

Older versions did not save pending setup. If an older attempt initialized the relay but lost the only Owner key, a retry cannot reconstruct it. Return to the original macOS account or restore its Keychain backup; reinstalling Tailscale and approving Login Items cannot repair a missing key. The controller preserves existing relay state and paired devices.

## Controller UI

- **Bridge** offers **Set up this computer**, which sequences verified Tailscale installation when missing, sign-in, and local relay setup. The backend rejects duplicate installs and overlapping setup actions. On macOS it waits for observed Login Items approval before continuing; on Windows it retains native elevation for installation and relay service setup. Actual download, verification, installation, and connection stages are displayed. Cancellation stops later steps and retains native installer ownership until it returns. **Connection details and troubleshooting** contains transport/service diagnostics and repair. Both platforms retain loopback-only relay operation, raw TCP Funnel on 8443, strict approval-URL filtering, and independent prerequisite verification.
- **Agent** distinguishes a paired phone from active cellular sharing using fresh relay observations. An expiring enrollment QR is not pairing success. Pairing visibility is requested only by an authenticated Owner with an explicit health capability header; older relays show unknown pairing state, and older clients retain their existing health response. Android and iOS keep the same enrollment and migration QR formats. After a Funnel name change the controller displays a distinct one-use migration QR.
- **AWS Login** defaults to an IAM user access key because it is easier for beginners than IAM Identity Center. The setup helper opens the `us-east-1` IAM user creation page. If a friend only has the AWS root login, they use root only in the browser to create an IAM user named `mobile-egress`, then create an access key for that IAM user and paste it into Mobile Egress. Never create or paste root access keys. IAM Identity Center remains available under **Advanced** for users who already have a Start URL.
- **EC2 Nodes** inventories supported `us-east-1` instances, safely prepares SSM IAM, and gives a running Agent 30 seconds to refresh its new credentials. If it remains unavailable, an explicit **Restart EC2 and continue** confirmation reboots only that selected instance, waits for a fresh SSM ping, and resumes signed Client installation. Activity events distinguish unregistered, offline, stale, and ready states without including provider errors or secrets. The page also installs/updates/repairs signed Clients, shows a redacted `127.0.0.2:1081:***:***` endpoint, copies that HTTP proxy line for Refract by default, and retains a separate SOCKS5 URL action. Both copy actions remain disabled until the node reports Client `1.1.1` or later. Update an older Client, wait for the refreshed version, and copy again rather than reusing a stale `.1` value.

The Windows tray or macOS menu-bar item reports bridge/Funnel state and reopens the controller. Quitting the controller does not erase or unregister relay state, and it never stops EC2 Client Windows services. On macOS, quitting is distinct from logging out: logout removes the supported per-user Tailscale availability and traffic fails closed.

## Local state and services

| Platform/role | Storage and service boundary |
|---|---|
| Windows controller | Owner/AWS/node metadata is protected with Windows-user DPAPI. `MobileEgressRelay` runs as automatic LocalSystem on `127.0.0.1:8443`; state is `C:\ProgramData\MobileEgress\Relay`. ProgramData ACLs allow only SYSTEM and local Administrators. The UAC helper stages public CSR/result data only. |
| macOS controller | Owner/AWS/node metadata uses Security.framework data-protection Keychain service `com.cbjjensen.mobile-egress.controller`, non-synchronizing device-only items, and the signed private access group. There is no plaintext or file-encryption fallback. The bundled `com.cbjjensen.mobile-egress.relay` LaunchDaemon runs as root on `127.0.0.1:8443`; state is `/Library/Application Support/ZFNF Mobile Egress/Relay` mode `0700`. Its admin socket is `/var/run/com.cbjjensen.mobile-egress.relay.sock`, `root:admin`, mode `0660`. First setup binds the kernel-authenticated controlling administrator UID; later management accepts that UID or root. |
| EC2 Client | `MobileEgressClient` remains an automatic LocalSystem service under `C:\Program Files\MobileEgress`, with ACL-protected state at `C:\ProgramData\MobileEgress\Client`. It exposes authenticated loopback SOCKS5 on `127.0.0.2:1080` and HTTP forward/CONNECT on `127.0.0.2:1081`; an application on that same EC2 node must explicitly opt in to one of these listeners. There is no `.1` compatibility listener. They are not controller-host, system-wide, VPN, public, UDP, or QUIC proxies. SOCKS, ordinary HTTP, HTTPS CONNECT, active requests, and ordinary HTTP's up to 16 retained idle destination streams (four per host) all share one relay session without a fixed active-stream ceiling; idle streams expire after 60 seconds. Outbound relay data prefers 16 KiB frames, while valid data frames up to thirty-two KiB are accepted. Inbound data allows thirty-two retained frames per stream within one 8,192-frame/64-MiB session budget; outbound writes retain their existing synchronous backpressure. The Client retains at most 1,024 recently closed stream IDs for late-frame handling. |

The Mac relay-admin socket exposes only `status`, `setup`, `rotate`, and `repair`; it never returns Owner private keys, AWS credentials, node metadata, raw daemon errors, or the relay CA key.

Across the bridge, Client-to-Agent and Agent-to-Client retained data allow 32 frames per stream and use separate 8,192-frame/64-MiB directional budgets.

Automated component tests exercise more than 1,024 live streams and independently bounded data queues. Physical throughput and sustained-load evidence are reported separately in [the browser throughput report](../docs/browser-throughput-measurements.md). Socket and device memory limits still apply; older peers may enforce their former stream caps.

## Node bootstrap and sealed configuration

The signed desktop controller embeds node-release manifest v2. The same raw manifest is embedded into both same-version Windows and macOS controllers. Before invoking SSM it parses the bounded public certificate DER and validates its exact SHA-1/SHA-256 identity, cryptographic self-signature, Code Signing EKU, CA=false constraint, current validity, and the GitHub release metadata. A bounded machine-global mutex serializes the node transaction. SSM downloads the exact Windows Client artifact, verifies SHA-256, requires the untrusted Authenticode signature to carry the exact embedded certificate bytes, imports only that DER into LocalMachine Root and TrustedPublisher when absent, and then requires Authenticode `Valid` before installation. All managed-node service and private-state behavior remains Windows-only.

After trust and artifact validation, the app installs the service and runs `bootstrap`. Bootstrap is idempotent and returns only the Client CSR and durable X25519 public configuration key. The public publisher certificate may appear in SSM input/logs; private signing material, SOCKS credentials, pairing values, and plaintext configuration may not.

The Owner signs the CSR directly. SOCKS credentials and the resulting endpoint/certificates are encrypted to the node key with ephemeral X25519 + HKDF-SHA256 + AES-256-GCM. Only the sealed envelope crosses SSM. An authenticated monotonic generation rejects old envelopes even after newer updates. A bounded fingerprint window rejects recent exact replays; any older valid envelope for the current generation is accepted only as an idempotent no-op when it decrypts to the exact persisted configuration. Same-generation content changes always fail closed.

Before remote configuration, the controller atomically reserves capacity and persists recoverable `configuring` metadata. The app enforces a single controller process so encrypted read/modify/write operations cannot race across two UI instances. Abandoned pre-metadata reservations are shown in the UI and require explicit confirmation to cancel. `Update` replaces only the verified executable. `Repair` also reseals/reapplies the retained desired generation and completes partial installation or endpoint rotation. Neither changes keys, certificate serial, or SOCKS credentials. Endpoint migration advances the generation and reseals only the relay URL. Existing controller and node state is migrated in place from schema version 1 to version 2, assigning generation 1 to historical configured nodes.

## Developer checks

The guided Tailscale installer on macOS delegates package trust to the system: `pkgutil --check-signature` must report a trusted certificate and the exact `Developer ID Installer: Tailscale Inc. (W5364U7YZB)` leaf identity, then `spctl --assess --type install` must succeed. Both commands use fixed executable paths, a minimal environment, bounded output, and staged-path revalidation before and after execution. The controller does not maintain a separate XAR/CMS verifier or bundled Apple root certificates.

From the repository root:

```powershell
go test ./windows-client/...
go vet ./windows-client/...
npm run check --prefix windows-client/frontend
npm run build --prefix windows-client/frontend
```

Production packaging uses the established tracked code-signing certificate through PowerShell `Set-AuthenticodeSignature`; it does not require the Windows SDK or `signtool`:

```powershell
& .\scripts\build-windows.ps1 -ReleaseVersion 1.1.1
```

`-CodeSigningThumbprint` remains an optional compatibility assertion and must equal the tracked publisher thumbprint if supplied.

Credential-free macOS staging uses the pinned Go 1.26.7, Node 24.20.0, and Wails 2.14.0 toolchain from `windows-client/macos/toolchain.lock` on an authorized Apple Silicon Mac. Production PKG creation is performed by `scripts/release-macos.sh` only through the coupled Desktop orchestrator; it requires approved Developer ID Application/Installer identities, a matching distribution profile, and a `notarytool` Keychain profile. Its local verification JSON is evidence, not a GitHub asset. See [Mac build server over SSH](../docs/ios-build-server.md) and [deployment](../docs/deployment.md); do not reconstruct a production invocation manually.

The expected public Desktop command is:

```powershell
& .\scripts\release-desktop.ps1 -ReleaseVersion '<version>'
```

The approved v1.1.1 Windows-only hotfix command is `& .\scripts\release-all.ps1 -ReleaseVersion '1.1.1' -Components Windows`. It signs and verifies only the Windows ZIP and EC2 Client and freezes a local tag; Android remains the published v1.1.0 APK and macOS is unavailable. Add `-Publish` only after separate publication approval.

Even without `-Publish`, the Desktop command performs Windows signing, remote Mac build/sign/notarization, and freezes a local annotated tag. `-Publish` is the separate boundary for pushing source/tag state and changing GitHub.

Unsigned builds can run unit tests and foreground developer commands, but production relay/Client setup intentionally rejects them.
