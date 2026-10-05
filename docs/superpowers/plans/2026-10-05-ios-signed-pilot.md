# Signed iPhone pilot build

The owner requested an installable iOS app after the compact QR update. Prepare the current hosted/direct 2.x app using the existing Apple team, registered app/extension and App Store Connect record. Work on the existing feature branch. This authorizes the iPhone beta preparation needed for testing; do not merge main, publish a GitHub/App Store production release, replace existing signing identities, or expose legacy builds as compatible downloads.

## Inspection and scope

- The Mac build server has valid Developer ID identities but no local Apple Development/Distribution identity or matching iOS profiles. The existing account has the Mobile Egress app and both registered bundle IDs, one distribution certificate, and internal/external ZFNF Friends groups. Preserve those resources.
- The only existing uploaded build predates 2.x and must not be handed out as a substitute. Current source is version 2.0.0, build 4; verify the upload number before using it.
- The iOS app needs its required-reason privacy manifest packaged with actual UserDefaults and uptime use. Preserve the cleanup extension, Network Extension/App Group/Keychain entitlements and approved foreground lifecycle.
- Use native Xcode archive/export and existing protected App Store Connect credentials. Keep API tokens, private keys, profiles, Apple IDs and device identifiers outside tracked output. If existing signing material cannot be used, diagnose the precise missing prerequisite; never revoke or replace an existing certificate to unblock a build.

## Checklist

- [x] Inspect existing source, build evidence and read-only Apple account/signing prerequisites.
- [x] Add and validate the required-reason privacy manifest against actual API use and Apple guidance.
- [x] Validate native tests and Release package structure from an exact source snapshot; committed archive verification follows.
- [x] Prepare the iOS signing/profile prerequisites using the existing team and identities where possible; preserve unrelated apps/profiles/certificates.
- [x] Archive and export an installable signed build; verify app/extension signatures, provisioning, entitlements, version and package hash.
- [x] Upload/validate the intended private TestFlight build if that is the selected installation path. Confirm processing before claiming availability. Do not add unrelated testers or enable a public rollout.
- [x] Provide the owner's actual installation path, preserving old artifacts, and record signing/device/review blockers explicitly.

## Acceptance and rollback

Source/build checks are separate from physical iPhone acceptance. Require actual install/launch, compact scan/pairing, cellular HTTP/CONNECT/SOCKS, protected key storage and lifecycle/keep-awake checks before release readiness. Do not claim unsigned compilation proves signed runtime behavior. The existing Xcode package runner's SSH testmanagerd limitation and opt-in Secure Enclave/Keychain checks remain documented.

Beta rollback means withdrawing only this new build from testing when requested; it does not revoke signing certificates, delete app records, change gateways or remove existing pairing state. No Inevitable infrastructure, commercial traffic, usage accounting or frontend patterns change.

## Preparation results

The owner's supplied email exactly matches the account-holder/Admin user. Use internal-only TestFlight distribution for that owner; do not attach this build to the existing public external group. The protected account lookup confirms that build 4 is not already uploaded.

The privacy manifest declares UserDefaults reasons `CA92.1` and `1C8F.1`, and elapsed-time reason `35F9.1`, based on [Apple's required-reason definitions](https://developer.apple.com/documentation/bundleresources/app-privacy-configuration/nsprivacyaccessedapitypes/nsprivacyaccessedapitypereasons). Release inspection found that linked core code retains both categories in the cleanup extension, so both bundles package the same manifest. No broad data-collection assertions were invented. The packaging test caught the initial missing extension resource, then passed with both bundles corrected.

Final native validation: 374 Swift tests, zero failures and two existing opt-in Secure Enclave/Keychain skips. Unsigned Release app/extension builds pass; both packaged manifests lint and exactly match source. Snapshot SHA-256 `92e0b1d75500203c47ed838094fabb536a36fd2768674b5254dfbab1d557cc1e`. Private evidence is under `G:/codex-build-cache/mobile-egress-hosted-20261004/ios-signed-20261005/privacy-validation/`. These results do not establish signing or device acceptance.

The initial automatic cloud-signing attempt was denied by the API key's cloud-signing permissions. Read-only inspection found the original usable Apple Distribution identity and matching profiles in the build Mac's earlier signing account. The existing configured credential unlocks that Keychain; no certificate replacement, account permission change or private-key export is needed. Use that established signing account for the archive and export.

The first local export from the unsigned intermediate succeeded, but package verification correctly rejected both bundles because their signed entitlements omitted the shared Keychain, App Group and Network Extension capabilities. Preserve this rejected artifact as evidence; do not upload it. Rebuild the exact committed source with signing enabled during archive, then repeat the full signature/entitlement checks before delivery. Export success alone is not an acceptance gate.

Xcode's automatic archive requires a development identity, while the cached matching profiles are managed distribution profiles. An explicit intermediate signature restores the capabilities, but Apple's guidance prefers Xcode's normal signing flow for iOS. For delivery, create narrowly named manual App Store profiles for only these two existing bundle IDs using the original Distribution certificate, then perform a normal signed Xcode archive/export. Preserve every existing certificate and profile. This is a signing-preparation change, not a product capability change.

Read-only distribution inspection found one existing internal tester with automatic access to all builds, separate from the owner's supplied address. The owner explicitly approved leaving that private testing access unchanged, preferring the simplest path. The upload therefore preserves existing internal distribution and does not promise owner-exclusive access. The public external group must not receive this build.

The source crypto audit found only Apple Security/Secure Enclave/Network implementations, no external package dependencies or bundled crypto. CSR/DER code formats public material rather than implementing cryptographic primitives. The app therefore declares no non-exempt encryption under [Apple's OS-encryption guidance](https://developer.apple.com/documentation/security/complying-with-encryption-export-regulations). Reassess if those dependencies change; this metadata does not claim that traffic is unencrypted.

## Signed package verification

The final delivery artifact uses normal Xcode archive/export with the original Distribution identity and two new dedicated manual App Store profiles for the existing bundle IDs. No original certificate/profile was revoked, replaced or edited. The earlier unsigned-export and explicit-signature experiments are preserved separately and are not delivery artifacts. Generated signing configuration stays private and outside the source tree; it selects a profile per target through `PROVISIONING_PROFILE_SPECIFIER = $(PILOT_PROFILE_$(PRODUCT_NAME))` and supplies the existing team/prefix.

Exact source: `3cce34e41d4031f994456c391854f8007ef42437`. Version **2.0.0**, build **4**. Native Release archive and App Store Connect export succeeded. The final app and extension pass strict signature verification and have the original signer, exact bundle/application identifiers, distribution profiles, shared Keychain/App Group, packet-tunnel entitlement, required-reason privacy manifest and expected versions. The main app has the provisioned Keychain prefix, OS-only encryption metadata and `TFInternalTestingOnly = true`.

Final IPA: `zfnf-mobile-egress-ios-2.0.0-build4.ipa`, 2,401,742 bytes, SHA-256 `0995576a33d9f7da122a36f8fbe140c7983c99b8d0a7da6e3b857ced2be3b7f5`. It is preserved under the private `G:/codex-build-cache/mobile-egress-hosted-20261004/ios-signed-20261005/` evidence directory alongside native archive/export logs and the verification report; both download and transfer to the upload account matched this hash. The mobile manifest validator passes. Apple `altool --validate-app` returned **VERIFY SUCCEEDED with no errors** for this exact IPA; its result is preserved as `apple-validate.json`.

A dedicated **Mobile Egress owner pilot** internal group was created with automatic access to all builds disabled and no public link. The supplied account-owner email was added and its exact membership verified; no unrelated tester was added. The existing internal group's automatic distribution setting remains untouched, as approved. The existing public external group receives no new build.

Apple accepted the exact verified IPA: `UPLOAD SUCCEEDED with no errors`, delivery UUID `05f9c391-1628-4079-b2df-0e6c6f6ab269`, 2,401,742 bytes. The result is preserved as `apple-upload.json`.

Final App Store Connect verification confirms version **2.0.0**, build **4**, processing `VALID`, audience `INTERNAL_ONLY`, not expired, no non-exempt encryption, internal state `IN_BETA_TESTING`, external state `NOT_APPLICABLE`. That exact build was assigned to **Mobile Egress owner pilot** and read back from the group's build list; the owner's exact email membership is verified. The existing automatic internal group also has access, as approved. Final evidence is `testflight-status.json` and `owner-build-assignment.json` in the private evidence folder.

Installation path: install Apple's TestFlight app on the iPhone, open Apple's invitation at the supplied owner email, and install **ZFNF Mobile Egress 2.0.0 (4)**. The tester API reports email invitation type but no tester state; mailbox delivery/read/acceptance is not proven. Internal testing is active, with no external review or public App Store release. The owner must accept/install on their own iPhone before physical acceptance can proceed.

Physical installation, launch, compact QR pairing, cellular HTTP/CONNECT/SOCKS, Secure Enclave/Keychain execution, legacy-profile cleanup, screen locking/app switching and keep-awake acceptance remain unperformed. A signed IPA is not a generally sideloadable iPhone installer; provide TestFlight access only after Apple processing and tester assignment succeed. Do not offer legacy build 2 as a fallback.
