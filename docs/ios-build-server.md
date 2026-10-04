# Apple build server

Windows remains the editing/publishing workstation. Native Apple Silicon Mac builds produce the direct Client PKG and compile/test iOS. The Mac is development infrastructure, never a Mobile Egress traffic relay.

## Connection and isolation

Use the existing ignored publisher configuration at `.local/mac-build-server/release-desktop.psd1`, its configured SSH key and standard known_hosts. The repository Mac build-server skill documents the available environment. Do not print configuration values, private keys, signing passwords, provisioning profiles or device identifiers.

Before access, verify that configuration/key files are ignored and untracked. Use strict host-key checking. Work in a fresh private scratch directory when validating an uncommitted local snapshot; include modified/new files and exclude deleted/ignored files. Verify transferred hashes. Never reset, overwrite or clean the shared Mac checkout to run an unrelated snapshot. Remove only the exact verified scratch path after testing.

## Native validation

Run Swift package tests with warnings as errors, the Xcode package tests, and unsigned iPhoneOS/simulator app plus cleanup-extension builds. Simulator and unsigned results establish compilation/test behavior, not provisioned device serving. A physical iPhone with the appropriate development/distribution identities is required for cellular and keep-awake acceptance.

For the Mac Client, run native Go tests/vet/build and race checks for the direct runtime, IPC, policy and framing. The Client-only bootstrap must use the pinned Go toolchain without invoking the retired controller/frontend build. Ordinary tests do not exercise signed root System Keychain access; follow [its explicit native acceptance procedure](macos-keychain-integration.md).

## Distribution boundary

Direct Mac PKGs require the established Developer ID Application/Installer identities and notarization credentials. The old controller provisioning profile and node manifest are not requirements for 2.x Client packaging. The guarded release handoff verifies the exact source archive/commit, package hash, native verification record, notarization and staple before promoting a local artifact.

iOS signing and TestFlight remain a separate native workflow with the appropriate app, group and cleanup-extension entitlements. Do not reuse a simulator/unsigned build or an old relay Agent as a compatible release fallback. See [deployment](deployment.md) and the [iOS guide](../ios/README.md).
