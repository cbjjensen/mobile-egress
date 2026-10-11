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
- [ ] Build, sign and publish a new immutable release through existing gates.
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
