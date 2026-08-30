# Windows Self-Signing and Guided Bootstrap Implementation Plan

**Goal:** Create one reusable, locally generated Windows Authenticode identity and a guided setup executable so friends can install Mobile Egress without buying a certificate, visiting a certificate-provider website, or manually running trust scripts.

**Architecture:** The publisher workstation owns a self-signed, code-signing-only RSA key. Release builds are signed and timestamped with built-in PowerShell. A signed setup application asks the recipient to verify the separately shared certificate fingerprint, elevates once, trusts that exact public certificate, installs the controller, and launches it unelevated. The signed controller embeds the same public certificate so SSM can establish the exact trust anchor on Windows EC2 Client nodes before requiring normal Authenticode validation.

**Global constraints:**

- Work in the current repository tree; do not create another worktree.
- Document the workflow before implementation changes.
- Keep all private signing material ignored and untracked; never print passwords or private keys.
- Reuse the established signing identity for every future release. Never regenerate it to fix an error.
- Friends and EC2 nodes receive only the public certificate and signed artifacts.
- Require exact certificate identity, SHA-256 artifact hashes, valid Authenticode, and timestamps. Never fall back to unsigned output.
- SmartScreen reputation is not solved by self-signing. The first setup launch may still require **More info → Run anyway** and show **Unknown publisher** before trust is installed.
- Use test-driven development for production behavior and RED/GREEN pressure testing for the new skill.

## Task 1: Document the supported workflow first

- Update the README and Windows quick start to make `MobileEgressSetup.exe` the normal friend entry point.
- Replace public-CA-only wording in deployment documentation with the supported local publisher workflow.
- Document publisher initialization, routine release, restore, fingerprint sharing, SmartScreen/UAC behavior, EC2 trust establishment, backup, compromise, expiry, and removal limitations.
- Keep the security boundary explicit: an out-of-band fingerprint is the only identity check before the self-signed certificate is trusted.

## Task 2: Create and reuse the local publisher identity

- Add `scripts/setup-windows-signing.ps1` with `-Initialize`, `-ValidateOnly`, and `-Restore` modes.
- Generate `CN=Mobile Egress Local Publisher`, RSA-4096, SHA-256, Code Signing EKU, CA=false, ten-year validity, and an exportable private key in `Cert:\CurrentUser\My`.
- Store the encrypted PFX at `windows-signing/mobile-egress-code-signing.pfx` and its generated password in `windows-signing/signing.properties`; restrict both to the current user, SYSTEM, and Administrators.
- Track `windows-signing/mobile-egress-code-signing.cer` plus `windows-signing/release-signing-certificate.txt` with subject, SHA-1 thumbprint, SHA-256 certificate fingerprint, and expiry.
- Import the public certificate into the publisher user's Root and TrustedPublisher stores so local verification returns `Valid`.
- Refuse duplicate initialization, mismatched restore, tracked secrets, missing private keys/EKU, expired identities, or public-record drift.
- Run `-Initialize` once on this computer and verify that only public identity files appear in Git.

## Task 3: Build signed releases and a guided setup application

- Replace the Windows SDK `signtool.exe` dependency with `Set-AuthenticodeSignature` using SHA-256 and the configured timestamp endpoint.
- Discover the exact certificate from the tracked public identity. Keep the old thumbprint parameter only as an optional compatibility assertion.
- Sign and verify the controller, setup, admin helper, relay, and Client. Require `Valid`, exact thumbprint, exact SHA-256 certificate identity, and a timestamp.
- Add `MobileEgressSetup.exe` as an unelevated parent plus narrowly scoped elevated child.
- The parent displays the SHA-256 publisher fingerprint, requires explicit confirmation, stages a nonce-bound request, requests UAC, waits for a redacted result, then launches the installed controller unelevated.
- The elevated child imports only the embedded certificate into `LocalMachine\Root` and `LocalMachine\TrustedPublisher`, verifies itself and the controller/admin/relay siblings against that exact certificate, installs the package under `C:\Program Files\MobileEgress\Controller`, and creates a Start Menu shortcut.
- Reinstallation is idempotent. If newly added trust is followed by verification or installation failure, remove only the exact certificate entries added by that attempt.
- Keep the release ZIP self-contained with setup, all signed siblings, the audit manifest, and the public certificate.

## Task 4: Establish the same trust on EC2 Client nodes

- Bump the embedded node release manifest to version 2.
- Extend `NodeRelease` with `signerCertificateSha256` and bounded DER-base64 public certificate data.
- Validate the DER certificate, self-signature, Code Signing EKU, SHA-1 thumbprint, SHA-256 fingerprint, size, and expiry in Go before constructing SSM commands.
- In install/update SSM scripts: download, verify the pinned artifact SHA-256, confirm the untrusted signature carries the embedded exact certificate, import that certificate into LocalMachine Root and TrustedPublisher when absent, then re-run Authenticode and require `Valid` plus the exact thumbprint.
- Never accept a certificate downloaded separately from the release and never remove unrelated trust entries.

## Task 5: Add the reusable agent skill and verify the complete change

- Baseline-test realistic signing recovery scenarios without the new skill and capture unsafe rationalizations.
- Add `.agents/skills/mobile-egress-windows-signing/SKILL.md` with the established-key invariant, exact local paths and commands, routine release, restore, friend setup, EC2 troubleshooting, SmartScreen facts, and compromise boundary.
- Re-run the same scenarios with the skill and validate its structure with the bundled skill validator.
- Run focused PowerShell and Go tests, all Windows-targeted builds, operations checks, the repository full gate, signing validation, and one actual signed Windows release.
- Record any physical fresh-machine or EC2 acceptance checks that still require an external machine; do not claim they ran locally.
