# Reconnect a saved phone after hosted reactivation

## Evidence and scope

The owner removed the computer in Inevitable and could not reconnect the phone through the desktop Client. Read-only installed status reports version `.3`, authorized, gateway connected, paired, generation 2, update pending and no live phone connection. The preceding accepted Windows/Android smoke used generation 1. Hosted reactivation changes the route; local authority, phone identity and proxy credentials are deliberately retained. An offline phone cannot receive the signed endpoint update through its former route. The phone's local pairing state was initially unknown; the owner subsequently confirmed it was also removed from Android, which requires a fresh pairing as recorded below.

Current desktop recovery is inside a collapsed dashboard-only "Recover after an address change" section. The verification step instead instructs starting the phone, and its recovery action is unavailable there. The correct existing operation is a signed connection-update QR, not a new invitation or resetting the phone pairing.

## Bounded implementation

- [x] Inspect live status and trace reactivation, endpoint generation and native phone update import without changing account/Client state.
- [x] Make a pending connection update a visible action in both setup and dashboard. Put **Reconnect your phone** first, provide the existing **Show connection update** action and explicit phone scanning instructions. Preserve manual recovery exports after an update is acknowledged.
- [x] Keep acknowledgement and live connection distinct. Retain pending state until acknowledgement, including an established session still acknowledging. Do not imply that gateway connection, export, or scanning alone completes recovery.
- [x] Remove obsolete pairing/Start guidance when an endpoint update is required. Clear old exported QR data on generation/configuration change and after acknowledgement; preserve unrelated errors.
- [x] Add regressions for paired offline reactivation, setup/dashboard, pending live acknowledgement, stale exports, unavailable service, and no extra enrollment or activation. Use existing service operations and wire format only.
- [x] Run focused and Windows/frontend/manifest gates, independent review, update documentation and sibling pilot evidence, commit and prepare a signed local validation build.
- [x] Help the owner recover, verify status/traffic when available, and record any user/device action still pending. Preserve saved pairing for update recovery; after the owner confirms deleting it on the phone, replace only the obsolete phone association through the normal protected API.

## Interfaces, rollback and acceptance

No new public endpoint, mobile login, backend routing change, protocol, credential or pairing replacement. Use protected `ExportEndpointUpdate`, current signed generation rules and native mobile scan/import support. Preserve direct-mode recovery and account-revocation enforcement. No infrastructure deployment, main merge or public release. Installer rollback preserves protected settings; the current app can already export the required update.

Success means the correct recovery action is immediately visible after a changed route, its QR updates the saved phone identity without duplicate enrollment, and only acknowledged/current authenticated phone state removes pending status and completes setup. Physical recovery remains unverified until the owner scans and status confirms it.

## Validation and owner recovery

The frontend regressions reproduced hidden recovery, premature continuation with an unacknowledged update, and stale exports after phone removal/service loss. The final 50 frontend tests and syntax checks pass. The full Windows gate passed Go tests/vet/build, installer/release contracts, manifest/schema and the initial 49 frontend cases; the final authorization-priority correction then passed all 50 frontend cases. Independent review found no remaining actionable findings.

The service required no production change. A new hosted-to-hosted reactivation regression proves authority/private key, phone certificate/key binding, Client/pairing identity and proxy credentials are retained; generation advances; restart retains pending state; the exported payload verifies under the existing pinned authority; old-origin acknowledgement fails; current-origin acknowledgement succeeds; and Connected still requires a current-generation session. The complete nodeservice suite passes. This is existing-protocol evidence, not physical acceptance.

An existing signed generation-2 update was exported through protected IPC and shown privately to the owner without changing any pairing/configuration. The owner reports an Android error after scanning; its exact wording is requested. Installed status remains authorized, gateway connected, paired, generation 2, update pending and disconnected. Physical recovery is not marked successful, and no pairing deletion, credential reset, new activation or backend deployment was attempted. Investigation of the Android error remains open.

The owner identified the error as **Client operation failed**, then confirmed the saved Client had also been removed from Android. Source tracing confirms the update importer has no saved pinned identity/key to match and rejects the update. This is expected trust behavior; the error wording is insufficiently specific. A future parity change should distinguish a missing saved pairing on both Android and iOS without bypassing verification or silently creating keys. No mobile code or APK was changed here.

For the owner's requested reattachment, protected IPC confirmed the old pairing was disconnected, then revoked only that now-removed phone association and issued a fresh ten-minute invitation. The account activation, hosted route/generation 2, authority and proxy settings were retained. The new QR was displayed privately and expires at 2026-10-04 23:34:49 UTC. Post-action status is authorized, gateway connected, unpaired, awaiting phone, and updatePending=false. Physical fresh-pairing/reconnection remains pending owner scanning; the separate saved-pairing signed-update path remains covered by software tests only.

### Completed owner recovery and signed build

The owner scanned the fresh invitation and started sharing. Protected status confirmed installed `.3`, authorized, gateway connected, paired, generation 2, Connected=true and UpdatePending=false. Three real requests passed again: HTTP absolute-form 200/5403ms, HTTPS over CONNECT 200/5713ms and HTTPS over SOCKS5 200/5713ms. All returned the same valid public IP, different from the direct PC comparison (200/162ms). Pairing and connection remained active afterward. These are small connectivity checks, not throughput or sustained latency qualification. Recovery required fresh phone pairing because the owner had explicitly removed the phone-side identity; the signed-update path for retained phone identities remains software-tested rather than physically accepted in this incident.

Source `e39428bc821fa375041ad8f4f4f3d38fadedcc62` is the tested desktop recovery fix. Guarded Windows validation build `2.0.0-hosted-validation.20261004.4` passed complete established-signer/timestamp, source/platform/version and embedded-payload verification. Installer SHA-256: `75B93EF71928F4E03F9B7539160E951B4586864C39320B9A2398043373583FF6`. It is prepared, not automatically installed over the recovered live connection. No release tag, main merge or deployment occurred. Mobile native behavior and manifest evidence are unchanged.
