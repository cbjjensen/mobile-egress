# Windows controller and headless Client

The signed Windows release has two roles: the Wails desktop controller on the operator's Windows 10/11 PC and `MobileEgressClient` services on existing Windows Server 2019 EC2 nodes.

## Friend quick start

Download and extract `mobile-egress-windows-<version>.zip`; do not start an individual controller, relay, admin, or Client executable. Obtain the publisher SHA-256 certificate fingerprint through a separate trusted channel. Before the first run, inspect the exact `MobileEgressSetup.exe` signer through **Properties → Digital Signatures** or trusted system **Windows PowerShell** and compare it with that separately shared identity. This optional PowerShell inspection rejects a damaged, unsigned, or differently signed setup and prints the certificate SHA-256 for comparison:

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

For the PowerShell inspection, Status must be exactly `NotTrusted` on a fresh PC or `Valid` if this publisher is already trusted; reject `HashMismatch`, `NotSigned`, `UnknownError`, or any other status. Do not run a verifier script from the ZIP. The expected SHA-1 and certificate SHA-256 must come from separately shared instructions. A value displayed by setup or shipped inside the ZIP is a reminder, not the separate identity check.

Double-click `MobileEgressSetup.exe`. The first launch can show **Unknown publisher** and may require **More info → Run anyway** in SmartScreen; self-signing does not establish SmartScreen reputation. Setup displays the tracked fingerprint and requires explicit **Yes**, then asks for one UAC approval. It holds its own exact executable against write/delete/replacement while checking its Authenticode signer, confirming, hashing, and waiting for the elevated child. The child repeats digest/signature checks before trust. Setup launches the installed controller unelevated only after child exit zero and a nonce/digest-bound success result.

## Controller UI

- **Bridge** verifies/installs official Tailscale, completes browser login, enables unattended raw TCP Funnel, and installs `MobileEgressRelay` as a loopback-only LocalSystem service.
- **Phone** issues an in-memory, expiring Agent enrollment QR. After a Funnel name change it displays a distinct one-use migration QR.
- **AWS Login** supports IAM Identity Center device/browser login and DPAPI-encrypted access-key fallback.
- **EC2 Nodes** inventories supported `us-east-1` instances, safely prepares SSM IAM, installs/updates/repairs signed Clients, shows redacted node metadata, and reveals SOCKS credentials only on copy.

The tray reports bridge/Funnel state and reopens the controller. Quitting the controller does not stop relay or Client Windows services.

## Local state and services

- Owner/AWS/node controller metadata: Windows-user DPAPI store under the user's configuration directory.
- Relay service: `MobileEgressRelay`, LocalSystem, auto start, `127.0.0.1:8443`, state `C:\ProgramData\MobileEgress\Relay`.
- EC2 service: `MobileEgressClient`, LocalSystem, auto start, authenticated `127.0.0.1:1080`, state `C:\ProgramData\MobileEgress\Client`.
- Installed service binaries: `C:\Program Files\MobileEgress`.

ProgramData state ACLs are reduced to SYSTEM and local Administrators. The elevated helper stages only public CSR/result data; the Owner key never crosses UAC.

## Node bootstrap and sealed configuration

The app invokes SSM to download the exact GitHub Client release, verify SHA-256 and Authenticode, install the service, and run `bootstrap`. Bootstrap is idempotent and returns only the Client CSR and durable X25519 public configuration key.

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
& .\scripts\build-windows.ps1 -ReleaseVersion 1.2.3
```

`-CodeSigningThumbprint` remains an optional compatibility assertion and must equal the tracked publisher thumbprint if supplied.

Unsigned builds can run unit tests and foreground developer commands, but production relay/Client setup intentionally rejects them.
