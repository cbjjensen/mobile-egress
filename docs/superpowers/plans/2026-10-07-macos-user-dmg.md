# Mac user-mode DMG implementation and publication

The owner approved this plan on 2026-10-07: add a Mac app that needs no administrator password, runs proxies only while open, and is publicly available alongside the existing PKG. Implement, validate, publish the next guarded Desktop pilot release and add both website download choices. Preserve historical assets, signing identities, PKG service/storage, mobile behavior and unrelated website source/configuration.

## Design and interfaces

- Compile an explicit `client_usermode` Mac GUI variant using the existing in-process `nodeservice.Direct` runtime. The default GUI continues using installed service IPC. Never infer user mode from unavailable service IPC.
- User mode closes traffic on window close/Quit/sign-out, keeps traffic while minimized, and owns bounded recovery/cancellation. No LaunchAgent, helper service, auto-start or privilege escalation.
- `securestore.NewUserKeychainStore() (Store, error)` opens only the current user's login Keychain, validates the established Developer ID GUI identifier/team and creates signature-bound item ACLs under `com.zfnf.mobile-egress.client.app.user`. Preserve hashed logical accounts, non-destructive updates, fail-closed errors and no plaintext fallback. Existing system/controller stores remain untouched.
- Use a protected per-user single-instance lock under `~/Library/Application Support/MobileEgressClient`. Refuse conflicting active installed service; report occupied proxy ports without altering another process or silently changing addresses. DMG has separate activation/identity/pairing; same-signed DMG upgrades retain state.
- Keep hosted outbound default, loopback-only HTTP/SOCKS, and existing Advanced direct transport. User mode does no privileged firewall mutation; hosted reports no rule needed, direct provides actionable manual-policy status. Shared UI displays the user-mode lifecycle accurately.
- DMG app keeps `com.zfnf.mobile-egress.client.app` and current executable/product names. Signed Info.plist records user mode; artifact verification checks actual executable runtime mode as well as source/version. DMG contains the app and instructions to copy into personal `~/Applications`, not a hardcoded `/Applications` shortcut.
- Expected first DMG version is 2.0.6, subject to an unused-version check. New Desktop contracts require Windows EXE, existing PKG and `inevitable-mobile-relay-macos-<version>-arm64.dmg`. Historical contracts remain unchanged. DMG private evidence uses `.dmg.verification.json` and binds source/version/hash/arm64/macOS13/app identity/runtime/signature/notary/staple/Gatekeeper checks.
- Preserve R2 catalog schema 1 and PKG `platforms.macos` metadata; add optional `platforms.macos.alternatives.dmg` with full version/source/url/hash/size metadata. A selected Mac platform expands to both artifacts for new versions; all selected bytes are validated/frozen/uploaded/checked before catalog promotion.
- Website preserves `downloads.macos` / `MOBILE_EGRESS_MACOS_DOWNLOAD_URL` for PKG. Add optional `downloads.macosDmg` / `MOBILE_EGRESS_MACOS_DMG_DOWNLOAD_URL`. Show DMG first with no-admin/keep-open wording and PKG with admin/background-service wording. Test independent availability without nested links.

## Tasks and ownership

- [ ] 1. User runtime, single-instance protection, firewall policy adapter, shared UI lifecycle copy and regression tests (runtime agent).
- [ ] 2. Isolated login-Keychain native store, identity/ACL handling, tests and signed native acceptance (root).
- [ ] 3. Mac build/sign/notarize/verify DMG, Desktop release/freeze/download contracts and R2 support with tests (release agent).
- [ ] 4. Sibling website contract/config/UI/tests and requirement documentation (website agent).
- [ ] 5. Independent code review; full applicable component/native/signed tests; fix and reverify (root/reviewer).
- [ ] 6. Signed standard-user installation, pairing/cellular HTTP/CONNECT/SOCKS and lifecycle acceptance. Missing access is an explicit publication gate.
- [ ] 7. Clean-source guarded Desktop prerelease, public R2 publication, narrowly scoped website deployment, live links/hash/health verification and synchronized evidence.

## Validation and rollout

Test lifecycle shutdown under active traffic, recovery, duplicate launches, PKG coexistence, port conflicts, separate storage and same-signed upgrades; locked/missing/denied Keychain errors must preserve existing state. Validate Apple Silicon/macOS13 binaries, hardened runtime, actual runtime mode, signed/notarized/stapled app and image, mounted payload identity and exact hashes. Run existing Windows component, native Go, frontend, installer/release and R2 suites plus sibling focused tests and workspace build. Record standard-user install and real phone acceptance separately from automated evidence.

Use the existing coupled Desktop flow after review and required acceptance. Android/iOS stay at their existing versions. Preserve pilot status. Website code rollout is limited to the new format choice, based on then-live source; compare complete production configuration, preserve unrelated settings, recheck concurrent deployments immediately before dispatch, then verify both live links. Use existing G: caches/temp paths for publisher disk headroom; preserve historical evidence.

## Execution evidence

Implementation started from mobile main `76b9b99` with a clean tracked checkout. Publication and physical acceptance are pending; this document is not a claim that an installer has been built or released.
