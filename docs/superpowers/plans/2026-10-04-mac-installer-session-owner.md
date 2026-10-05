# Repair Mac installation owner detection

## Confirmed failure

The owner attempted historical 1.1.7 and current notarized `.8` on their existing Apple Silicon EC2 Mac over remote desktop. Both stop in `preinstall` with PackageKit error 112 and `Install while the intended Client owner is logged in.` No Client state directory, app, daemon or receipt was installed.

Read-only SSH diagnosis confirmed the active account is `ec2-user` (UID 501), `who` lists its console login and `launchctl print gui/501` succeeds, but `/dev/console` is owned by root. System Configuration's `State:/Users/ConsoleUser` reports the correct top-level `UID : 501` and completed foreground login. The installer incorrectly uses the device node's filesystem owner as its sole GUI-session identity. Its post-install handoff uses the same faulty source.

## Scope and steps

- [x] Preserve `.8` and existing state, inspect the real failed installation and record its cause.
- [x] Add failing regressions using the actual installer scripts for a root-owned console device with a valid GUI owner, fresh/headless setup, malformed/absent session identity, existing-owner preservation and safe GUI handoff.
- [x] Read the current GUI owner from macOS System Configuration in preinstall and postinstall; validate a non-system numeric UID and GUI domain. Fail closed for fresh installation without a valid session. Preserve saved installation ownership and retain the bounded, explicitly unelevated best-effort GUI launch.
- [x] Run installer/native/release/frontend/manifest checks and independent review. Keep the correction limited to Mac installer ownership detection and its diagnostics.
- [x] Commit exact source and produce a fresh signed/notarized `.9` using the existing guarded pipeline; never replace `.8` or historical assets.
- [x] Install/verify the repaired package on the owner's already provisioned rental through its existing authorized SSH access. Verify ownership, service, protected IPC, GUI and repair behavior; run signed native storage acceptance if available. Do not change paid resources, expiry, network/security groups, DCV or unrelated apps.
- [x] Record evidence and remaining physical/lifecycle gates in both repositories and provide the working installer/location.

## Acceptance and boundaries

The source regression must reproduce the failed fresh install before correction. The installed service and GUI must use existing identities, state paths and secure storage; pairing/configuration must survive repair. Session lookup must never silently assign root, a system UID or a malformed/ambiguous UID. Existing owners are preserved even during headless repair; handoff only launches into the matching active GUI session after explicitly dropping to that UID.

The latest user request is to resolve their failed installation, following their attempts and administrator authorization. Use the rental's existing access only for Client repair/acceptance. No infrastructure provisioning, customer AWS integration, public release, merge or phone reset is needed. The local build Mac remains a separate build host.

## Validation

The new actual-script regression reproduced the logged-in-owner preinstall failure and skipped postinstall GUI launch against unchanged production scripts. The fix extracts exactly one top-level UID, checks the scutil exit status before parsing, rejects malformed/duplicate/nested/system/overflow identities, and verifies the user's GUI domain. Existing saved ownership bypasses session discovery; postinstall discovery remains inside the existing bounded best-effort handoff.

Native package signatures/notarization on `.8` passed, including a fresh Gatekeeper/hash check on the owner's actual Mac; the actual installation failure supersedes any assumption that packaging tests alone establish installed readiness.

Source fix `fc8097a` passed Windows installer tests/vet, release-all/desktop/direct contract checks, manifest/schema checks and independent review. Its first native full run passed ordinary tests/vet but race execution hit two 10-second shell-fixture deadlines (`another_saved_owner`, `launch_failure`). Test-only follow-up `c36c758` limits fixture process fan-out to three chains before measured execution, retaining the original 8-second handoff assertions and 10-second command deadlines. Three consecutive focused Windows runs passed. Exact final source `c36c758116c73677c7530861964c146b7358e1b1` then passed native full uncached tests/vet/race and all 71 frontend tests/syntax. No test deadline or production timeout was relaxed.

The guarded pipeline produced notarized `mobile-egress-client-macos-2.0.0-hosted-validation.20261004.9-arm64.pkg` (13,766,321 bytes), SHA-256 `09e60569c1bf488d9062b95a1cd7ed7d43c326d19d6e9df885e61e06487422d1`. Signature/timestamp/identities, arm64/macOS 13, notarization, staple, Gatekeeper, source-bound verification record and transferred package hashes passed. It remains in the Windows release folder, the build Mac's Shared folder, and the owner's rental Transfers folder. `.8` was not overwritten.

Actual fresh installation on the owner's macOS 27/arm64 rental succeeded. Saved owner is UID 501; service runs as root; the GUI launches as UID 501; ordinary owner IPC returns version `.9`, hosted/inactive/waiting, unpaired and disconnected. Root-owned state is 0700, owner file 0600, runtime directory 0755, and peer-credential enforcement rejects another local UID while owner status succeeds. Same-package repair succeeded, restarted the daemon, retained the stable Client ID and owner, and retained Mac proxy endpoints `127.0.0.1:1081/1080`.

Guarded signed root System Keychain CRUD and phases A/B across two separately built and same-signed fixture binaries passed on the rental. Fixtures used source `fc8097a` (secure-store implementation unchanged in `c36c758`), retained the daemon signing identifier/team, and used random disposable items. Test items, phase state and remote fixture directories were cleaned up. This is native secure-store upgrade continuity, not a claim of completed paired-client version-upgrade or reboot acceptance.

Evidence: [Mac validation](../../mac-signed-validation-2026-10-04.md), local `G:/codex-build-cache/mobile-egress-hosted-20261004/mac-client-installer-9/`, and the synchronized Inevitable pilot record. Physical phone traffic, boot/logout, a different-version installed upgrade and retention of an already-paired phone across upgrade remain open. The working Client is left at account setup. Existing Windows/phone credentials and pairing, network/provider settings, rental expiry, and unrelated applications were unchanged; no public release or main merge occurred.
