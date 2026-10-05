# Mac signed native-storage validation, 2026-10-04

The initial local build-server System Keychain acceptance was blocked by noninteractive root permissions. Later owner-authorized testing on a separate rented Mac passed native signed storage, fresh installation and repair after fixing GUI-owner detection, as recorded under **Remote desktop installer repair and native acceptance** below.

The current hosted Client PKG was subsequently built, signed, notarized and verified successfully as recorded under **Current hosted Client installer** below. Installed service/storage and physical-device acceptance remain separate.

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

## Current hosted Client installer

The owner requested the current Mac installer on October 4 (local time). Package verification finished October 5, 2026 UTC. The [preparation plan](superpowers/plans/2026-10-04-mac-client-installer.md) was saved and committed before building. Source `75f1120ff0684f7ff93138c5e056ca1cc4a319b6` includes current desktop wording/navigation and footer removal. No product implementation change was required.

Artifact: `mobile-egress-client-macos-2.0.0-hosted-validation.20261004.8-arm64.pkg`, 13,766,003 bytes.

- Windows: `C:/Users/Chad/workspace/mobile-egress/windows-client/build/release/mobile-egress-client-macos-2.0.0-hosted-validation.20261004.8-arm64.pkg`.
- Mac owner-accessible copy: `/Users/Shared/mobile-egress-client-macos-2.0.0-hosted-validation.20261004.8-arm64.pkg`.
- Package SHA-256: `f71aefa63b910bfad8db3db1de872315d5cd119df1749856f303cfa5d2f1b9c8` on the build host, downloaded copy and Shared copy.
- Private source/version/signing verification JSON remains adjacent to the Windows package; it is not a public release asset.

Fresh read-only prerequisites confirmed macOS 26.2/arm64, a clean build checkout, configured notary input and pinned Go 1.26.7. Exact source bundle SHA-256 `cb27797fc9ccfff9ec4485d228c5a0df5e0a1ae805c0cd33cd9fcb1e284cb3dd` matched before checkout. The existing `release-client-macos.sh` local packaging entry point and desktop SSH/configuration/record-verifier helpers preserved the established signing/notary handling. No general Desktop publication/tag operation ran.

Passed:

- Native pinned Go full uncached tests, vet and race tests; native pinned Node syntax and all 71 frontend tests.
- Publisher workstation release-all/desktop/direct contract suites, Mac verification-record/CLI tests, Client installer contracts, 71 frontend tests/syntax, mobile manifest validation and validator regression suite.
- Developer ID Application app/daemon signatures, exact identifiers/team, hardened runtime and existing Developer ID Installer PKG signature/timestamp.
- Apple notarization **Accepted**, stapling and staple validation; Gatekeeper accepted both the app and package as Notarized Developer ID.
- Download/source/version/hash/signing verification using the existing Go Client-record validator. Repeated independent package signature, ticket and Gatekeeper checks passed.
- Extracted payload signatures, both executable version checks, arm64 and minimum macOS 13, embedded exact VCS revision and `vcs.modified=false`. Packaged preinstall/postinstall scripts match source byte-for-byte, are executable and parse; app and LaunchDaemon plists validate.

Extracted daemon SHA-256: `c0a18e5cd0d356ac342192b7ca444f38ed9d39fdefe4c0cbac2825cc2f0c5958`. Extracted GUI executable SHA-256: `36e81866ca484806fbe80e431531872316c84391feb068a144285c75612df2f2`. Temporary inspection files were removed; immutable packages and private verification evidence were retained.

Evidence directory: `G:/codex-build-cache/mobile-egress-hosted-20261004/mac-client-installer-8/`.

- `native-tests.log`: `C2197F28249B05E9642D41DAE3DBF9C62FEC20580052C642F53A893B6B0AD740`.
- `package-build.log`: `8BD51BE0FF935C2EDB846F9DD4D1A04AFA2BF4069ABC3DBC159CDE8C3F0DDE8F`.
- `payload-inspection.log`: `3DB2FC51987464D7DD5F51326448F054D4FE767CF27ACB6F33CCC4D9CE2763E9`.

Remaining: a fresh `sudo -n true` still requires authentication, and the active GUI user differs from the SSH/build user. No administrator credential was requested, inferred, or reused; no ownership/privilege change or installation was attempted. Root System Keychain CRUD/same-signed upgrade, fresh install/upgrade/repair, owner GUI handoff, daemon boot/logout and physical Android/iPhone traffic are **NOT RUN**. Install while logged into the intended Mac owner account; normal macOS Installer administrator authorization is required. The signed/notarized package is ready for owner installation/testing, not stable production promotion. No GitHub release, tag, main merge, Inevitable deployment, or existing installation change occurred.

## Remote desktop installer repair and native acceptance

The owner's screenshots and Installer Log revealed both 1.1.7 and `.8` failed during preinstall on their separate EC2 Mac. Existing verified SSH access confirmed macOS 27.0/arm64, a real UID-501 GUI login and `gui/501`, but a root-owned `/dev/console`. System Configuration correctly reported active UID 501. No Client state, app, daemon or receipt existed after the failures. The transferred `.8` hash and Gatekeeper checks passed, ruling out a damaged/unsigned transfer as this failure's cause.

The [repair plan](superpowers/plans/2026-10-04-mac-installer-session-owner.md) records regression-first correction of both preinstall owner selection and postinstall handoff. Exact final source `c36c758116c73677c7530861964c146b7358e1b1` includes fix `fc8097a` and a test-only fixture-concurrency correction. An initial native race run timed out in two shell fixtures; limiting concurrent fixture processes fixed test-host contention while preserving all production/test timeout assertions. Final native full tests/vet/race, 71 frontend cases/syntax, Windows installer/record checks, release contracts, manifest/schema checks and independent review passed.

Current artifact: `mobile-egress-client-macos-2.0.0-hosted-validation.20261004.9-arm64.pkg`, 13,766,321 bytes; SHA-256 `09e60569c1bf488d9062b95a1cd7ed7d43c326d19d6e9df885e61e06487422d1`. Existing identities/hardened runtime, signing/timestamp, Apple notarization Accepted, stapling, Gatekeeper, minimum macOS 13/arm64 and source/version/hash-bound record verification passed. Downloaded and rental-transferred bytes match. Copies are in the Windows `windows-client/build/release` directory, the build Mac's `/Users/Shared`, and the rental's `/Users/ec2-user/Transfers`. Historical `.8` remains unchanged.

Fresh installation through normal `installer -pkg ... -target /` succeeded on the owner's rental. It saved UID 501, started the root LaunchDaemon and launched the GUI as UID 501. Owner IPC confirmed exact `.9` version, stable identity and initial hosted account-setup state; no account activation, pairing or connection was falsely claimed. State directory/owner file are root-owned 0700/0600, runtime directory is 0755, and protected IPC rejects a tested nonowner UID while the owner's status call succeeds. Installed executable SHA-256 values:

- Daemon: `565b0d0cd7491233112d861d60865c6e775e5e9197a17b00e946ad39d22ab78e`.
- GUI: `a8431e1bfe259471067976602d9df237904a4eb61d4d71f74d664c0546912fb1`.

Same-package repair also succeeded through the ordinary installer. The daemon restarted and owner, stable Client ID and Mac local proxy endpoints survived. This was an unpaired fresh Client; it does not establish a different-version package upgrade or retention of a live paired session.

Native signed root storage acceptance now **PASS** on the rental: `TestSignedRootSystemKeychainCRUD`, then phases A and B of `TestSystemKeychainSameSignedUpgrade` across separately built test binaries signed with the existing Developer ID Application and `com.zfnf.mobile-egress.client` identifier. Fixture source was `fc8097a`, whose secure-store implementation is identical to final package source; B stripped debug information to ensure a different binary. Both designated requirements verified. Binary hashes: A `58da85fbfac9d8d85bed9974752a7774f220ebfef119990c47ab01cfe98e3868`, B `afdabdbe342fad10cec1a8b12c7051095b2f104f8a550a6ddc5b7af1f84e45bc`. Random test items and upgrade phase state were deleted by the tests; private fixture directories on both Macs and temporary installation-status files were removed. No production Keychain item was exported or deleted.

Private evidence is retained under `G:/codex-build-cache/mobile-egress-hosted-20261004/mac-client-installer-9/`: `native-tests.log`, `package-build.log`, `package-inspection.log`, `storage-build.log`, `storage-acceptance.log`, `fresh-install.log`, `repair-install.log`, `installed-identity.log`, exact source bundles and verified package record. Source bundle for final `c36c758` matched SHA-256 `4bc9dd971a91b5aa22f5231ab8734e4a1965749d4798c42dd5370ec45f82b441` before checkout.

Still **NOT RUN**: physical Mac-to-phone HTTP/CONNECT/SOCKS, reboot/logout, different-version installed upgrade and paired-state retention. The app is installed and ready for the owner's account/phone setup; stable release readiness is not claimed. No host allocation, rental expiry, security group, router, DCV, Inevitable deployment, unrelated app or existing Windows/phone pairing changed. No public release/tag or main merge occurred.
