# Apple build server

Windows remains the editing/publishing workstation. Native Apple Silicon Mac builds produce the Client PKG and, from Desktop 2.0.6, the app-only user DMG, and compile/test iOS. The Mac is development infrastructure, never a Mobile Egress traffic relay. Follow [Mac DMG release and acceptance](macos-user-dmg.md) for the new format's signing, mounted payload verification and private evidence; its standalone validation path does not freeze or publish a Desktop release.

## Connection and isolation

Use the existing ignored publisher configuration at `.local/mac-build-server/release-desktop.psd1`, its configured SSH key and standard known_hosts. The repository Mac build-server skill documents the available environment. Do not print configuration values, private keys, signing passwords, provisioning profiles or device identifiers.

Before access, verify that configuration/key files are ignored and untracked. Use strict host-key checking. Work in a fresh private scratch directory when validating an uncommitted local snapshot; include modified/new files and exclude deleted/ignored files. Verify transferred hashes. Never reset, overwrite or clean the shared Mac checkout to run an unrelated snapshot. Remove only the exact verified scratch path after testing.

## Native validation

Run Swift package tests with warnings as errors, the Xcode package tests, and unsigned iPhoneOS/simulator app plus cleanup-extension builds. Simulator and unsigned results establish compilation/test behavior, not provisioned device serving. A physical iPhone with the appropriate development/distribution identities is required for cellular and keep-awake acceptance.

For the Mac Client, run native Go tests/vet/build and race checks for the direct runtime, IPC, policy and framing. The Client-only bootstrap must use the pinned Go toolchain without invoking the retired controller/frontend build. Ordinary tests do not exercise signed root System Keychain access; follow [its explicit native acceptance procedure](macos-keychain-integration.md).

## Distribution boundary

Direct Mac PKGs require the established Developer ID Application/Installer identities and notarization credentials. The old controller provisioning profile and node manifest are not requirements for 2.x Client packaging. The guarded release handoff verifies the exact source archive/commit, package hash, native verification record, notarization and staple before promoting a local artifact.

iOS signing and TestFlight remain a separate native workflow with the appropriate app, group and cleanup-extension entitlements. Do not reuse a simulator/unsigned build or an old relay Agent as a compatible release fallback. See [deployment](deployment.md) and the [iOS guide](../ios/README.md).

## External TestFlight notifications

Website signups use the owner's **Inevitable Proxies Group** external group (`caff1c49-c628-464e-8a63-89e40d6a32a4`), created on 2026-10-07. **ZFNF Friends** (`c00ac6bc-4e4b-474a-b32d-823b80a8f01e`) and its existing testers are preserved separately. Verify and assign each future build to the explicitly intended groups; do not migrate existing testers or assume the website group change updates old group assignments. The example below targets the website group.

For each explicitly authorized compatible external release, verify `autoNotifyEnabled` **before assigning the build to the external group or submitting beta review**. The tracked helper uses the existing ignored Mac publisher configuration and keeps the private API key on the Mac:

```powershell
./scripts/set-ios-external-auto-notify.ps1 -AppId 6807680693 -GroupId caff1c49-c628-464e-8a63-89e40d6a32a4 -BuildId '<exact Apple build ID>' -ExpectedVersion '2.0.3' -ExpectedBuildNumber '<exact build number>'
```

The default is read-only and fails the release gate when the flag is false. For the owner's authorized release, repeat the exact command with `-Apply` to set only that build's automatic-notification flag, then verify the successful readback. The helper checks the exact app, existing external group, iOS version/build number, valid processing, external-capable distribution and expiry. It rejects versions outside 2.x and builds below the fixed 2.0.0 (7) compatibility floor. New code still needs its normal source, signing and device-acceptance review; metadata checks do not establish physical acceptance.

An already-enabled flag is a verified no-op, including on a currently testing build. To avoid late notification changes, applying a false flag is allowed only while Apple reports `READY_FOR_BETA_SUBMISSION`. If the mutation outcome is unknown, run the read-only command to inspect current state before any retry. Do not continue release/review until verification passes.

After this gate, follow the separately authorized exact-build group/review steps and verify Apple's external testing state. Existing group members gain access through TestFlight; Apple handles new-build notifications according to this setting, and device automatic updates depend on each tester's TestFlight settings. Do not send an additional tester invitation for a build update. Backend release discovery remains read-only and cannot replace this publisher gate. See [Apple's external-testing instructions](https://developer.apple.com/help/app-store-connect/test-a-beta-version/invite-external-testers/) and [build beta detail API](https://developer.apple.com/documentation/appstoreconnectapi/patch-v1-buildbetadetails-_id_).

Run the network-free helper regressions with `py -3 scripts/test-ios-external-auto-notify.py` on Windows or `python3 scripts/test-ios-external-auto-notify.py` on macOS.
