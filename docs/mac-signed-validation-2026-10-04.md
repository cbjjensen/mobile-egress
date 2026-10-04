# Mac signed native-storage validation, 2026-10-04

Native System Keychain acceptance remains blocked by existing noninteractive root permissions. Signing and designated-requirement verification passed after the documented configured login-keychain unlock; this check does not establish signed root storage continuity or installed-package acceptance.

## Evidence

Read the Mac build-server and release skills, `docs/macos-keychain-integration.md`, and the complete guarded `windows-client/internal/securestore/system_keychain_darwin_integration_test.go` before checking the Mac. The project SSH key is ignored and untracked. Existing SSH access succeeded to `chad.jensen@Y9YD7JN54M.local`.

At 2026-10-04 07:15:56 UTC the Mac reported macOS 26.2, build 25C56. `security find-identity` reported valid existing identities:

- Developer ID Application: Chad Jensen (26VMY3JMQ9), fingerprint `6F80A3DBA9B328275E6FDD07DD06DC1E5DE96A06`.
- Developer ID Installer: Chad Jensen (26VMY3JMQ9), fingerprint `6844E11B6162C6400D4A71EC07BC1C6AAA777851`.

Both `sudo -n -l` and `sudo -n true` exited 1 with `sudo: a password is required`. Thus the current SSH session cannot perform the required root test execution using existing noninteractive permissions. No password was requested or supplied and no permission changes were attempted.

The ordinary Homebrew `go` is 1.21.1 and was not used to build these tests. The separately established pinned Go 1.26.7 validation environment remains required for any later execution.

## Scope and remaining acceptance

`TestSignedRootSystemKeychainCRUD` uses a random `native-acceptance-` fixture item and exact-item cleanup. `TestSystemKeychainSameSignedUpgrade` uses a random `signed-upgrade-` item and an absolute private state file containing its name; phases A and B require separately built binaries signed with the same Developer ID and daemon identifier. Both require root System Keychain access. Neither test was run, so this report claims no native storage pass.

A private owned scratch directory was created for compile/sign preparation and removed after inspection; no test item or fixture state was created. No installed application, daemon, production state directory, System Keychain item, signing identity, software installation, notarization, release tag, or published artifact was changed. Installer identity presence is not signed PKG installation/repair, logout, or reboot acceptance.

A later authorized acceptance run must use a new private scratch directory, pinned Go 1.26.7, signed test binaries with identifier `com.zfnf.mobile-egress.client`, and existing permitted root execution. It must retain the exact fixture state for cleanup if phase B fails. Installed-package acceptance remains a separate task.

## Preserved local evidence

Source revision inspected: `7430ba63ace5597d0453ac23d6eaeb8bcf155a83`.

Probe log: `G:/codex-build-cache/mobile-egress-hosted-20261004/mac-signed-storage/permissions-and-identities.log`.

- Log SHA-256: `1C6FC0A97D968B2017A689866CC8CD47B763B940E23C223CEF56109C2DEDD170`.
- Guarded integration test source SHA-256: `71172F4F1E51E122A1ECF1FBC66B809E47C99ADFB62FAA0DB18B8E012F35FA62`.

## Isolated compile/sign preparation

A fresh archive of the inspected revision was uploaded to private scratch `/Users/chad.jensen/mobile-egress-validation/signed-storage-20261004-0716`; local and remote SHA-256 matched `17109CD64A10325C246EF7137EFB7986B88A4E10C9B4C6D98C01F07DD747CF62`. The guarded securestore test binary compiled successfully with pinned Go 1.26.7, `CGO_ENABLED=1`, `GOTOOLCHAIN=local`, and macOS deployment target 13.0 using the established build cache. Compiler output contained the expected deprecated file-based Keychain API warnings.

Signing with the existing Application identity and requested identifier `com.zfnf.mobile-egress.client` failed with `errSecInternalComponent` (exit 1), confirmed by a second bounded attempt. Inspection showed only the original linker ad-hoc signature (`Identifier=a.out`, no TeamIdentifier). Identity discovery passed, but usable noninteractive signing did not pass; the error alone does not establish its underlying cause. No keychain unlock, credential request, identity/ACL modification, or signing workaround was attempted. Neither fixture test was executed as a user or root.

The compiled binary SHA-256 before cleanup was `8D5E579C3E21401B0829E07D2FC82EE096949A01885E0A5155A5F2D38945B9D7`. The owned Mac scratch directory was removed and absence verified. Local archive and `build-and-sign.log` remain under the evidence directory above; signing log SHA-256 is `76AA28FD0846CAEA22B9F1169EE2176E83BEF822DDAEA98C69C1FEB3CC6A4D21`.

Result: compile preparation **PASS**; certificate discovery **PASS**; initial noninteractive Developer ID signing **BLOCKED** (resolved by the configured unlock below); root native CRUD and same-signed upgrade acceptance **ROOTBLOCKED**. Signed PKG and installed-daemon lifecycle acceptance remain unperformed.

## Configured signing unlock and final result

An additional bounded check used `Get-MobileEgressDesktopConfig -ClientOnly` from the release script's function definitions only; no release entry point was executed. The helper validated the existing ignored/untracked signing configuration and credential presence without printing values. The existing `MacKeychainPassword` was passed solely over SSH stdin to the script's documented login-keychain unlock sequence, cleared from the remote shell immediately afterward, and was never used for sudo or written to logs/command literals. No signing ACL, identity, account, or password was changed.

A fresh private scratch directory received the same hash-verified archive. Pinned Go 1.26.7 again compiled the guarded fixture binary. Existing Developer ID signing then succeeded, and `codesign --verify --strict --verbose=2` reported both valid-on-disk and designated-requirement satisfaction. Inspection confirmed identifier `com.zfnf.mobile-egress.client`, TeamIdentifier `26VMY3JMQ9`, and the existing Developer ID Application certificate. The designated requirement binds that identifier, Apple's Developer ID certificate chain, and the team.

Signed binary SHA-256: `0435B6547ECE635496F22C9EF3C6823AB7A720DBC66BB7DB70A174C29D95F19D`. Preserved `configured-unlock-sign.log` SHA-256: `0DC606D7C146878E09641977536B8AF3302A27C9C3EA602B29C229D9D66C267B`. The owned scratch was removed and its absence verified. No native fixture tests were executed and no System Keychain fixture items were created.

Final result: compile **PASS**; certificate discovery **PASS**; configured Developer ID signing and designated-requirement verification **PASS**; native CRUD and same-signed upgrade acceptance **ROOTBLOCKED** because existing noninteractive sudo still requires a password. No signed PKG installation, notarization, release publication, or installed-daemon lifecycle test occurred.
