# Prepare the current hosted Mac Client installer

## Scope and implementation

The owner requested a usable current Mac Client installer after learning that only the incompatible historical 1.1.7 PKG was available. Prepare an immutable local Apple Silicon/macOS 13+ validation artifact from the existing hosted feature branch, including the latest product wording and footer removal. Preserve all installation, service, protected storage and signing identities. This task does not publish, tag a public release, merge, deploy Inevitable, or change protocol/mobile behavior.

- [x] Inspect the current source, guarded package pipeline, configuration presence and Mac prerequisites; preserve existing work.
- [ ] Transfer an exact committed source bundle to the clean configured Mac build checkout and verify the revision.
- [ ] Run current frontend, Go/native and installer/release-contract checks applicable to this package.
- [ ] Build through `scripts/release-client-macos.sh` with existing Developer ID identities, Apple notarization, stapling, Gatekeeper and package verification. Use the existing desktop SSH/credential/record-verification helpers; do not reconstruct or bypass signing checks.
- [ ] Retrieve the PKG and private verification record, compare local/remote SHA-256, and validate its source/version/platform identities.
- [ ] Run available signed native storage and installation acceptance; record unavailable checks explicitly rather than marking them passed.
- [ ] Synchronize acceptance and sibling pilot documentation and provide the installer path and remaining physical-test requirements.

## Defaults and boundaries

Use a fresh `2.0.0-hosted-validation.20261004.8` artifact name unless occupied. Historical packages remain unchanged. The guarded Mac Client package script supports prerelease versions and clean exact-commit feature checkouts. Use that local packaging path because the general Desktop publication orchestrator requires clean main and couples Windows/tag operations, which are outside this request. All native package verification remains mandatory.

No new frontend patterns or infrastructure responsibilities are introduced. Existing hosted outbound connectivity, advanced direct behavior, local proxy endpoints, phone lifecycle limitations and all pairing state remain unchanged. The build host remains development infrastructure; building a package does not turn it into a workload server.

Rollback: no production installation is changed by package preparation. Retain immutable artifact hashes and exact source provenance. If any build prerequisite or verification fails, repair that specific failure and use a fresh artifact version if a final package has already been produced.

## Acceptance and initial findings

Required package evidence: arm64/macOS 13, existing app/daemon IDs and Developer ID signers, hardened runtime, notarization Accepted, valid stapled ticket, Gatekeeper acceptance, matching package hash and a validated private verification record. A package pass is not a physical phone traffic or boot/logout acceptance pass.

The initial read-only probe reached the configured Mac (macOS 26.2, arm64). The remote checkout is clean; pinned Go 1.26.7 and the configured notary key exist. `sudo -n true` reports that a password is required. The foreground GUI user differs from the SSH/build user. Do not infer installation ownership or reuse the login-keychain credential for sudo. Root System Keychain CRUD, signed upgrade continuity and package installation therefore remain blocked until suitable administrator execution is available. Signing/notarization and all unprivileged checks can proceed independently.

## Validation results

Pending.
