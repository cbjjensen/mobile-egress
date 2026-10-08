# Mac user-mode DMG implementation and publication

The owner approved this plan on 2026-10-07: add a Mac app that needs no administrator password, runs proxies only while open, and is publicly available alongside the existing PKG. Implement, validate, publish the next guarded Desktop pilot release and add both website download choices. Preserve historical assets, signing identities, PKG service/storage, mobile behavior and unrelated website source/configuration.

The owner subsequently instructed: **"No just release this for now. Don't wait for anyone to confirm anything"**. This supersedes the earlier standard-user/physical-phone prerequisite for publication. Complete automated/native signing and release checks, then publish without waiting for human confirmation. Record physical installation/cellular/lifecycle acceptance as unverified, not a publication blocker.

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

- [x] 1. User runtime, single-instance protection, firewall policy adapter, shared UI lifecycle copy and regression tests (runtime agent).
- [x] 2. Isolated login-Keychain native store, identity/ACL handling, tests and signed native acceptance (root).
- [x] 3. Mac build/sign/notarize/verify DMG, Desktop release/freeze/download contracts and R2 support with tests (release agent).
- [x] 4. Sibling website contract/config/UI/tests and requirement documentation (website agent).
- [x] 5. Independent code review; full applicable component/native/signed tests; fix and reverify (root/reviewer).
- [x] 6. Record unverified signed standard-user installation, physical pairing/cellular traffic and GUI lifecycle acceptance; the owner waived waiting for these checks before publication.
- [x] 7. Clean-source guarded Desktop prerelease, public R2 publication, narrowly scoped website deployment, live links/hash/health verification and synchronized evidence.
- [x] 8. Owner follow-up: make download URLs follow the verified catalog so normal
  future releases require no website deployment; preserve caching, failure
  fallback, existing product access and iPhone configuration.

## Validation and rollout

Test lifecycle shutdown under active traffic, recovery, duplicate launches, PKG coexistence, port conflicts, separate storage and same-signed upgrades; locked/missing/denied Keychain errors must preserve existing state. Validate Apple Silicon/macOS13 binaries, hardened runtime, actual runtime mode, signed/notarized/stapled app and image, mounted payload identity and exact hashes. Run existing Windows component, native Go, frontend, installer/release and R2 suites plus sibling focused tests and workspace build. Record standard-user install and real phone acceptance separately from automated evidence.

Use the existing coupled Desktop flow after automated review and verification. Android/iOS stay at their existing versions. Preserve pilot status. Website code rollout is limited to the new format choice, based on then-live source; compare complete production configuration, preserve unrelated settings, recheck concurrent deployments immediately before dispatch, then verify both live links. Use existing G: caches/temp paths for publisher disk headroom; preserve historical evidence.

## Execution evidence

Implementation started from mobile main `76b9b99`. The guarded Desktop publisher
built and froze source `46834f6a572a890c259edbf15f8f7f5e51f56942` and published
[pilot v2.0.6](https://github.com/cbjjensen/mobile-egress/releases/tag/v2.0.6)
on 2026-10-07 (2026-10-08 UTC). GitHub reports exactly three uploaded assets with
matching frozen SHA-256 digests, `isDraft=false` and `isPrerelease=true`.

| Artifact | Bytes | SHA-256 |
|---|---:|---|
| `InevitableMobileRelaySetup.exe` | 25,911,072 | `fb8b00bd8462a6a3bee33ef7bec988ede816a7bc11ebae05cd981d9f2a893d52` |
| `inevitable-mobile-relay-macos-2.0.6-arm64.pkg` | 14,679,188 | `7ff743f989e9c46897ffdfca8e9965e518919d6cb0cd8446c5c85092468e35e7` |
| `inevitable-mobile-relay-macos-2.0.6-arm64.dmg` | 10,203,637 | `d47af107a1fa5f38dd4de3b19cb410561e5bdfff9118f6bd922d66e30c8031f1` |

The full Windows component gate passed, including Go tests/vet/build, tagged
installer checks, all release contracts, 19 R2 tests and 88 frontend tests.
Native Mac runtime/locking/port-recovery checks, the runtime/Client race detector,
user-mode GUI vet and native release/CLI tests passed. Signed native Keychain
CRUD, wrong-identifier rejection and two-build same-signed upgrade checks passed
as documented in [Mac installation choices](../../mac-user-dmg.md). Independent
storage/website and final runtime/release reviews reported no serious findings.

Both native Mac artifacts passed their publisher checks. The DMG app and image
were independently signed, notarized and stapled; both passed Gatekeeper. The
read-only mounted app matched source, version, user runtime and executable hash
`846455581251807efec6f2d1e70a33b922764fe7aecfecf620e5b61fb7417180`.
Private verification records remain local. Android remains the exact frozen
2.0.3 APK; iOS distribution is unchanged.

Standard-account install, real-phone cellular proxy traffic and physical GUI
minimize/close/reopen/upgrade acceptance remain **unverified**, under the owner's
explicit instruction to publish without waiting. The release remains a pilot.
The guarded R2 pilot publisher verified every selected public object's exact
bytes, hash, size, content type, attachment filename and immutable cache header
before catalog promotion. Independent HTTPS catalog readback matched all four
downloads and `Cache-Control: no-store`. The PKG metadata remains at
`platforms.macos`; the DMG is its additive `alternatives.dmg` entry.

- [Mac DMG](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/2.0.6/inevitable-mobile-relay-macos-2.0.6-arm64.dmg)
- [Mac PKG](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/2.0.6/inevitable-mobile-relay-macos-2.0.6-arm64.pkg)
- [Windows installer](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/2.0.6/InevitableMobileRelaySetup.exe)
- [Unchanged Android 2.0.3](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/2.0.3/inevitable-mobile-relay-android-2.0.3.apk)

The scoped [website rollout 37715190066](https://github.com/cbjjensen/inevitable-proxies/actions/runs/37715190066)
completed at 02:04:44 UTC on October 8 from source
`6162b14bd2b0f3d859f745d66c6650403869ce1b`. All 240 projected production
settings matched the intended candidate, with only the three Desktop URLs
changed from the previous 239-key baseline. CI, Terraform plan and runtime gates
passed; Terraform apply and both gateway image paths were skipped. Backend/UI/
outbox source and immutable image digests matched the workflow's manifests.
API health/readiness and website health returned 200; PostgreSQL/Redis were ready.

Root refreshed the existing authenticated live `/dashboard/mobile-egress` page
and read all four rendered hrefs: Windows/DMG/PKG 2.0.6 and Android 2.0.3, exactly
matching the verified R2 catalog. DMG appears before PKG with the correct
no-admin/keep-open and admin/background-service labels. No business-state
mutations were performed. Private browser evidence is
`G:/codex-build-cache/inevitable-mobile-relay-dmg/website/live-browser-links-2.0.6.json`.

## Dynamic download follow-up

During the rollout, the owner asked why each URL change requires a website update
and requested a dynamic flow. The sibling backend implementation reads only the
existing fixed schema-1 pilot catalog with strict metadata/path validation,
five-minute successful caching, 30-second failure retry, a two-second/32-KiB read
bound and shared concurrent refreshes. Valid missing formats become unavailable;
failures retain the last valid links, or the configured URLs before the first
successful refresh. iPhone distribution and all auth/billing/eligibility behavior
remain unchanged. The existing browser product response shape is preserved.

The one-time enablement requires a scoped rollout with
`MOBILE_EGRESS_DOWNLOAD_CATALOG_ENABLED=true`. Future guarded R2 catalog
promotions then update resolved downloads on page visits/refetches without
per-version environment edits or website deployment. The final focused resolver/
integration run passed 84 tests; the full backend suite passed 2,619 tests with
264 existing gated skips, and the workspace build passed. Independent review
found no serious issues. The compiled resolver also fetched the actual public
catalog successfully and reused the same snapshot with a 299,999-ms remaining
cache lifetime on an immediate second call.

Reviewed source `ecef2a3ddc39b86adc98e854e39c62df47dd5703` completed
[the one-time enablement rollout](https://github.com/cbjjensen/inevitable-proxies/actions/runs/37716750300)
at 02:22:49 UTC on October 8. All required CI, Terraform plan and runtime gates
passed; Terraform apply and both gateway image paths were skipped. The deployed
configuration matched all 241 intended settings, adding only the enabled catalog
flag to the verified 240-key baseline, with no other differences or missing keys.
Backend/outbox digest
`sha256:723e0fe4cd3c86fad2ece90eea7ab6fe754084f476f3fea30a55f2f54b985e1a`
and UI digest
`sha256:e96870bc1ac0cdc7c37e9849a71bf84d81ef054ba26cc49d5b22965efbf3fca0`
matched the workflow manifests and source provenance. API health/readiness and
website health returned HTTP 200; PostgreSQL and Redis were ready.

A read-only check inside the deployed backend instantiated the actual compiled
resolver against its production configuration. It confirmed the enabled flag,
an initially empty snapshot, one successful real public-catalog fetch, all four
expected links, preserved iPhone configuration and a 300,000-ms cache lifetime.
The next resolve reused the same complete snapshot without a second fetch.
This verifies catalog operation independently of the configured fallback URLs.

Root then refreshed the authenticated live page and confirmed the four rendered
Windows/DMG/PKG 2.0.6 and Android 2.0.3 hrefs, including the correct Mac lifecycle
labels. Private configuration comparison, resolver, image, health and browser
evidence is under
`G:/codex-build-cache/inevitable-mobile-relay-dmg/website-dynamic/`.
Ordinary future version URL changes now require only guarded catalog publication
and live-link verification after cache refresh, with no website deployment.
