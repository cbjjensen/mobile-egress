# Multiple phones per Client implementation plan

Approved for implementation 2026-10-06. Goal: ten phones simultaneously per Windows/Mac Client, selected by separate stable loopback HTTP/SOCKS5 proxies. Preserve existing identities, pairing, first-phone proxy details, branded OLED UI and platform lifecycle constraints.

The canonical cross-repository [analysis](../../../../inevitable-proxies/technical-requirement-docs/2026-10-06-mobile-relay-multiple-phones/analysis.md), [plan](../../../../inevitable-proxies/technical-requirement-docs/2026-10-06-mobile-relay-multiple-phones/plan.md), [step ledger](../../../../inevitable-proxies/technical-requirement-docs/2026-10-06-mobile-relay-multiple-phones/implementation-summary.md) and [validation record](../../../../inevitable-proxies/technical-requirement-docs/2026-10-06-mobile-relay-multiple-phones/validation-report.md) must stay synchronized with implementation.

## Implementation outcome (2026-10-06)

Source implementation and independent review are complete. The [service report](2026-10-06-multiple-phones-service-report.md), [desktop UI report](2026-10-06-multiple-phones-ui-report.md), canonical step ledger and validation record document red/green checks, review repairs, decisions, source hashes and limitations. Ten real simulated phone sessions exercise independent authenticated HTTP/SOCKS traffic on the Client; one-route gateway fixtures cover same/cross-node traffic, reconnect/control bursts, EOF/cancellation and cleanup. No runtime phone, gateway, backend, database, commercial proxy or accounting changes were necessary. Website guidance changes are source-only.

The complete Windows component gate, 85 Client UI tests, 317 Android tests/lint/debug build, native Swift 395 tests (two existing platform-security skips), unsigned iOS device/simulator builds, native Mac Go tests/vet/build/race/production GUI, gateway native race/vet/build, 672 website tests and workspace build passed. Xcode's separate package test runner could not contact `testmanagerd` in the SSH session on two attempts; that gate is not passed. Physical cellular runs, installed APK/TestFlight compatibility, signed Windows/Mac upgrades and signed root Mac Keychain acceptance remain unverified. No installer, mobile app, website or gateway was published/deployed.

Implementation commits: `60342d8` (mobile compatibility evidence), `af44d50` (Client authority/runtime), `8f4ff14` (Client IPC/UI), sibling Inevitable `e2be7d0d` (website guidance, gateway tests and synchronized architecture/evidence). The initial plan commits are `1bd9100` here and `8c3fd4b9` in the sibling repository. Final documentation closeout is recorded in the canonical ledger. Publish website guidance only alongside/after verified multi-phone installers through the separately scoped release process.

## Component boundary

- Required: Windows/Mac protected service and GUI changes; website help/product guidance; both platforms' compatibility evidence; gateway one-route test coverage; current documentation.
- Not required by the design: backend/database/shared website API runtime changes, gateway production changes/configuration/deployment, commercial proxy changes, or new mobile wire fields/runtime features.
- Android/iOS distributed-build compatibility remains unverified until measured. No mobile update is mandatory by design. Record any incompatibility and revise the plan before implementing a repair.
- Website computer revocation affects all hosted phones on that Client; local removal affects one pairing. No backend phone inventory, local proxy secrets, usage, quotas or seats.

## Task 1: authority, state and runtime

Owner: nodeservice implementer. Files: windows-client/internal/nodeservice and narrowly necessary proxyendpoint helpers; tests colocated. Use a versioned ten-record protected collection in the existing state location. Atomic v2 migration retains original Client/CA/pairing/activation/pending invitation and first proxy credentials. Older binaries reject the new schema safely. No unbounded revoked history.

One active invitation reserves a slot. Ten-minute initial redemption, same-key retry and issued-identity recovery persist; expire only unredeemed invitations automatically. Explicit cancellation/removal frees the slot. Stable IDs are independent of list order and assigned before enrollment. Default labels Phone 1..10; trimmed 1..64-rune labels, empty Add name chooses default. Rename rejects blank/control-character labels.

Persist slot 0..9; SOCKS=1080+2*slot and HTTP=1081+2*slot. New pairings get random credentials; original active/pending phone preserves old details. Reusing removed slots never reuses credentials. Runtime owns one tunnel and two listeners per phone. An occupied port stops just that proxy pair with a retry action; never renumber. No pooling, replay or fallback.

Track serial admission, renewal overlap, acknowledged generation/endpoint, session generation/transport and endpoint update per phone. Preserve desired and still-needed acknowledged/pending hostnames in the shared server certificate. Removal must commit before reporting success; failed removal suppresses only that phone until a successful retry and reports uncertainty. Shared unreadable state fails closed. Test restart after failed persistence explicitly.

### Frozen service interface for Task 2

Add exported nodeservice types (JSON lower camel case):

- PhoneStatus: ID string (json phoneId), Name string, Slot int, Paired bool, Connected bool, UpdatePending bool, Phase string, Message string, SOCKSAddress string, HTTPAddress string, ProxyRunning bool, InvitationExpiresAt *time.Time (omitempty).
- PhonesStatus: Phones []PhoneStatus (always an array), MaxPhones int (10), PendingPhoneID string (omitempty).
- PhoneInvitation: PhoneID string, Bundle string (secret-bearing only on explicit authorized action).

Direct implements:

```go
Phones(context.Context) (PhonesStatus, error)
AddPhone(context.Context, string) (PhoneInvitation, error)
CancelPhoneInvitation(context.Context, string) error
RenamePhone(context.Context, string, string) error
RevokePhone(context.Context, string) error
PhoneProxy(context.Context, string, string) (string, error)
ExportPhoneEndpointUpdate(context.Context, string) (string, error)
RetryPhoneProxy(context.Context, string) error
```

AddPhone resumes the sole pending invitation rather than reserving duplicates. Existing methods remain for single-phone compatibility; phone-scoped legacy mutations/copy/export reject when ambiguous. Existing StandaloneStatus shape stays unchanged; aggregate Paired/Connected mean any acknowledged/ready phone; UpdatePending means any pending phone update. Multi-phone UI uses Phones status for per-phone decisions, not aggregate readiness. Multiple-phone legacy status has no misleading single proxy addresses. No secrets in status.

## Task 2: desktop local interface and UI

Owner: clientapp implementer; all windows-client/internal/clientapp production/tests. Add optional phone-service interface rather than breaking old test doubles/Service interfaces. Forward through firewall wrapper, authenticated local IPC and App GUI binding. Retain old response shape on old IPC requests; new phone-list response and explicit phone-ID operations are additive. Unknown IDs/invalid labels/unknown proxy kinds reject. Bind all actions and asynchronous results to stable phone ID so selection changes cannot misapply QR/copy/remove.

Dashboard lists names, phone status, HTTP/SOCKS copy and settings; Add phone available below ten slots. Selected phone settings provide rename, signed update QR/copy, port retry and confirmed removal. Add flow reuses setup verification without reactivation or disrupting siblings; cancel returns to dashboard. Keep first setup usable, existing OLED semantics/QR pixels/native theme, keyboard focus and mobile lifecycle copy. App exposes Phones, AddPhone(name), CancelPhoneInvitation(id), RenamePhone(id,name), RevokePhone(id), CopyPhoneProxy(id,kind), ExportPhoneEndpointUpdate(id), CopyPhoneEndpointUpdate(id), RetryPhoneProxy(id). AddPhone returns phoneId + existing BundleView fields. Maintain existing API only for legacy single-phone flows.

## Task 3: sibling website and gateway validation

Task 2 interface ruling: additionally expose `CopyPhoneInvitation(phoneID)` in App. Cache the explicit AddPhone result by stable phone ID; selected UI state must never silently choose another invitation. No additional public phone wire or service operation is required.

Owner: sibling implementer. Change only existing Mobile Relay website guidance and relevant tests; validate intended copy in rendered existing components. Add meaningful ten-phone/same-route gateway coverage (current capacity tests use ten routes), same/cross-node, exact bytes/EOF/cleanup and resource evidence. No gateway production/config/backend/contract edits unless a demonstrated defect causes documented plan revision. Keep website deployment separate from installer/link updates. Record detailed work in website-gateway-report.md in the canonical packet.

## Task 4: mobile parity, integration and documentation

Owner: root. Preserve Android/iOS runtime unless tests demonstrate need; add tracked compatibility evidence for independent phones sharing Client identity and pair-specific updates. Update docs/mobile-feature-manifest.json with real tracked source/test evidence. Run relevant Go/frontend/Android/native Mac/iOS and manifest checks; record unavailable physical gates honestly. Add/update current protocol/onboarding/architecture/operations/capacity docs and sibling accepted architecture; link superseding decision without rewriting historical evidence.

## Task 5: review and handoff

Independent review covers migration, trust admission, concurrency/callback isolation, local port/credential selection, UI/IPC and documentation truth. Repair findings with regressions. Record every coherent commit and exact checks in the canonical step ledger. No release claim without actual signed artifact/distribution evidence. No backend/gateway rollout prerequisite. Preserve scoped website-link and iOS auto-notify publisher requirements for any separately performed authorized publication.

## Validation and rollout

Before product edits run relevant baselines. Test migration and failed writes/restarts; ten plus rejected eleventh; real per-phone proxy routing/cross-credentials; lost response/retry; removal/reconnect/renewal; blocked ports; mixed-generation recovery; old IPC ambiguity; frontend transitions; same/cross-node ten phones on one attachment; aggregate bounds and cleanup. Test each meaningful phase before commit. Check complete component gates once integrated, then broaden only for changed/failing areas. Physical cellular/distributed-build acceptance is separate and unverified without hardware evidence.

Implementation uses existing main checkouts under owner authorization. New state cannot be opened by older binaries; prefer forward repair rather than stale-state restore that could resurrect revoked phones. No production deployment or stable promotion is performed merely to implement this plan.
