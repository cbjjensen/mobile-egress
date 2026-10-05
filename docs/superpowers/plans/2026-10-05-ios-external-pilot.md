# Private external iPhone pilot

The owner requested another tester and supplied their email after being told that external testing requires a compatible build and Apple's beta review. Prepare a private external TestFlight build and invite only the supplied tester. Do not grant App Store Connect roles, expose the existing public testing link, submit an App Store production version, or merge main. Existing internal testers may receive the build through their already-approved automatic settings.

## Inspection and implementation

The supplied tester is neither an existing App Store Connect user nor a beta tester. Current compatible build 2.0.0 (4) is `INTERNAL_ONLY` and cannot be used for external testing. Legacy build 2 is incompatible and must not be offered. Preserve both published builds and the existing public external group unchanged.

- [x] Verify tester eligibility and current build distribution constraints.
- [ ] Increment only the iOS build number to 5; preserve version 2.0.0 and all functional behavior.
- [ ] Archive/export exact committed source through the existing Xcode/manual-profile/Distribution identity path, with internal-only export disabled. Verify both bundles' signatures, capabilities, manifests, version, and transferred artifact hashes.
- [ ] Create or reuse a private external pilot group with no public link and add only the requested email. Keep tester identifiers/contact information in private evidence.
- [ ] Validate/upload the new build, wait for processing, and assign the exact current build to that private group.
- [ ] Inspect required beta-review metadata. Supply truthful app/test notes; reuse valid existing review contacts. Never invent reviewer credentials or claim hardware acceptance. Record any missing review prerequisites as blockers.
- [ ] Submit for beta review when prerequisites are satisfied; verify Apple's returned status and record whether the tester can actually install yet.
- [ ] Synchronize the sibling pilot record and provide the owner accurate invitation/review/install status.

## Acceptance and rollback

The source change is build metadata only. Existing native/source tests from build 4 remain applicable; run exact-source signed archive/export, package validation, Apple upload validation/processing, and documentation checks for this build. Preserve the existing phone lifecycle distinction and no-usage-accounting contract. Signed physical iPhone install, pairing, cellular HTTP/CONNECT/SOCKS, cleanup and keep-awake acceptance remain open.

External beta review is an Apple-controlled gate. Adding an email to a group does not prove a usable invitation or mailbox delivery; distinguish registered, waiting for review, approved, invited, and installed states. A rollback would remove only this new build from private testing or expire it when requested, never delete existing testers, signing assets or pairings. No Inevitable runtime, paid provider inventory, billing/accounting, production environment or frontend pattern changes are needed.
