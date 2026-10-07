# Multiple phones service implementation evidence

Owner: service implementation agent. Scope: nodeservice authority, protected state, runtime and tests. No deployment or commits by this agent.

## Step 1 — inspect and establish red tests

Read the approved plan, repository AGENTS.md, PowerShell safety and TDD/writing-good-tests instructions. Inspected singleton state, admission, renewal, endpoint notification, runtime and existing tests. Existing wire fields and shared authority remain unchanged.

Added behavioral tests for ten persisted reservations/phones, rejected eleventh, stable slots and IDs, credential rotation on slot reuse, sibling preservation, legacy ambiguity, labels and expired unredeemed reservation cleanup. `go test ./windows-client/internal/nodeservice` failed as expected because the frozen phone methods do not exist. This is an API compile-red; behavioral red follows once the exported interface compiles.

Decision: schema version 3 remains in the existing protected record key, so version-2-only older binaries reject it. Legacy singleton JSON fields are migration inputs only; the phone collection is canonical. First-use credentials remain available until the first reservation, preserving existing unpaired installations as well.

## Step 2 — implement canonical state, authority and frozen interface

Added the frozen `PhoneStatus`, `PhonesStatus`, `PhoneInvitation` and eight methods without changing the shared wire contract. Stable phone IDs are generated before enrollment. The collection holds each phone's slot, name, invitation, pairing, acknowledged endpoint/generation and proxy credentials. A separate runtime map owns each opener, active serial, session generation/transport, listener pair and failed-removal suppression. Admission resolves the authenticated serial and public key to exactly one record. Renewal overlap and retirement mutate that record only.

Added ten slots, one pending reservation, default/rune-limited labels, explicit cancellation/removal, per-phone proxy copy/update export/retry, and ambiguity rejection in legacy mutations/copy/export. Shared endpoint changes keep each phone's needed acknowledged/pending hostname in the CA-signed server certificate. Aggregate status uses any ready/paired phone and any pending update; multiple phones omit misleading single-proxy addresses.

The first full suite exposed old tests accessing singleton internals, two tests calling public Status while holding its new authority lock, old assumptions that configuration changes discard invitation recovery, and a pending pairing phase being overwritten by update_pending. Adapted fixtures to explicit first-phone records, removed/released fixture-only nested locking, made canonical-origin tests explicitly cancel their earlier fixture invitation, retained exact invitation recovery across endpoint/transport changes, and kept initial issued identities in acknowledging phase. Existing coverage was retained.

`go test ./windows-client/internal/nodeservice -timeout 30s` passed after these changes (6.158s). The initial full runs before repairs failed the named activation/canonical-origin/pending-phase cases and timed out in `TestMigrationVerificationRequiresCurrentModeGenerationPhoneSession`; these were fixture/behavior mismatches described above, not silently ignored.

## Step 3 — migration and failed persistence red/green

Added v2 invitation/issued/acknowledged migration fixtures built independently of the collection, failed-write byte preservation, restart/stable-ID checks, retained original Client/CA/identity/credentials and the schema-version rejection boundary used by older binaries. Added failed removal -> scoped suppression -> restart retaining the old durable identity -> successful retry -> restart rejecting that identity, with sibling admission throughout. Failed writes explicitly do not claim durable revocation; status says restarting may restore saved access.

`go test ./windows-client/internal/nodeservice -run 'TestPhonesInvalidMigration|TestPhonesVersion2|TestPhoneFailedRemoval' -count=1` failed at `unreadable state admitted on retry`. The candidate was initially published by save before all validation. Moved CA, configuration, hosted, activation and TLS validation before persistence/publication. Full suite then passed (6.226s).

Added pre-redemption endpoint-change recovery and revoked-v2 credential rotation tests. `go test ./windows-client/internal/nodeservice -run 'TestPhoneInvitationSurvives|TestPhoneRevokedV2' -count=1` failed with `old invitation issued an unreachable generation 2` and `removed v2 credentials reused`. Added protected invitationGeneration (no new mobile wire field), preserving the endpoint generation represented by the QR through later enrollment, and discarded revoked v2 credentials. The same focused tests plus v2 migration passed (0.828s). Existing endpoint fixtures now explicitly cancel their old invitation before testing a newly configured endpoint. Already issued/pending invitations remain recoverable after their initial ten-minute redemption deadline; only unredeemed reservations expire automatically.

## Step 4 — real sessions and proxy isolation

Added `TestTenPhonesRealProxyRoutingCredentialIsolationAndRemoval`: ten real authenticated TLS/WebSocket phone sessions share one Client authority/listener. Both actual SOCKS5 and HTTP CONNECT listeners exchange tagged payloads concurrently, reject each neighboring phone's credentials, preserve response tails/EOF, replace only the selected session, reject removed-phone reconnection and keep every sibling routing after removal. The proxy-pair constructor is shared by production fixed-port startup and ephemeral test listeners; this avoids touching the installed Client.

The first command failed to compile because the shared proxy-pair constructor did not yet exist. Extracted the existing startup into that constructor with rollback on the second bind failure. `go test ./windows-client/internal/nodeservice -run TestTenPhonesReal -count=1 -timeout 30s` passed (0.916s).

Verified the installed Windows Client occupies 127.0.0.2:1080/1081 and left it running. Preserved the platform-specific existing proxyendpoint.Host (Windows .2, Mac .1), with ports 1080+2*slot and 1081+2*slot. Added `TestPhoneOccupiedPortRollsBackOnlyItsPairAndRetriesStablePorts`, reserving slot 9's HTTP port temporarily, verifying the partially started SOCKS listener closes, sibling ephemeral listeners remain running, and release/retry restores the identical fixed ports and credentials. It skips rather than interferes if an external process already owns slot 9. On this Windows run it passed without skipping.

`go test ./windows-client/internal/nodeservice -run 'TestPhoneOccupied|TestTenPhonesReal' -v -count=1 -timeout 30s` passed both tests (0.980s).

## Step 5 — independent review repairs and isolation

Reviewer identified slow synchronous endpoint-update writes holding the shared authority lock, nil-configuration collection acceptance, revoked-v2 credential reuse (already repaired), and failed expiration hiding healthy sibling status. Each behavior received an executable regression.

`go test ./windows-client/internal/nodeservice -run 'TestPhoneSlowUpdate|TestPhonesWithoutConfiguration' -count=1` failed with `slow phone update blocked sibling authority` and `phone collection without configuration accepted`. Endpoint notifications now use at most one worker and one coalesced pending notification per phone, write outside the shared lock, and bind queued results to the runtime/session instance. Collections with phones require configuration. The blocked sender test verifies sibling rename completes while the selected update sender is blocked.

`go test ./windows-client/internal/nodeservice -run 'TestPhoneFailedExpiration|TestPhoneMixedGeneration' -count=1` failed only because failed expiration returned an empty list/error. Phones now reports cached sibling status and the target's explicit persistence uncertainty; Add still refuses to reuse an uncommitted slot. Mixed-generation tests verify old/middle/latest SAN coverage after restart, phone-specific signed update pairing IDs, independent acknowledged generations and renewal serial retirement.

`go test ./windows-client/internal/nodeservice -count=1 -timeout 30s` passed after these repairs (6.286s). No mobile runtime, backend, commercial proxy, gateway production or deployment changes were made by this work. Distributed mobile builds/physical cellular validation and signed installer publication are not claimed by these service tests. Root owns integrated/native/race gates and coherent commits.

## Integrated handoff

Final implementer rerun passed in 6.500s. Independent review closed all findings and passed focused service/IPC/frontend/gateway checks. Root's full Windows gate passed, followed by native Apple Silicon Go tests/vet/build, all eight selected race packages (nodeservice 8.415s) and unsigned daemon/production-tag GUI builds. See the canonical [validation record](../../../../inevitable-proxies/technical-requirement-docs/2026-10-06-mobile-relay-multiple-phones/validation-report.md) for commands, source hashes and remaining physical/distribution gates. Old binaries reject schema 3; restoring an older protected-state backup is not cryptographically prevented and can restore obsolete trust, so forward repair remains the documented recovery path.
