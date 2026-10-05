# Prepare the current hosted Mac Client installer

## Scope and implementation

The owner requested a usable current Mac Client installer after learning that only the incompatible historical 1.1.7 PKG was available. Prepare an immutable local Apple Silicon/macOS 13+ validation artifact from the existing hosted feature branch, including the latest product wording and footer removal. Preserve all installation, service, protected storage and signing identities. This task does not publish, tag a public release, merge, deploy Inevitable, or change protocol/mobile behavior.

- [x] Inspect the current source, guarded package pipeline, configuration presence and Mac prerequisites; preserve existing work.
- [x] Transfer an exact committed source bundle to the clean configured Mac build checkout and verify the revision.
- [x] Run current frontend, Go/native and installer/release-contract checks applicable to this package.
- [x] Build through `scripts/release-client-macos.sh` with existing Developer ID identities, Apple notarization, stapling, Gatekeeper and package verification. Use the existing desktop SSH/credential/record-verification helpers; do not reconstruct or bypass signing checks.
- [x] Retrieve the PKG and private verification record, compare local/remote SHA-256, and validate its source/version/platform identities.
- [x] Check native storage/installation prerequisites and record unavailable checks explicitly. Inspect and execute version checks on the signed packaged payload without installing it.
- [x] Synchronize acceptance and sibling pilot documentation and provide the installer path and remaining physical-test requirements.
- [ ] Follow-up requiring Mac administrator access and physical devices: signed root storage continuity, installation/repair/upgrade, owner handoff, boot/logout and phone traffic acceptance.

## Defaults and boundaries

Use a fresh `2.0.0-hosted-validation.20261004.8` artifact name unless occupied. Historical packages remain unchanged. The guarded Mac Client package script supports prerelease versions and clean exact-commit feature checkouts. Use that local packaging path because the general Desktop publication orchestrator requires clean main and couples Windows/tag operations, which are outside this request. All native package verification remains mandatory.

No new frontend patterns or infrastructure responsibilities are introduced. Existing hosted outbound connectivity, advanced direct behavior, local proxy endpoints, phone lifecycle limitations and all pairing state remain unchanged. The build host remains development infrastructure; building a package does not turn it into a workload server.

Rollback: no production installation is changed by package preparation. Retain immutable artifact hashes and exact source provenance. If any build prerequisite or verification fails, repair that specific failure and use a fresh artifact version if a final package has already been produced.

## Acceptance and initial findings

Required package evidence: arm64/macOS 13, existing app/daemon IDs and Developer ID signers, hardened runtime, notarization Accepted, valid stapled ticket, Gatekeeper acceptance, matching package hash and a validated private verification record. A package pass is not a physical phone traffic or boot/logout acceptance pass.

The initial read-only probe reached the configured Mac (macOS 26.2, arm64). The remote checkout is clean; pinned Go 1.26.7 and the configured notary key exist. `sudo -n true` reports that a password is required. The foreground GUI user differs from the SSH/build user. Do not infer installation ownership or reuse the login-keychain credential for sudo. Root System Keychain CRUD, signed upgrade continuity and package installation therefore remain blocked until suitable administrator execution is available. Signing/notarization and all unprivileged checks can proceed independently.

## Validation results

Source `75f1120ff0684f7ff93138c5e056ca1cc4a319b6` includes footer removal `e26dca4`. The exact Git bundle matched SHA-256 `cb27797fc9ccfff9ec4485d228c5a0df5e0a1ae805c0cd33cd9fcb1e284cb3dd` locally and on Mac before checkout. Native pinned Go 1.26.7 full uncached tests, vet and race tests passed, as did Node 24.20.0 JavaScript syntax and all 71 frontend tests. Expected deprecated file-based Keychain compiler warnings remain; guarded root integration tests were not run.

Independent publisher-workstation checks passed: release-all/desktop/direct contract suites, Mac record/CLI tests, Client installer contract tests, frontend syntax/71 tests, mobile feature manifest validation and its schema/exception regression suite. No product source repair or new frontend pattern was needed.

The guarded pipeline produced `mobile-egress-client-macos-2.0.0-hosted-validation.20261004.8-arm64.pkg` (13,766,003 bytes), SHA-256 `f71aefa63b910bfad8db3db1de872315d5cd119df1749856f303cfa5d2f1b9c8`. Developer ID app/daemon/package signing, hardened runtime, Apple notarization Accepted, staple validation and Gatekeeper checks passed. Downloaded bytes matched the remote hash and the existing Go Client-record verifier validated the private source/version/identity-bound record. The generic Desktop artifact helper cannot parse prerelease strings as PowerShell `[version]`; its underlying existing record verifier was called directly without weakening any record check.

Independent extraction reverified app and daemon signatures, exact embedded version, arm64, macOS 13 minimum, embedded VCS revision and `vcs.modified=false`. Both installer scripts matched source byte-for-byte and remained executable; plist and shell parsing passed. Extracted inspection files were cleaned up. A hash-identical, readable package copy is available on Mac at `/Users/Shared/mobile-egress-client-macos-2.0.0-hosted-validation.20261004.8-arm64.pkg` and on Windows under `windows-client/build/release/`.

Full evidence and remaining acceptance are in [Mac validation](../../mac-signed-validation-2026-10-04.md). No installation, release tag, publication, production change or privilege change occurred. The installer is ready for owner installation/testing; this is not a production acceptance sign-off.
