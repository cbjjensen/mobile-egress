# Windows Server installer elevation repair

## Report and cause

The owner supplied a Windows Server 2022 report: a normal double-click of
Inevitable Mobile Relay Setup failed with "run Inevitable Mobile Relay Setup
normally, not from an elevated process", despite Run as administrator being
unchecked. `RunParent` rejected every elevated token before confirmation.

Windows can give a normal built-in Administrator desktop an unsplit full-privilege
token when Admin Approval Mode is disabled. The screenshot is consistent with
that configuration; the reporter's account and policy have not been inspected.
This is an account/token distinction, not a Server 2022 version check.

References: [Microsoft UAC settings](https://learn.microsoft.com/en-us/windows/security/application-security/application-control/user-account-control/settings-and-configuration)
and [TOKEN_ELEVATION_TYPE](https://learn.microsoft.com/en-us/windows/win32/api/winnt/ne-winnt-token_elevation_type).

## Scoped correction

- [x] Read the actual process token's elevation type, session ID and user SID.
  Allow normal interactive Default/Limited tokens. Reject Full split tokens,
  unknown/unreadable token information, session zero and service identities.
- [x] Repeat the same policy before launching the fixed installed Client path.
  The Client inherits the existing desktop token; no UAC policy or permissions
  change and no token substitution is attempted.
- [x] Preserve exact signer verification, explicit confirmation, held executable
  lock, nonce/digest binding, actual elevated child, exit/result agreement,
  global install transaction, trust rollback and protected owner/state handling.
- [x] Add regression tests and document the unpublished source behavior.
- [x] Run all certificate-dependent installer checks with normal publisher access.
- [x] Build, sign and publish a new immutable Windows release through existing gates.
- [ ] Verify normal double-click installation on the affected Server 2022 desktop.

## Verification on 2026-10-10

Before the fix, the regression reproduced the exact reported rejection for a
normal unfiltered Administrator. After the fix, focused tests passed for normal
and explicitly elevated tokens, RDP/session-zero/service cases, unreadable token,
confirmation decline, wrong result digest, nonzero child exit, failed install
result and unavailable runtime. The setup command suite and Go vet passed.
An unsigned compilation check passed; it is not a distributable installer.
A read-only probe through `WindowsPlatform.CheckParentElevation` successfully
queried this workstation's current token without installation or app launch.
Independent review reported no blocking correctness/security findings.

The complete setup/command test run recorded 76 passing test events, but the
setup suite remains failing because the sandbox denies access to the existing
public `windows-signing/mobile-egress-code-signing.cer` fixture. Affected cases
include signature validation, identity loading, payload preparation, bound
results and trust/installation rollback. The exact per-test results are retained
in `.local/setup-elevation-validation/setup-suite.jsonl`; no test was changed or
skipped to suppress that restriction. No signing files were read or replaced.

The initial session also had read-only Git metadata and restricted network
access, so it left an unpublished working-tree fix. The owner then restored
publisher access and explicitly instructed **"Ok release"**. The established
Windows signing identity validated successfully, and both complete setup and
setup-command suites now pass with their real public certificate fixture.
The earlier sandbox failures above are historical and resolved.

The complete Windows component gate passed at `bed76cb5d7eaeccea52c22abfd034e4b5dd6f2e2`.
While the coupled Desktop build was awaiting Apple notarization, the owner
instructed **"Can we progress with just Windows?"**. The combined publisher was
stopped before freezing, tagging or publishing 2.0.7. The release scope is now
exactly Windows 2.0.7, enforced by a narrow version-specific release policy.
Mac PKG/DMG remain the frozen 2.0.6 downloads, Android remains 2.0.3 and iOS is
unchanged. Normal Windows signing, verification and publication gates remain
required. The dynamic website catalog needs no deployment for this URL change.
Neither this workstation's token probe nor simulated token tests establish
successful installation on the reporter's Server 2022 machine.

## Windows 2.0.7 pilot publication

[GitHub prerelease v2.0.7](https://github.com/cbjjensen/mobile-egress/releases/tag/v2.0.7)
contains exactly `InevitableMobileRelaySetup.exe`, frozen from
`b483f811d6aaf4cf6a88516fcd581ccf9c972f5a` with `components: ["Windows"]`.
Its size is **25,913,632 bytes** and SHA-256 is
`59365093e3c2cf9b448f9f5c776c5c54f11df557ca767d5ece353f6ca7a6011f`.
The timestamped Authenticode signature is valid with the established publisher
thumbprint `85F220C1BF05A5D3A86B5DD408787EC1B122ECB7`.
GitHub reports the single asset uploaded with the same digest, a published
prerelease and the exact frozen tag/source. Release notes retain the Mac 2.0.6
PKG/DMG and Android 2.0.3 links.

The complete Windows component gate passed again at this frozen source: release
orchestration/contracts, parity manifest, all Go tests, Client installer payload
contracts, Go vet/build, 88 frontend tests and syntax, 19 R2 contracts and wrapper
safety. The new scope matrix first failed against the old policy, then passed
with the exact Windows-only exception. Independent review found no blocking
issues and confirmed the signing, provenance, freeze and upload gates remain.

The interrupted Desktop candidate from `bed76cb` is private/unpublished and was
preserved separately before rebuilding Windows at the final source. Private Mac
2.0.7 build output is not part of this tag or the download catalog. No published
2.0.6 artifact was replaced. Logs, GitHub readback and private local publication
evidence are retained under
`G:\codex-build-cache\inevitable-mobile-relay-server-2.0.7`.

The guarded R2 publisher passed offline preparation, verified frozen local and
remote GitHub provenance, and checked actual public bytes/hashes/headers for
Windows 2.0.7, both Mac 2.0.6 formats and Android 2.0.3 before conditional
catalog promotion. Independent public catalog readback at
`2026-10-11T05:45:48Z` matched the complete plan, including `no-store` headers.
The [Windows download](https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev/mobile-egress/2.0.7/InevitableMobileRelaySetup.exe)
is available. The retained platform sources and bytes match their existing
published releases; no immutable asset was replaced.

At `2026-10-11T05:47Z`, after the existing backend cache refreshed, the
authenticated live `/dashboard/mobile-egress` page showed Windows 2.0.7,
Mac DMG 2.0.6, Mac PKG 2.0.6 and Android 2.0.3 at the verified R2 URLs.
No website deployment, environment edit or iPhone distribution change was made.
The reported Server 2022 host still needs its installation acceptance check;
publication was explicitly authorized without waiting for that confirmation.
