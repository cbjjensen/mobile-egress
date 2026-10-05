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
- [ ] Prepare the iOS signing/profile prerequisites using the existing team and identities where possible; preserve unrelated apps/profiles/certificates.
- [ ] Archive and export an installable signed build; verify app/extension signatures, provisioning, entitlements, version and package hash.
- [ ] Upload/validate the intended private TestFlight build if that is the selected installation path. Confirm processing before claiming availability. Do not add unrelated testers or enable a public rollout.
- [ ] Provide the owner's actual installation path, preserving old artifacts, and record signing/device/review blockers explicitly.

## Acceptance and rollback

Source/build checks are separate from physical iPhone acceptance. Require actual install/launch, compact scan/pairing, cellular HTTP/CONNECT/SOCKS, protected key storage and lifecycle/keep-awake checks before release readiness. Do not claim unsigned compilation proves signed runtime behavior. The existing Xcode package runner's SSH testmanagerd limitation and opt-in Secure Enclave/Keychain checks remain documented.

Beta rollback means withdrawing only this new build from testing when requested; it does not revoke signing certificates, delete app records, change gateways or remove existing pairing state. No Inevitable infrastructure, commercial traffic, usage accounting or frontend patterns change.

## Preparation results

The owner's supplied email exactly matches the account-holder/Admin user. Use internal-only TestFlight distribution for that owner; do not attach this build to the existing public external group. The protected account lookup confirms that build 4 is not already uploaded.

The privacy manifest declares UserDefaults reasons `CA92.1` and `1C8F.1`, and elapsed-time reason `35F9.1`, based on [Apple's required-reason definitions](https://developer.apple.com/documentation/bundleresources/app-privacy-configuration/nsprivacyaccessedapitypes/nsprivacyaccessedapitypereasons). Release inspection found that linked core code retains both categories in the cleanup extension, so both bundles package the same manifest. No broad data-collection assertions were invented. The packaging test caught the initial missing extension resource, then passed with both bundles corrected.

Final native validation: 374 Swift tests, zero failures and two existing opt-in Secure Enclave/Keychain skips. Unsigned Release app/extension builds pass; both packaged manifests lint and exactly match source. Snapshot SHA-256 `92e0b1d75500203c47ed838094fabb536a36fd2768674b5254dfbab1d557cc1e`. Private evidence is under `G:/codex-build-cache/mobile-egress-hosted-20261004/ios-signed-20261005/privacy-validation/`. These results do not establish signing or device acceptance.

The planned signing path uses Xcode automatic cloud signing with the existing App Store Connect API key and team, rather than exporting/replacing the existing certificate's private key. An unsigned archive is an intermediate only; verify the actual distribution signature and profiles after export. Apple documents [cloud signing](https://developer.apple.com/videos/play/wwdc2021/10204/). Keep private build distribution separate from public release readiness.
