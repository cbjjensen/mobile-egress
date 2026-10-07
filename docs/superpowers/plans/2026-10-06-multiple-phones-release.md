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
- Publication: COMPLETE. Guarded Desktop publisher exited 0, ran the Windows component gate (including all 85 UI tests), verified Windows signatures and Mac Developer ID/hardened runtime/notarization/staple/Gatekeeper evidence, froze source `22c9db4b27f8201ef9660df5a1e044587aeb173b` and published [v2.0.5](https://github.com/cbjjensen/mobile-egress/releases/tag/v2.0.5) as a prerelease with exactly two Desktop assets. GitHub uploaded digests match the freeze record. Added concise release notes describing ten-phone support, independent proxies, storage migration and pilot limitations without altering the managed download block.
- R2: COMPLETE. Complete catalog dry-run passed, then guarded `-Publish -Pilot` returned success after public byte/hash/size/header checks. Independent catalog readback matched every version/source/URL/hash/size for Desktop 2.0.5 and unchanged Android 2.0.3. Private logs, plans and preservation manifests are retained under `G:\codex-build-cache\inevitable-mobile-relay-2.0.5`; the Windows build root and Go cache already point to G:, and process-local temporary variables kept new scratch work there.
- Website activation: IN PROGRESS after public artifact verification; actual live verification is required before completion.

## Public artifacts

| Download | Bytes | SHA-256 |
| --- | ---: | --- |
| [Windows installer](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/2.0.5/InevitableMobileRelaySetup.exe) | 25,912,096 | `baa448ed7376bcac829e2fb0b22eed36cd09ff7ff515345e6c4c309fdd1e731a` |
| [Apple Silicon Mac PKG](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/2.0.5/inevitable-mobile-relay-macos-2.0.5-arm64.pkg) | 14,677,573 | `f9b503e6f68bcfb34a19c1004402d4661c896a1a2cb803fadee0e483908f1778` |

Android remains the published 2.0.3 artifact, 11,906,044 bytes, SHA-256 `c5277ffdb122518e6f0b0a38abfbb956b8ee8f3ec9ca0b898faf63ad447f8185`, frozen source `06b299df12c3a143aed6f3c97e488c989aadf599`. No new Android/iPhone build or gateway rollout occurred. Artifact publication does not establish physical installation or cellular acceptance.
