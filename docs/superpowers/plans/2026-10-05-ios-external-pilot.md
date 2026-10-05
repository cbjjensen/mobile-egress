# External iPhone pilot in ZFNF Friends

The owner requested another tester and supplied their email after being told that external testing requires a compatible build and Apple's beta review. They then explicitly selected the existing **ZFNF Friends** external group. Reuse that group and invite only the supplied tester; do not create a second external group. Preserve its existing public-link settings without publishing the link. Do not grant App Store Connect roles or submit an App Store production version. The owner subsequently authorized all work on `main` and merging as needed. Existing internal testers may receive the build through their already-approved automatic settings.

## Inspection and implementation

The supplied tester is neither an existing App Store Connect user nor a beta tester. Current compatible build 2.0.0 (4) is `INTERNAL_ONLY` and cannot be used for external testing. The requested Friends group contains no testers and still points to legacy build 2. Detach that obsolete group/build association before inviting the new tester so it cannot offer the incompatible app. Preserve the uploaded historical artifact and all unrelated group associations.

- [x] Verify tester eligibility and current build distribution constraints.
- [x] Increment only the iOS build number to 5; preserve version 2.0.0 and all functional behavior.
- [ ] Archive/export exact committed source through the existing Xcode/manual-profile/Distribution identity path, with internal-only export disabled. Verify both bundles' signatures, capabilities, manifests, version, and transferred artifact hashes.
- [x] Reuse ZFNF Friends, detach only its obsolete build association, and add only the requested email. Keep tester identifiers/contact information in private evidence.
- [ ] Validate/upload the new build, wait for processing, and assign the exact current build to ZFNF Friends.
- [ ] Inspect required beta-review metadata. Supply truthful app/test notes; reuse valid existing review contacts. Never invent reviewer credentials or claim hardware acceptance. Record any missing review prerequisites as blockers.
- [ ] Submit for beta review when prerequisites are satisfied; verify Apple's returned status and record whether the tester can actually install yet.
- [ ] Synchronize the sibling pilot record and provide the owner accurate invitation/review/install status.

## Approved companion installer publication

The owner separately approved publishing the Windows and Mac pilot installers for reviewer/tester access, then approved working/merging on `main`. Use the existing coupled guarded release workflow from clean `main` to produce and publish matching **2.0.0** Windows and Mac installers as a prerelease. This avoids a separate pilot publication mechanism. Preserve the previously verified local pilot artifacts and all historical published builds.

The guarded release runs component gates and verifies Windows provenance/signatures/embedded payload, Mac signatures/notarization/stapling/Gatekeeper, the immutable source/tag/freeze record, and every uploaded GitHub asset digest before exposing the prerelease. Verify downloaded public bytes afterward. Publish only `MobileEgressClientSetup.exe` and `mobile-egress-client-macos-2.0.0-arm64.pkg`. Do not upload profiles, private verification records, recovery files, separate publisher certificates, signing keys, or old Agent builds. No Inevitable production deployment or stable promotion is part of this work.

These are limited pilot downloads, not a stable or fully physically accepted release. Apple reviewers can use the current companion installer and explicit Advanced direct setup without needing an Inevitable account, with reachable endpoint/cellular requirements explained. Hosted testing still requires eligible Client activation; do not invent reviewer credentials. Keep phone foreground-only availability and incomplete physical iPhone acceptance visible in review/release notes.

## Acceptance and rollback

The source change is build metadata only. Existing native/source tests from build 4 remain applicable; run exact-source signed archive/export, package validation, Apple upload validation/processing, and documentation checks for this build. Preserve the existing phone lifecycle distinction and no-usage-accounting contract. Signed physical iPhone install, pairing, cellular HTTP/CONNECT/SOCKS, cleanup and keep-awake acceptance remain open.

External beta review is an Apple-controlled gate. Adding an email to a group does not prove a usable invitation or mailbox delivery; distinguish registered, waiting for review, approved, invited, and installed states. A rollback would remove only this new build from private testing or expire it when requested, never delete existing testers, signing assets or pairings. No Inevitable runtime, paid provider inventory, billing/accounting, production environment or frontend pattern changes are needed.
