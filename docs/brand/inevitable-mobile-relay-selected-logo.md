# Selected mobile logo: original Broadcast

Selected by the owner on 2026-10-06: **original concept 4, Broadcast**. The mark has a white transmitter with two radio-wave arcs on each side and six colored stones along the bottom. The later refinements and colored-wave versions are not the selected artwork.

## Source and scope

- Preserve the approved image bytes as `assets/branding/inevitable-mobile-relay-logo-source.png`.
- Approved source SHA-256: `CF27A5D1CB8C64A03FF5BFBAF0C1233A3C71098E2BB5A9C17CB8D2BE8CEAD7E2`.
- Update Android launcher/dashboard artwork and iOS AppIcon/dashboard artwork together. Keep the current resource names, app identities and signing unchanged.
- Generate separate monochrome Android notification and themed-icon masks using the same mark. Keep colored foreground artwork within the adaptive-icon safe region.
- The initial mobile selection retained the old ZFNF source and desktop artwork. The owner's desktop follow-up below replaces current Client artwork as well; the legacy ZFNF source/PNG and published installers, APKs and TestFlight builds remain immutable.

## Implementation and validation

- [x] Record the owner selection and preserve the source.
- [x] Extend the existing deterministic asset generator for a separate full-color mobile source.
- [x] Verify color preservation, opaque iOS AppIcon, transparent headers/foregrounds, adaptive safe bounds, and monochrome system masks.
- [x] Regenerate both mobile platforms and inspect the generated artwork at icon/header sizes.
- [x] Update mobile parity evidence and synchronize the sibling branding record.
- [x] Run asset-generation regression/check mode, Android resource validation and mobile manifest validation.

### Results (2026-10-06)

- The canonical source matches the approved SHA-256 above. The color regression first failed against the old monochrome generator, then passed with all six stone colors retained.
- `scripts/test-brand-assets.ps1` passed, covering opaque RGB iOS artwork, transparent foreground/header assets, white-only Android system masks, unchanged desktop artwork, determinism, and missing/stale output detection. Its adaptive-circle assertion caught clipping at scale 0.76; final Android foreground and themed masks use 0.74 and pass. The iOS icon uses the approved source's existing margin without additional padding.
- `scripts/generate-brand-assets.ps1 -Check` passed after final regeneration.
- Android `:app:assembleDebug :app:lintDebug` passed. Existing SDK/compiler/native-symbol warnings did not prevent the build or lint.
- `scripts/validate-mobile-feature-manifest.ps1` and `git diff --check` passed. The approved Android/iOS lifecycle exception is unchanged.
- Inspected the [generated asset preview](inevitable-mobile-relay-selected-logo-preview.png), including iPhone, Android circular/rounded masks, 42-pixel headers and Android system masks. The [preview source](inevitable-mobile-relay-selected-logo-preview.html) uses actual generated assets and is explicitly not a device screenshot.

Native Xcode compilation and physical-device acceptance were not run for this artwork change. The iOS image contract is covered by asset checks; no new signed build, publication or installation is claimed.

This is source artwork adoption. New signed builds and device installation are separate delivery steps; do not claim installed or published apps have changed.

## Desktop follow-up (2026-10-06)

The owner also requested the selected Broadcast artwork on the Windows and Mac Clients. Extend the existing generator to produce a shared transparent Client header, full-color Mac ICNS, and Windows ICO/resources for the Client executable and installer. Keep the old ZFNF source/legacy PNG, mobile output bytes, bundle IDs, signing, service/storage paths and published binaries intact. The Windows resource uses the existing Wails application icon ID (3); no connection or setup behavior changes.

- [x] Generate desktop artwork and reproducible Windows icon resources from the same approved source.
- [x] Add the shared logo to the desktop header without changing navigation or setup behavior.
- [x] Validate assets, Windows resource embedding, frontend behavior and Mac packaging inputs; inspect normal and narrow desktop renders.
- [x] Record outcomes and synchronize the sibling branding ledger.

### Desktop results

- The extended asset regression first failed because the desktop header was absent, then passed after implementation. Full-color PNG/ICO/ICNS and both AMD64 resource objects are generated and included in deterministic check mode. All five selected mobile PNGs were hash-compared before/after desktop generation and remain unchanged.
- Windows Client GUI and installer resource-check builds passed. Parsed their actual PE resources and verified that all nine icon sizes contain the exact generated PNG bytes under Wails icon resource 3. The isolated build tool pins the existing Wails `winres` version and adds no application dependency, privilege manifest or signing mutation.
- All 71 desktop frontend tests passed. Go tests passed for Client app, Mac release/packaging inputs, native GUI entry point and setup with `client_setup` enabled. Historical/branded release contracts and mobile-manifest validation passed. Generator regression and `-Check` passed.
- Rendered the real shared HTML/JS with isolated synthetic bridge state at 940x760 and 620x650 for connected and fresh-setup screens. All four renders had no JavaScript errors or horizontal overflow. Inspected the [dashboard](inevitable-mobile-relay-desktop-dashboard-preview.png) and [narrow setup screen](inevitable-mobile-relay-desktop-setup-preview.png). The logo is decorative beside the accessible product heading and leaves existing navigation/actions unchanged.
- No installed Client, published artifact or signed binary was replaced. Native Mac compilation, Dock verification and signed Windows/Mac installation remain for the next separately prepared build; the Mac ICNS container and packaging inputs passed local checks.

## Inevitable website follow-up (2026-10-06)

The owner also approved Relay-specific website artwork. Inevitable now imports an exact copy of the generated desktop header (SHA-256 `59959FCAF8714F3C8004292F8A940C041563D4D32FB0EEC61C95815008D76395`) through a shared feature-owned logo component. The customer hero, decorative phone, Settings and activation header use it; Inevitable's global branding and access/billing behavior are preserved. The sibling record is `technical-requirement-docs/2026-10-06-mobile-relay-broadcast-logo/implementation-summary.md`: 107 UI tests and the workspace build passed, and 12 local fixture renders covered desktop/phone widths and all three themes. No website deployment or published app replacement is claimed.

## Authorized delivery follow-up

The subsequent deployment request produced signed Windows/Mac/Android 2.0.3 downloads on GitHub/R2 and signed iPhone 2.0.3 (8) on TestFlight. See the [coordinated publication record](../superpowers/plans/2026-10-06-broadcast-release.md) for hashes, verification, website rollout and Apple/physical acceptance status. The source-only statements above describe the earlier implementation checkpoints.
