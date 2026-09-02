# Desktop controller and headless Windows Client

The shared React/Wails and Go controller runs on Windows 10/11 or Apple Silicon macOS 13+ through thin platform composition roots under this existing `windows-client` tree. `MobileEgressClient` remains a Windows Server 2019 EC2 service; there is no Mac headless Client. A normal public **Desktop** release couples the Windows controller ZIP, EC2 Client, and macOS PKG at one version. The explicitly approved v1.1.1 proxy hotfix is Windows-only, keeps Android on the published v1.1.0 APK, and has no macOS artifact.

## Friend quick start

### Windows

Download `mobile-egress-windows-<version>.zip` only from the project's official GitHub Releases page and extract it; do not start an individual controller, relay, admin, or Client executable. Obtain the publisher SHA-256 certificate fingerprint through a separate trusted channel. You may independently inspect the exact `MobileEgressSetup.exe` signer through **Properties → Digital Signatures** or trusted system **Windows PowerShell** and compare it with that separately shared identity. This optional PowerShell inspection rejects a damaged, unsigned, or differently signed setup and prints the certificate SHA-256 for comparison:

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

Open `/Applications/ZFNF Mobile Egress.app`. When Tailscale is absent, **Install Tailscale** verifies the official standalone PKG before opening Apple Installer. Approve its system extension and VPN configuration when requested, then finish browser login. A correctly signed existing standalone or Mac App Store Tailscale app is accepted; guided installation always installs the standalone variant.

Choose **Set up local bridge**. The first call registers the bundled relay through Service Management. If the UI reports **Login Items approval required**, approve ZFNF Mobile Egress under **System Settings → General → Login Items**, return to the controller, and let an ordinary status poll prove the exact enabled helper. Approval-pending setup returns without creating an Owner key. Invoke **Set up local bridge** again after the UI reports **Relay service enabled**. Keep this administrator logged in; logout makes the per-user Tailscale path unavailable and proxy traffic fails closed.

## Controller UI

- **Bridge** distinguishes official Tailscale being absent, installed/offline, and online. On Windows it offers verified MSI installation only when absent; when installed/offline it offers **Connect Tailscale**, which performs browser login and unattended setup without rerunning the MSI. The backend independently rejects duplicate install requests. Bridge setup streams the hidden Funnel command's bounded output, opens only an exact official `https://login.tailscale.com/f/funnel?...` approval URL on first use, waits for approval, and then installs `MobileEgressRelay` as a loopback-only LocalSystem service through UAC. The installer passes its private staging path through an isolated process environment instead of PowerShell command text, tolerates spaces in the Windows profile path, and reports the safe failing stage when setup cannot continue. Recurring background Tailscale status and Funnel commands use Windows' no-console process policy so controller polling never flashes command windows. On macOS, Bridge verifies the official stable PKG, opens Apple Installer, guides system-extension/VPN approval, sets `TAILSCALE_BE_CLI=1`, and uses `tailscale up` without Windows-only arguments. The Mac relay state is public as `not-registered`, `approval-required`, `enabled`, `version-mismatch`, or `unavailable`; setup does not create an Owner before exact enabled-helper proof, and `version-mismatch` is repaired without unregistering the service. Both platforms retain raw TCP Funnel on 8443 and strict approval-URL filtering.
- **Agent** issues an in-memory, expiring Agent enrollment QR. Android and iOS use the same protocol-compatible enrollment and migration QR formats; after a Funnel name change the controller displays a distinct one-use migration QR.
- **AWS Login** defaults to an IAM user access key because it is easier for beginners than IAM Identity Center. The setup helper opens the `us-east-1` IAM user creation page. If a friend only has the AWS root login, they use root only in the browser to create an IAM user named `mobile-egress`, then create an access key for that IAM user and paste it into Mobile Egress. Never create or paste root access keys. IAM Identity Center remains available under **Advanced** for users who already have a Start URL.
- **EC2 Nodes** inventories supported `us-east-1` instances, safely prepares SSM IAM, and gives a running Agent 30 seconds to refresh its new credentials. If it remains unavailable, an explicit **Restart EC2 and continue** confirmation reboots only that selected instance, waits for a fresh SSM ping, and resumes signed Client installation. Activity events distinguish unregistered, offline, stale, and ready states without including provider errors or secrets. The page also installs/updates/repairs signed Clients, shows a redacted `127.0.0.2:1081:***:***` endpoint, copies that HTTP proxy line for Refract by default, and retains a separate SOCKS5 URL action. Both copy actions remain disabled until the node reports Client `1.1.1` or later. Update an older Client, wait for the refreshed version, and copy again rather than reusing a stale `.1` value.

The Windows tray or macOS menu-bar item reports bridge/Funnel state and reopens the controller. Quitting the controller does not erase or unregister relay state, and it never stops EC2 Client Windows services. On macOS, quitting is distinct from logging out: logout removes the supported per-user Tailscale availability and traffic fails closed.

## Local state and services

| Platform/role | Storage and service boundary |
|---|---|
| Windows controller | Owner/AWS/node metadata is protected with Windows-user DPAPI. `MobileEgressRelay` runs as automatic LocalSystem on `127.0.0.1:8443`; state is `C:\ProgramData\MobileEgress\Relay`. ProgramData ACLs allow only SYSTEM and local Administrators. The UAC helper stages public CSR/result data only. |
| macOS controller | Owner/AWS/node metadata uses Security.framework data-protection Keychain service `com.cbjjensen.mobile-egress.controller`, non-synchronizing device-only items, and the signed private access group. There is no plaintext or file-encryption fallback. The bundled `com.cbjjensen.mobile-egress.relay` LaunchDaemon runs as root on `127.0.0.1:8443`; state is `/Library/Application Support/ZFNF Mobile Egress/Relay` mode `0700`. Its admin socket is `/var/run/com.cbjjensen.mobile-egress.relay.sock`, `root:admin`, mode `0660`. First setup binds the kernel-authenticated controlling administrator UID; later management accepts that UID or root. |
| EC2 Client | `MobileEgressClient` remains an automatic LocalSystem service under `C:\Program Files\MobileEgress`, with ACL-protected state at `C:\ProgramData\MobileEgress\Client`. It exposes authenticated loopback SOCKS5 on `127.0.0.2:1080` and HTTP forward/CONNECT on `127.0.0.2:1081`; an application on that same EC2 node must explicitly opt in to one of these listeners. There is no `.1` compatibility listener. They are not controller-host, system-wide, VPN, public, UDP, or QUIC proxies. SOCKS, ordinary HTTP, HTTPS CONNECT, active requests, and ordinary HTTP's two retained idle destination streams all share one 256-slot relay session; the Agent-wide limit is also 256 streams and idle streams expire after 15 seconds. Outbound relay data prefers 16 KiB frames, while valid data frames up to thirty-two KiB are accepted. Inbound data allows thirty-two retained frames per stream within one 8,192-frame/64-MiB session budget; outbound writes retain their existing synchronous backpressure. The Client retains at most 1,024 recently closed stream IDs for late-frame handling. |

The Mac relay-admin socket exposes only `status`, `setup`, `rotate`, and `repair`; it never returns Owner private keys, AWS credentials, node metadata, raw daemon errors, or the relay CA key.

Across the bridge, Client-to-Agent and Agent-to-Client retained data allow 32 frames per stream and use separate 8,192-frame/64-MiB directional budgets.

The 256-stream/32-frame capacity is covered by deterministic unit/component tests and ordinary build checks, not by a load, soak, memory, authenticated-harness, or physical-device run. Those acceptance executions remain pending and were prohibited for this change.

## Node bootstrap and sealed configuration

The signed desktop controller embeds node-release manifest v2. The same raw manifest is embedded into both same-version Windows and macOS controllers. Before invoking SSM it parses the bounded public certificate DER and validates its exact SHA-1/SHA-256 identity, cryptographic self-signature, Code Signing EKU, CA=false constraint, current validity, and the GitHub release metadata. A bounded machine-global mutex serializes the node transaction. SSM downloads the exact Windows Client artifact, verifies SHA-256, requires the untrusted Authenticode signature to carry the exact embedded certificate bytes, imports only that DER into LocalMachine Root and TrustedPublisher when absent, and then requires Authenticode `Valid` before installation. All managed-node service and private-state behavior remains Windows-only.

After trust and artifact validation, the app installs the service and runs `bootstrap`. Bootstrap is idempotent and returns only the Client CSR and durable X25519 public configuration key. The public publisher certificate may appear in SSM input/logs; private signing material, SOCKS credentials, pairing values, and plaintext configuration may not.

The Owner signs the CSR directly. SOCKS credentials and the resulting endpoint/certificates are encrypted to the node key with ephemeral X25519 + HKDF-SHA256 + AES-256-GCM. Only the sealed envelope crosses SSM. An authenticated monotonic generation rejects old envelopes even after newer updates. A bounded fingerprint window rejects recent exact replays; any older valid envelope for the current generation is accepted only as an idempotent no-op when it decrypts to the exact persisted configuration. Same-generation content changes always fail closed.

Before remote configuration, the controller atomically reserves capacity and persists recoverable `configuring` metadata. The app enforces a single controller process so encrypted read/modify/write operations cannot race across two UI instances. Abandoned pre-metadata reservations are shown in the UI and require explicit confirmation to cancel. `Update` replaces only the verified executable. `Repair` also reseals/reapplies the retained desired generation and completes partial installation or endpoint rotation. Neither changes keys, certificate serial, or SOCKS credentials. Endpoint migration advances the generation and reseals only the relay URL. Existing controller and node state is migrated in place from schema version 1 to version 2, assigning generation 1 to historical configured nodes.

## Developer checks

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
