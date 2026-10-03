# AWS-optional Windows and Mac Clients Implementation Plan

> Approved for implementation directly on `main` in `C:\Users\Chad\workspace\mobile-egress`. This document is the durable specification and execution checklist.

## Goal and approved design

Applications on non-AWS Windows machines and Apple Silicon Macs can use an installed, paired Client. AWS/SSM remains an optional management path for existing EC2 Clients. Traffic remains workload -> local authenticated proxy -> Tailscale Funnel -> the owner's personal Windows PC or Mac relay -> Android/iOS Agent -> cellular Internet. No hosted relay, inbound workload port, or workload Tailscale installation is introduced.

The standalone graphical Client supports pairing, service status, copying HTTP/SOCKS credentials, and importing connection updates. Its signed installer performs local updates and repair, preserving identities and credentials. The controller must remain open during pairing. Runtime traffic continues without the controller UI after setup, subject to the existing relay/controller availability requirements.

### Global constraints

- Implement and verify in the existing checkout on `main`; archive only the unused, clean setup worktree.
- Windows x64 Windows 10/11 and Server 2019+; Apple Silicon macOS 13+. Linux, Intel Mac, SSH provisioning, and remote software maintenance are deferred.
- At most ten Clients, including EC2, paired Clients, and pending reservations.
- Windows proxies stay `127.0.0.2:1080` (SOCKS) and `127.0.0.2:1081` (HTTP/CONNECT). Mac uses `127.0.0.1` with the same ports. Never silently fall back to another bind address or port.
- Client private keys remain on the workload machine; Owner private keys remain on the controller. Pairing invitations contain the pinned relay CA, origin, and a ten-minute capability, never Owner authority.
- Persist controller configuration before delivering the sealed envelope; make retries recoverable and identity issuance idempotent. Acknowledgement precedes completed setup.
- Preserve existing EC2 enrollment, release manifest, signer identity, Agent enrollment, wire protocol, and single-tunnel topology. New control operations are separate HTTP APIs.
- Windows background state uses service-account DPAPI. Mac uses a dedicated file-based System Keychain with service-only access, no plaintext fallback. Local administration requires authenticated OS IPC.
- Mac Client survives logout and resumes after boot when the OS and networking are available. This does not change the Mac relay/controller's existing logged-in-user dependency.
- Signed upgrades/repair retain state. User-visible output must exclude capabilities, raw secrets, certificates, destinations, and payloads except intentional proxy-copy/invitation actions.

## Implementation checklist

- [x] Save the approved design and implementation checklist before code changes.
- [x] Verify and archive the unused `aws-optional-clients` worktree (no unique commits or changes).
- [x] Generalize encrypted controller records with stable Client IDs, display name, platform, architecture, and management method (`aws-ssm`/`paired`); preserve legacy EC2 identifiers, credentials, certificates, generations, and reservations.
- [x] Add recoverable standalone enrollment handoff and Owner-visible Client status at the local relay, with scoped ten-minute invitations, durable key binding, cancellation, sealed configuration delivery, and authenticated acknowledgements.
- [x] Implement standalone Client service management and graphical UI, durable local key bootstrap, pairing/retry, status, proxy copying, revocation behavior, and endpoint import.
- [x] Add native Windows/Mac service installation and Mac System Keychain storage, plus signed Windows Client setup and signed/notarized Mac Client PKG build support.
- [x] Add generic controller bindings and Clients UI; default to Add Windows/Mac Client and retain Add from AWS. AWS failures affect only AWS operations.
- [x] Persist desired endpoint changes for all Clients. Apply EC2 updates when AWS is available; export sealed standalone updates and keep unacknowledged changes pending. Accept missed endpoint generations without replacing identity/credentials; reject stale, wrong-key, and incompatible updates.
- [x] Extend guarded Desktop release asset contracts while retaining existing raw Windows Client assets and immutable historical releases.
- [x] Update architecture, onboarding, operations, and status documentation; validate the mobile parity manifest and preserve mixed-version Android/iOS/EC2 interoperability.
- [x] Run focused tests, full Windows/frontend/release gates, native Mac checks where available, review the integrated diff, and record physical/signing acceptance truthfully.

## Interfaces and work boundaries

1. **Relay/control contracts:** a new portable `internal/clientcontrol` package defines strict standalone invitation, bootstrap, enrollment-state, sealed-delivery, receipt, and status types. Relay endpoints use the existing TLS listener and Owner mTLS; bootstrap requests require their scoped invitation capability. Preserve all current v1 Agent and EC2 endpoints. New relayclient helpers mirror these types. Store only public bootstrap, sealed envelopes, and safe status at the relay.
2. **Controller registry/UI:** extend `cloud.ManagedNode` and repository migration compatibly rather than renaming every existing package. Generic Client ID is distinct from optional AWS instance ID. Add generic invitation/cancellation/list/status/proxy/export/revoke desktop bindings while retaining existing EC2 wrappers. Keep installation state distinct from live relay connectivity.
3. **Workload service/UI:** share the existing nodeservice/proxy/session core. The workload UI communicates with the background service through OS-authenticated local IPC; it does not open a network administration listener. The service owns enrollment private keys, applies sealed state, exposes safe status, and reveals proxy credentials only through explicit copy actions.
4. **Platform/packaging:** retain `mobile-egress-client.exe` as the Windows service artifact. Add `mobile-egress-client-app` GUI command, `MobileEgressClientSetup.exe`, and `mobile-egress-client-macos-<version>-arm64.pkg`. Use separate Client app/native service identities so the controller and Client can coexist.

## Acceptance and regression tests

- No-AWS installation/pairing/configuration and HTTP/CONNECT/SOCKS requests; AWS unset/invalid must not prevent standalone use.
- Wrong/expired/reused invitation, competing bootstrap keys, cancellation, restart and lost response at each enrollment step, and concurrent admission at the combined limit.
- Secret-safe snapshots, authenticated local IPC, unavailable secure storage, wrong-key/tampered envelope, revocation, and proxy port conflicts.
- Signed upgrade/repair continuity, reboot/logout service recovery, and real Mac System Keychain access across same-signed upgrades.
- Mixed fleet migration with AWS offline, interrupted exports/imports, repeated endpoint rotations, skipped generations, unchanged credentials/identity, and pending-until-acknowledged UI.
- Existing EC2 install/update/repair/proxy contracts and older Client/Agent wire interoperability remain green.
- Full Windows Go tests/vet/build, frontend tests/typecheck/build, release contract tests, mobile-feature-manifest validator, and native Mac service/storage/build checks.
- Physical acceptance on a non-AWS Windows machine and Apple Silicon Mac with Android and iOS remains explicitly NOT RUN until performed; source tests never stand in for device, signing, notarization, or live AWS acceptance.

## Execution record

- 2026-10-03: Approved plan saved. Main checkout clean at baseline `99567e1c0fb41c4b76cdace9af369d36a8202ca0`. Unused managed worktree has no changes and zero commits unique to main; archived through the native worktree tool; the attachment now reports archived_worktree.
- All implementation stayed in the original checkout on main. Publishing releases and changing external accounts remain outside this source implementation task.

### Implementation decisions and recovery details

- Controller schema v3 extends the existing encrypted `cloud` repository instead of replacing the EC2 API. Stable IDs, metadata, pending invitations, applied generations, and live connectivity are separate. Original EC2 IDs/credentials/certificates/reservations survive migration.
- Relay schema v4 adds bounded standalone control state. Ten-minute invitations bind the first public bootstrap; approval and delivery survive lost responses and restarts. Only public material and sealed ciphertext are held at the relay. Existing Agent and traffic APIs remain unchanged.
- The exact initial generation-1 envelope is retained until receipt. If the origin changes mid-pairing, an explicitly revealed invitation refreshes only its origin; the workload reuses its keys/bootstrap, receives the initial configuration, and imports the newest endpoint update. Skipped endpoint generations do not alter credentials or identity.
- Older EC2 services require consecutive generations. An encrypted, bounded per-Client queue retains up to 256 updates/64 KiB and replays them in order. Each successful receipt is persisted before the next command. This was necessary to make repeated offline rotations recoverable without changing old Clients. History already discarded by older controller versions cannot be reconstructed; such a legacy record remains pending rather than being reset.
- AWS endpoint delivery uses an independent job with bounded per-Client attempts and a one-minute retry delay. One offline Client cannot starve healthy Clients. Standalone status, proxy copy, and revocation remain available while an unrelated AWS operation holds its provisioning lock.
- Native installation and secure storage are implemented as planned. The Mac workload Client uses its distinct root LaunchDaemon/System Keychain namespace; it does not change the personal Mac relay's logged-in-user availability requirement. Windows standalone setup rejects an existing AWS-managed service rather than taking it over.
- No destructive reset/re-pair feature was added for revoked identities. Signed installer repair deliberately preserves them; revocation continues to fail closed.

### Validation completed on 2026-10-03

| Check | Result |
|---|---|
| `scripts/test-all.ps1 -Components Windows` | PASS after review fixes: all Go packages, Client installer tagged payload contracts, Go vet/build, 53 frontend tests, TypeScript/build, release orchestrator contracts, mobile manifest validator |
| `scripts/test-all.ps1 -Components Android` | PASS: unit-test task, lint, debug assemble, release contract tests, mobile manifest validator |
| Focused cloud/desktop/relay/control/service tests | PASS: migration, shared ten-Client admission, expired/reused invitations, restart/lost-response recovery, exact sealed retries, skipped endpoint updates, immutable credentials, safe snapshots, occupied ports, revocation, mixed legacy delivery |
| Additional desktop regressions after full gate | PASS: no-AWS desired endpoint saved before QR failure; initial receipt missed across multiple rotations; standalone status/copy/revoke while AWS operation is blocked; offline EC2 does not starve next Client |
| Native Mac cgo tests | PASS: securestore, macosrelease, nodeservice, clientapp, proxyendpoint, SOCKS, HTTP/CONNECT |
| Native Mac unsigned builds | PASS: daemon and production GUI, ARM64 and macOS 13.0 minimum verified; opt-in System Keychain acceptance test compiles |
| Review regressions on Mac | PASS: actual umask 077 reproduced before fix and directory traversal corrected; consecutive installer builds, failure cleanup, preexisting-path preservation, final-artifact overwrite refusal |
| Independent integrated review | Findings fixed and re-reviewed: Mac IPC umask, per-Client AWS failure isolation, repeated Mac build staging, and real relay late-approved-poll rejection. No remaining material findings in follow-up review |
| Visual check | Synthetic controller screenshot inspected with Windows/Mac entries and platform-correct endpoints. Additional browser DOM inspection was blocked by automatic approval review without a specific reason. This is not native acceptance evidence |
| Native shell syntax and whitespace | PASS; shell files use LF, and `git diff --check` is clean |

The Windows workstation's previously recorded temporary GCC compiler is unavailable, so no Windows race claim is made. Native Mac `go test -race -json` passed for `internal/clientcontrol`, relay service, relayclient, cloud, nodeservice, and clientapp: six package passes, 281 top-level tests (435 passing events including subtests), zero failures/skips. Both `swift test --package-path ios` and the warnings-as-errors run passed: 308 tests, two explicit acceptance skips, zero failures per run. Logs were independently inspected under ignored `.local/mac-build-server/mobile-egress-final-check-52ce4e2e680b452b88705d6348f4b8ee-evidence/`. A PowerShell stdin trailing CR caused the final shell wrapper to exit 1 only after those test commands exited 0; JSON package results and Swift totals confirm their success. The final small expired-bound-pairing recovery regression is also checked separately with native nodeservice race instrumentation.

Final recovery review permits an expired invitation only for the exact locally persisted, unconfigured, non-revoked bootstrap/key binding, through polling the existing enrollment. New/unbound or changed-authority expired invitations still fail. Controller resume UI preserves the original expiry and only marks an approved stored configuration resumable. This closes an address-change-after-expiry recovery gap without extending invitation authority.

The real relay API regression first reproduced HTTP 401 on approved late recovery, then passed with the matching narrow server exception. Capability/hash and active-identity/revocation checks precede a poll-only exception for approved/delivered records with exact bound public keys. Expired submissions, unapproved records, altered CSR/X25519/capabilities, canceled/revoked records, and terminal acknowledged records remain rejected. Tests preserve the original certificate/ciphertext across relay restart. Final Windows relay/service/desktop tests, focused vet, full Go build, and all 53 frontend tests/typecheck/build passed after this delta.

The final native `go test -race -json -count=1 ./relay/internal/service` also passed after the real-handler change: 173 top-level tests, 300 passing events including subtests, zero skips/failures, command and transport exit 0. The final nodeservice race rerun passed 29 tests with zero skips/failures and exit 0. Both final JSON logs are retained beside the earlier evidence and their package pass events were inspected. Follow-up independent review confirmed the relay recovery issue resolved. Implementation and verification were performed in the `main` checkout. Release publication and PR creation were not part of this change.

### Remaining release and physical acceptance

- [ ] Produce and verify the signed Windows installer and Developer ID signed/notarized/stapled Mac PKG through a newly authorized guarded release. No release was published or existing tag changed here.
- [ ] Prove graphical no-AWS setup and real HTTP/CONNECT/SOCKS traffic on a non-AWS Windows workload and Apple Silicon Mac with real Android/iOS Agents.
- [ ] Verify real boot/logout recovery, signed upgrades/repair, signed root daemon System Keychain ACL/CRUD and same-signed upgrade access, occupied ports, unavailable storage, and actual mixed EC2/AWS outage scenarios.
- [ ] Run current native Xcode/device acceptance as applicable. Portable/component tests do not substitute for a live iOS device or the existing Xcode infrastructure gate.
