# Repair Mac installation owner detection

## Confirmed failure

The owner attempted historical 1.1.7 and current notarized `.8` on their existing Apple Silicon EC2 Mac over remote desktop. Both stop in `preinstall` with PackageKit error 112 and `Install while the intended Client owner is logged in.` No Client state directory, app, daemon or receipt was installed.

Read-only SSH diagnosis confirmed the active account is `ec2-user` (UID 501), `who` lists its console login and `launchctl print gui/501` succeeds, but `/dev/console` is owned by root. System Configuration's `State:/Users/ConsoleUser` reports the correct top-level `UID : 501` and completed foreground login. The installer incorrectly uses the device node's filesystem owner as its sole GUI-session identity. Its post-install handoff uses the same faulty source.

## Scope and steps

- [x] Preserve `.8` and existing state, inspect the real failed installation and record its cause.
- [x] Add failing regressions using the actual installer scripts for a root-owned console device with a valid GUI owner, fresh/headless setup, malformed/absent session identity, existing-owner preservation and safe GUI handoff.
- [x] Read the current GUI owner from macOS System Configuration in preinstall and postinstall; validate a non-system numeric UID and GUI domain. Fail closed for fresh installation without a valid session. Preserve saved installation ownership and retain the bounded, explicitly unelevated best-effort GUI launch.
- [ ] Run installer/native/release/frontend/manifest checks and independent review. Keep the correction limited to Mac installer ownership detection and its diagnostics.
- [ ] Commit exact source and produce a fresh signed/notarized `.9` using the existing guarded pipeline; never replace `.8` or historical assets.
- [ ] Install/verify the repaired package on the owner's already provisioned rental through its existing authorized SSH access. Verify ownership, service, protected IPC, GUI and repair behavior; run signed native storage acceptance if available. Do not change paid resources, expiry, network/security groups, DCV or unrelated apps.
- [ ] Record evidence and remaining physical/lifecycle gates in both repositories and provide the working installer/location.

## Acceptance and boundaries

The source regression must reproduce the failed fresh install before correction. The installed service and GUI must use existing identities, state paths and secure storage; pairing/configuration must survive repair. Session lookup must never silently assign root, a system UID or a malformed/ambiguous UID. Existing owners are preserved even during headless repair; handoff only launches into the matching active GUI session after explicitly dropping to that UID.

The latest user request is to resolve their failed installation, following their attempts and administrator authorization. Use the rental's existing access only for Client repair/acceptance. No infrastructure provisioning, customer AWS integration, public release, merge or phone reset is needed. The local build Mac remains a separate build host.

## Validation

The new actual-script regression reproduced the logged-in-owner preinstall failure and skipped postinstall GUI launch against unchanged production scripts. The fix extracts exactly one top-level UID, checks the scutil exit status before parsing, rejects malformed/duplicate/nested/system/overflow identities, and verifies the user's GUI domain. Existing saved ownership bypasses session discovery; postinstall discovery remains inside the existing bounded best-effort handoff.

Native package signatures/notarization on `.8` passed, including a fresh Gatekeeper/hash check on the owner's actual Mac; the actual installation failure supersedes any assumption that packaging tests alone establish installed readiness. Further validation is pending.
