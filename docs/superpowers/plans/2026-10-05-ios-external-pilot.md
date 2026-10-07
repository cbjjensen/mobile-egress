# External iPhone pilot in ZFNF Friends

## Later website-group authorization (2026-10-07)

The owner subsequently requested a separate **Inevitable Proxies Group** for website signups, including the latest unsent request, and explicitly kept ZFNF Friends unchanged. This supersedes the original no-second-group restriction only for that website flow. External group `caff1c49-c628-464e-8a63-89e40d6a32a4` was created with public links disabled. The publisher notification helper verified automatic notifications before the already-approved 2.0.3 (8) build was assigned. The one selected pending request was retargeted with consent preserved and recovered through Inevitable's guarded processor. App-scoped Apple readback confirmed `INVITED` by `EMAIL`, membership in the new group only, and valid external build access at 2026-10-07 19:19 UTC. Mailbox receipt and installation remain unverified. See the sibling [implementation record](../../../../inevitable-proxies/technical-requirement-docs/2026-10-06-mobile-relay-self-service-invitations/implementation-summary.md) for deployment and recovery evidence.

## Original pilot authorization and record

The owner requested another tester and supplied their email after being told that external testing requires a compatible build and Apple's beta review. They then explicitly selected the existing **ZFNF Friends** external group. Reuse that group and invite only the supplied tester; do not create a second external group. Preserve its existing public-link settings without publishing the link. Do not grant App Store Connect roles or submit an App Store production version. The owner subsequently authorized all work on `main` and merging as needed. Existing internal testers may receive the build through their already-approved automatic settings.

## Inspection and implementation

The supplied tester is neither an existing App Store Connect user nor a beta tester. Current compatible build 2.0.0 (4) is `INTERNAL_ONLY` and cannot be used for external testing. The requested Friends group contains no testers and still points to legacy build 2. Detach that obsolete group/build association before inviting the new tester so it cannot offer the incompatible app. Preserve the uploaded historical artifact and all unrelated group associations.

- [x] Verify tester eligibility and current build distribution constraints.
- [x] Increment only the iOS build number to 5; preserve version 2.0.0 and all functional behavior.
- [x] Archive/export exact committed source through the existing Xcode/manual-profile/Distribution identity path, with internal-only export disabled. Verify both bundles' signatures, capabilities, manifests, version, and transferred artifact hashes.
- [x] Reuse ZFNF Friends, detach only its obsolete build association, and add only the requested email. Keep tester identifiers/contact information in private evidence.
- [x] Validate/upload the new build, wait for processing, and assign the exact current build to ZFNF Friends.
- [x] Inspect required beta-review metadata. Supply truthful app/test notes; reuse valid existing review contacts. Never invent reviewer credentials or claim hardware acceptance. Record any missing review prerequisites as blockers.
- [x] Submit for beta review when prerequisites are satisfied; verify Apple's returned status and record whether the tester can actually install yet.
- [x] Synchronize the sibling pilot record and provide the owner accurate invitation/review/install status.

## Approved companion installer publication

The owner separately approved publishing the Windows and Mac pilot installers for reviewer/tester access, then approved working/merging on `main`. Use the existing coupled guarded release workflow from clean `main` to produce and publish matching **2.0.0** Windows and Mac installers as a prerelease. This avoids a separate pilot publication mechanism. Preserve the previously verified local pilot artifacts and all historical published builds.

The guarded release runs component gates and verifies Windows provenance/signatures/embedded payload, Mac signatures/notarization/stapling/Gatekeeper, the immutable source/tag/freeze record, and every uploaded GitHub asset digest before exposing the prerelease. Verify downloaded public bytes afterward. Publish only `MobileEgressClientSetup.exe` and `mobile-egress-client-macos-2.0.0-arm64.pkg`. Do not upload profiles, private verification records, recovery files, separate publisher certificates, signing keys, or old Agent builds. No Inevitable production deployment or stable promotion is part of this work.

These are limited pilot downloads, not a stable or fully physically accepted release. Apple reviewers can use the current companion installer and explicit Advanced direct setup without needing an Inevitable account, with reachable endpoint/cellular requirements explained. Hosted testing still requires eligible Client activation; do not invent reviewer credentials. Keep phone foreground-only availability and incomplete physical iPhone acceptance visible in review/release notes.

## Results and remaining gates

The requested tester's membership in the existing **ZFNF Friends** external group is verified. Only that empty group's legacy build-2 association was removed; the uploaded historical build remains unexpired and unrelated groups remain unchanged. No App Store Connect user role or public-link setting was changed.

iPhone source `84ecb9810d65b352d918d9647d61fa3cbd6177c5` produced version 2.0.0/build 5. Compared with verified build 4, executable source is unchanged except for the build-number metadata. Normal Xcode archive/export and strict checks pass for both bundles, including original signer, distribution profiles, shared Keychain/App Group/Network Extension, privacy manifests, Keychain prefix and encryption declaration. The IPA is external-capable, 2,401,732 bytes, SHA-256 `5a12bac3d5ea4a586b4073db9d556a6e15fab75257f95edda043f267b587e55d`. Apple validation and upload both succeeded without errors. Private evidence is under `G:/codex-build-cache/mobile-egress-hosted-20261004/ios-external-20261005/`.

The guarded Desktop workflow published [v2.0.0](https://github.com/cbjjensen/mobile-egress/releases/tag/v2.0.0) as a prerelease from clean `main` source/tag `d7cf65bf09738658343f44810e0663c37e494b2b`. Windows release/component/installer contracts, mobile manifest/schema checks, Go tests/vet/build and all 71 frontend tests pass. Original Windows signatures, timestamp and embedded payload verify; Mac signing, notarization, stapling and Gatekeeper pass. An initial fixture path-string mismatch caused by forward-slash `GOTMPDIR` was reproduced and resolved with normalized Windows environment paths; no code/gate changes or skipped checks were needed. Both attempts are preserved.

Public anonymous downloads independently matched the frozen source-bound assets and GitHub digests:

| Installer | Bytes | SHA-256 |
|---|---:|---|
| `MobileEgressClientSetup.exe` | 25,677,088 | `480a26dc5fc35a7f6075b6dc4ac3b93408ccaaecacef00ed34373d20db6dab17` |
| `mobile-egress-client-macos-2.0.0-arm64.pkg` | 13,767,181 | `57d9378e42e26dda93c94a9b1a4115a3887128b04e99484b71b1e2d1461e00a1` |

Only those two installers were published. Existing pilot artifacts and historical releases remain preserved. No Android rebuild/publication, Inevitable deployment or device installation occurred. Publication evidence is under `pilot-publication-20261005/` beside the iPhone evidence.

Beta metadata now describes 2.x operation instead of the obsolete relay. Reviewer notes link the verified companion downloads and describe account-free Advanced direct setup, its incoming-network requirements, fresh ten-minute pairing, cellular-only traffic and iOS foreground lifecycle. Existing review contacts, feedback email and privacy fields were preserved. Independent review found no blocking metadata/API error.

Final Apple build/review ID `2184f20d-6afd-43fb-8331-dea32a58e128`: processing `VALID`, distribution `APP_STORE_ELIGIBLE`, internal testing active, external state `WAITING_FOR_BETA_REVIEW`, review `WAITING_FOR_REVIEW`. Build 5 is assigned to ZFNF Friends and auto-notification is enabled. This is a beta-review submission only, not an App Store production submission. The new tester cannot install the external build until Apple approves it. The tester API does not report a delivered/read/accepted invitation state, so mailbox delivery is not claimed.

Remaining external gates: Apple's review decision and actual tester acceptance/install; physical iPhone pairing, cellular HTTP/CONNECT/SOCKS, protected storage, legacy cleanup, interruption/reconnect and keep-awake validation. Current installer upgrades/repair and sustained multi-Client device acceptance retain their separately tracked limits. No stable promotion or full physical acceptance is claimed.

## Subsequent owner-authorized internal tester access

On 2026-10-05, after the internal/external distinction and App Store Connect access requirement were explained, the owner requested immediate installation for the specifically named additional tester. This supersedes the external-only/no-role restriction above for that tester alone. Invite that email with only `MARKETING`, restrict visible apps to ZFNF Mobile Egress, and disable all-app and provisioning access. Do not grant reports, developer, administrator, signing, or API-key access. Apple requires the recipient to accept the team invitation before internal TestFlight group membership can be completed. Use the current compatible build; preserve existing external review and unrelated users/groups. Keep personal contact information and invitation evidence private. Rollback, if requested, removes only the newly authorized access.

The invitation was created and verified through both invitation and visible-app readbacks. The only role is `MARKETING`, provisioning access is false, and the visible-app relationship contains only ZFNF Mobile Egress. Apple returns `allAppsVisible: null` despite an explicit false request; the explicit app relationship was checked rather than treating null as false. An immediate read returned 404 before the invitation became visible; a subsequent read confirmed the same single invitation, without resending. Recipient acceptance and subsequent internal-group enrollment remain pending. The existing internal Friends group already has compatible build 5 in testing; no review, historical build, or existing group setting was changed. Private evidence: `ios-external-20261005/parker-internal-invitation.json` under the existing build-cache root.

The recipient subsequently accepted the team invitation. Fresh API reads verified the accepted account retains only Marketing access, provisioning disabled and Mobile Egress as its sole visible app. Adding the exact existing tester to **ZFNF Friends Internal** returned HTTP 204; a separate membership read confirmed enrollment and preservation of all previous members. Compatible build 2.0.0 (5) is valid, unexpired and in internal testing in that group. Internal install access is now available without external beta approval; actual email receipt/device installation remain unverified. This resolves the pending acceptance/enrollment checkpoint above. Private evidence: `ios-external-20261005/parker-internal-membership.json`.

## Acceptance and rollback

The source change is build metadata only. Existing native/source tests from build 4 remain applicable; run exact-source signed archive/export, package validation, Apple upload validation/processing, and documentation checks for this build. Preserve the existing phone lifecycle distinction and no-usage-accounting contract. Signed physical iPhone install, pairing, cellular HTTP/CONNECT/SOCKS, cleanup and keep-awake acceptance remain open.

External beta review is an Apple-controlled gate. Adding an email to a group does not prove a usable invitation or mailbox delivery; distinguish registered, waiting for review, approved, invited, and installed states. A rollback would remove only this new build from private testing or expire it when requested, never delete existing testers, signing assets or pairings. No Inevitable runtime, paid provider inventory, billing/accounting, production environment or frontend pattern changes are needed.
