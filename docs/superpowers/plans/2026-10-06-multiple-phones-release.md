# Multi-phone Client pilot publication: 2.0.5

The owner explicitly requested committing all work, releasing the Clients and updating Inevitable downloads on 2026-10-06. This authorizes the guarded coupled Windows/Mac Desktop build, existing signing identities, Mac notarization, GitHub prerelease, R2 pilot publication and scoped website download settings update. Android stays on the exact frozen 2.0.3 APK; iPhone/TestFlight is unchanged. Existing physical upgrade/interoperability acceptance remains unverified, and this does not promote stable.

Read the [implementation plan](2026-10-06-multiple-phones.md), [load evidence](../../../../inevitable-proxies/technical-requirement-docs/2026-10-06-mobile-relay-multiple-phones/load-test-report.md) and [scoped download procedure](../../r2-downloads.md#updating-website-links-without-unrelated-deployments). The [sibling publication report](../../../../inevitable-proxies/technical-requirement-docs/2026-10-06-mobile-relay-multiple-phones/release-report.md) records the complete execution ledger and website activation.

## Ordered release plan

1. Confirm clean committed source, remote ancestry and unallocated 2.0.5 version. Check established signing inputs and clean Mac checkout without exposing private data. Preserve existing artifacts. Move this task's ignored validation evidence to G: with hashes and original-path junction if needed for publisher disk headroom; put temporary build work on G: without changing release gates.
2. Commit this release scope, then run `scripts/release-desktop.ps1 -ReleaseVersion 2.0.5 -Publish` from clean main. The guarded entry point runs component checks, builds and signs Windows, builds/signs/notarizes/verifies the Mac PKG, freezes the exact source/artifacts and publishes only verified Desktop assets. Never replace a frozen version after interruption.
3. Prepare and dry-run the complete R2 catalog with Desktop 2.0.5 and Android 2.0.3 from their exact freeze records. Publish through `publish-local-mobile-egress-downloads.ps1 -Publish -Pilot`; verify public bytes, headers and catalog before website settings change.
4. Inspect actual live source, full configuration equality and active deployments. Change only the two Desktop download settings through the existing environment publisher and guarded workflow using explicitly selected live source. Check concurrency immediately before dispatch; do not deploy unrelated current-main changes or roll back a newer independent deployment.
5. Verify actual live source/settings, health/readiness and authenticated website links. Synchronize local protected configuration after checking concurrent edits. Record public hashes, links, verification evidence and remaining physical gates; commit/push final documentation in both repositories. Keep private signing/configuration evidence untracked.

## Component impact

Windows and Mac installers carry the new ten-phone Client functionality. No Android/iOS runtime release, gateway code/config rollout, backend/database migration or accounting change is required. Website activation requires only the two existing download URL settings; the prior website copy/style changes remain committed source and are not included in this narrowly scoped link rollout. No new frontend pattern or infrastructure responsibility is introduced.

## Progress

- Preflight: existing feature work committed on both main checkouts. Mobile main contains origin/main; 2.0.4 is the latest published Desktop prerelease and 2.0.5 has no existing tag. GitHub authentication succeeded. The configured Mac checkout is clean at the previous Desktop source with sufficient disk space. Publisher C: has approximately 200 MiB free, so local evidence/temp relocation is required before building; G: has approximately 29 GiB free. Existing Go cache already uses a G: junction.
- Local preservation: moved 26 task-evidence files (23,871,619 bytes) and 32 files in eight historical hosted-validation output directories to the 2.0.5 G: cache. Verified every file's size/SHA-256 before/after and installed original-path junctions. No historical bytes or evidence were discarded; private manifests retained. Release temporary files use the same cache's `temp` directory through process-local environment settings.
- Publication and live activation: PENDING; update with actual verified outcomes before completion.
