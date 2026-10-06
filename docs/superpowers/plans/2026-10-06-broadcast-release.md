# Broadcast branding publication

The owner authorized deploying all pending changes to GitHub, R2, Inevitable, Apple and Android on 2026-10-06. Use the existing main checkouts and existing signing/distribution identities. Preserve historical artifacts, tester memberships, pairings and concurrent work. This is the existing pilot/prerelease channel, not stable or App Store promotion.

## Ordered checklist

- [x] Inventory pending artwork, website commits, published versions and Apple builds. Highest published desktop/Android version is 2.0.2 (Android code 26); highest Apple build is 7. Select 2.0.3/code 27 and iOS 2.0.3/build 8.
- [ ] Commit selected Broadcast artwork, retained design evidence and version metadata. Run deterministic asset and relevant source gates.
- [ ] Run guarded Desktop/Android release from clean committed main; verify original signatures, Mac notarization and immutable GitHub assets before publication.
- [ ] Mirror exact frozen assets to existing R2 prefix and verify public bytes/headers before promoting the pilot catalog.
- [ ] Run native Apple checks, archive/export and validate/upload exact committed iPhone build. Verify signatures, capabilities and version in both bundles.
- [ ] Check automatic tester notifications for the exact new Apple build before external assignment/submission. Preserve existing groups/testers and verify internal availability versus external review status separately.
- [ ] Deploy the committed Inevitable artwork with the three verified download URLs through the existing production workflow. Preserve eligibility, billing, gateway behavior and all other production configuration.
- [ ] Record source revisions, build/release/deployment outcomes and remaining physical/Apple review gates in this plan and sibling requirements.

## Acceptance, risk and rollback

Require passing existing release gates and signed artifact verification; never rebuild/clobber a frozen version. Verify R2 downloads against frozen SHA-256 values and the production website against the deployed commit/assets. No new runtime protocol, accounting, infrastructure responsibilities or frontend patterns are introduced by publication. Website asset patterns were documented in its implementation change.

Native/signed builds do not prove physical upgrade, camera, cellular traffic, lifecycle or keep-awake acceptance. Record these as unverified if devices are unavailable. Apple review is an external gate; upload/group assignment alone is not external install availability.

Rollback selects an explicitly verified previous website revision/download catalog without deleting immutable files or changing signing identities. Preserve all release evidence under the existing G: build-cache root. C: disk pressure is being addressed by relocating only the existing Go cache to G: with a junction; no source or credentials are removed.

Commit boundaries: artwork implementation/evidence; release metadata and authorization; website deployment record; final publication evidence.
