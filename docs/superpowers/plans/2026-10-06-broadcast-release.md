# Broadcast branding publication

The owner authorized deploying all pending changes to GitHub, R2, Inevitable, Apple and Android on 2026-10-06. Use the existing main checkouts and existing signing/distribution identities. Preserve historical artifacts, tester memberships, pairings and concurrent work. This is the existing pilot/prerelease channel, not stable or App Store promotion.

## Ordered checklist

- [x] Inventory pending artwork, website commits, published versions and Apple builds. Highest published desktop/Android version is 2.0.2 (Android code 26); highest Apple build is 7. Select 2.0.3/code 27 and iOS 2.0.3/build 8.
- [x] Commit selected Broadcast artwork, retained design evidence and version metadata. Run deterministic asset and relevant source gates.
- [x] Run guarded Desktop/Android release from clean committed main; verify original signatures, Mac notarization and immutable GitHub assets before publication.
- [x] Mirror exact frozen assets to existing R2 prefix and verify public bytes/headers before promoting the pilot catalog.
- [x] Run native Apple checks, archive/export and validate/upload exact committed iPhone build. Verify signatures, capabilities and version in both bundles.
- [x] Check automatic tester notifications for the exact new Apple build before external assignment/submission. Preserve existing groups/testers and verify internal availability versus external review status separately.
- [ ] Deploy the committed Inevitable artwork with the three verified download URLs through the existing production workflow. Preserve eligibility, billing, gateway behavior and all other production configuration.
- [ ] Record source revisions, build/release/deployment outcomes and remaining physical/Apple review gates in this plan and sibling requirements.

## Acceptance, risk and rollback

Require passing existing release gates and signed artifact verification; never rebuild/clobber a frozen version. Verify R2 downloads against frozen SHA-256 values and the production website against the deployed commit/assets. No new runtime protocol, accounting, infrastructure responsibilities or frontend patterns are introduced by publication. Website asset patterns were documented in its implementation change.

Native/signed builds do not prove physical upgrade, camera, cellular traffic, lifecycle or keep-awake acceptance. Record these as unverified if devices are unavailable. Apple review is an external gate; upload/group assignment alone is not external install availability.

Rollback selects an explicitly verified previous website revision/download catalog without deleting immutable files or changing signing identities. Preserve all release evidence under the existing G: build-cache root. C: disk pressure is being addressed by relocating only the existing Go cache to G: with a junction; no source or credentials are removed.

Commit boundaries: artwork implementation/evidence; release metadata and authorization; website deployment record; final publication evidence.

## Publication evidence

Exact frozen source is `06b299df12c3a143aed6f3c97e488c989aadf599`, following artwork commit `e0799bf` and metadata/plan commit `726d93a`. Main and annotated `v2.0.3` were pushed by the guarded publisher. [GitHub v2.0.3](https://github.com/cbjjensen/mobile-egress/releases/tag/v2.0.3) is a three-asset prerelease. Android is 2.0.3/code 27. All three files below are also public in the existing R2 `mobile-egress/2.0.3/` prefix; the [pilot catalog](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/downloads.json) passed independent readback.

| Artifact | Bytes | SHA-256 |
|---|---:|---|
| `InevitableMobileRelaySetup.exe` | 25,838,880 | `343603b078727e828a3e4a4eec7d09db6cee37c95e2b0877e246719504a89d86` |
| `inevitable-mobile-relay-macos-2.0.3-arm64.pkg` | 14,611,994 | `93b397dcf4dccf21ddb55f8869db7a2a15a31da913976ccc84b7852e4c30021f` |
| `inevitable-mobile-relay-android-2.0.3.apk` | 11,906,044 | `c5277ffdb122518e6f0b0a38abfbb956b8ee8f3ec9ca0b898faf63ad447f8185` |

The original Windows publisher signature/timestamp and Android v3 signer verify. Mac Developer ID app/daemon/package signatures, hardened runtime, notarization, stapling and Gatekeeper pass. Public GitHub digests and R2 conditional writes, response bytes, hashes, sizes, attachment names, content types and cache headers passed before catalog promotion. Historical assets were not replaced.

Validation passed: deterministic brand checks, mobile manifest, release/installer/R2 contracts (16 R2 tests), Go tests/vet/build, 71 desktop frontend tests, Android unit/lint/debug and signed release assembly. Native Apple checks passed 393 Swift tests with two physical/security skips, simulator build, Xcode package tests, and Mac Client app/service/packaging Go packages. The extra native Go check initially selected the system Go; rerunning with the established pinned 1.26.7 toolchain passed. Existing Keychain deprecation warnings remain because the daemon uses the required file-based Keychain implementation.

The known Gradle lint-cache lock resolved through the guarded one-daemon-stop/one-retry path. An early R2 attempt rejected the still-draft GitHub release before any object write; after published-state verification, the identical plan succeeded. No gate was bypassed. The C: Go cache was moved to G: with its original path retained as a junction; source, credentials and historical build evidence were preserved.

### iPhone

Exact source above produced signed **2.0.3 (8)**. IPA is 3,821,720 bytes, SHA-256 `86967e4c447e87e8dd0fa511ad8d2252a6df167128b14ae8405ef6d493583902`. Both app and cleanup extension preserve original signer, bundle IDs, profiles, Keychain/App Group/Network Extension capabilities and privacy manifests; external-capable metadata verifies. Apple validation/upload succeeded, build ID `bbcbad4b-d29a-430c-8b82-57752343729d` is VALID and unexpired.

Verified internal testing in existing ZFNF Friends Internal and Mobile Egress owner pilot. Required exact-build notification checks passed before external assignment and beta submission (`autoNotifyEnabled=true`, no change needed). Existing ZFNF Friends now includes build 8; Apple reports WAITING_FOR_BETA_REVIEW / WAITING_FOR_REVIEW. Contacts, privacy fields, tester memberships and public-link settings were preserved. No App Store production submission or re-invitation occurred. External install availability remains pending Apple approval; actual notification receipt and physical installation are not claimed.

### Inevitable rollout checkpoint

All 238 projected production environment values matched the live host before changes. Only Windows/Mac/Android download URLs advanced from verified 2.0.2 to 2.0.3. Existing Apple release following remains enabled and retains approved build 7 until a compatible newer external release is approved. Website 107 focused tests passed. Source `04a40086af13f3e0e426ecd5a63f155b6356c86a` is pushed; [production workflow 37494987155](https://github.com/cbjjensen/inevitable-proxies/actions/runs/37494987155) is running CI, Terraform plan and runtime deployment, with gateway builds/rollouts and Terraform apply disabled. Final production verification follows.

Private freeze/build/signing/upload evidence is retained under `G:/codex-build-cache/inevitable-mobile-relay-2.0.3/` and existing ignored release output paths. Physical signed Windows/Mac upgrade/repair and Android/iPhone pairing, traffic, camera, lifecycle/keep-awake acceptance remain unverified for this artwork release. No stable promotion is claimed.
