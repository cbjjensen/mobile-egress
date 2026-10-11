# Windows PowerShell signature status correction

## Report and evidence

A second Windows Server 2022 user downloaded Windows 2.0.7 and received the
generic `install_failed` result after publisher confirmation. Their SHA-256
exactly matches the published installer:
`59365093e3c2cf9b448f9f5c776c5c54f11df557ca767d5ece353f6ca7a6011f`.
The reported signer thumbprint is the established
`85F220C1BF05A5D3A86B5DD408787EC1B122ECB7`, and a DigiCert timestamp is present.
Windows PowerShell reports `UnknownError`. Both machine certificate stores
exist; the publisher certificate and Client service were absent after failure.
Absence after rollback does not identify which installation step failed.

The publisher confirmed on Windows PowerShell 5.1.19041.6456, without changing
trust or running an installer, that its native error-to-status mapping returns:

| Native result | PowerShell status |
| --- | --- |
| `0x00000000` | `Valid` |
| `0x800B0109` (`CERT_E_UNTRUSTEDROOT`) | `UnknownError` |
| `0x80096010` (`TRUST_E_BAD_DIGEST`) | `HashMismatch` |

The public [PowerShell implementation](https://github.com/PowerShell/PowerShell/blob/master/src/System.Management.Automation/security/MshSignature.cs#L266-L297)
also defaults unmapped errors to `UnknownError`; its `NotTrusted` mapping is for
explicit distrust. This source is PowerShell Core; the local native probe
separately establishes the Windows PowerShell 5.1 behavior.

The setup parent's held-handle native verification accepts the precise
untrusted-root result. The elevated child's second verification incorrectly
required PowerShell's broad label to be `NotTrusted` for that same native result,
so a fresh untrusted publisher could be rejected before `EnsureTrust` ran.
The reporter subsequently supplied `StatusMessage`: "A certificate chain
processed, but terminated in a root certificate which is not trusted by the
trust provider". This confirms that the reported `UnknownError` is the same
untrusted-root condition reproduced independently. Corrected signed installation
on the affected server remains pending. The earlier corrupted copy/paste attempt
produced no signature evidence.

## Correction and verification

- Accept `UnknownError` alongside the existing `NotTrusted` compatibility case
  only when independent native `WinVerifyTrust` returned `CERT_E_UNTRUSTEDROOT`.
- Keep exact certificate bytes, thumbprint, certificate hash and timestamp
  checks. All other failing native trust results remain rejected. Native success
  still requires `Valid`; post-trust verification still requires `Valid`.
- Do not alter UAC, execution policy, trust-store policy, setup confirmation,
  transaction/rollback behavior or published 2.0.7 bytes.
- The new regression failed against the old source with the precise status
  mismatch, then passed after the one-branch correction. It also exercises
  other native failures, incompatible PowerShell labels, changed certificate
  metadata, missing timestamp and post-trust rejection using an isolated
  in-memory validator identity, without requiring publisher signing files.

This is an unpublished source correction. Signed build, release and physical
Server 2022 acceptance remain separate required steps.

Independent review of the exact production and regression-test diff found no
blocking issues. It confirmed that the exception is restricted to the native
untrusted-root result, with signer identity, timestamp and post-trust checks
preserved. This review does not establish successful installation on the
reporter's server.

## Initial restricted validation

The initial complete setup and setup-command test invocation did not pass. The setup
package recorded 70 passing test events, including the new regression under
normal `go test` vetting, but its certificate-dependent tests could not read the
existing tracked `windows-signing/mobile-egress-code-signing.cer` because this
session returns access denied. Those failed test events are listed below.
The setup-command package then failed to build because the writable drive ran
out of space while compiling/vetting dependencies. No test was skipped or
changed to suppress either limitation. The full JSON log is retained at
`.local/setup-failure-validation/full-setup-tests.jsonl`.

- `TestValidateAuthenticodeResultRequiresExactTimestampedCertificate`
- `TestPreTrustAuthenticodeAcceptsOnlyValidOrExactUntrustedRoot`
- `TestEmbeddedPayloadIsPreparedUnderTransactionBeforeTrustAndCleaned`
- `TestSetupAcceptsPayloadDirectoryWithOnlySetupAtTopLevel`
- `TestEmbeddedSetupNeedsNoAdjacentFilesAndIgnoresAdjacentPayload`
- `TestLoadIdentityRequiresEmbeddedCertificateFingerprintMatch`
- `TestRunParentRejectsBoundSuccessReplacingFailureWhenElevatedChildFails`
- `TestRunParentRequiresZeroExitAndBoundSuccessMatrix/nonzero_valid_failure_surfaced`
- `TestRunParentRequiresZeroExitAndBoundSuccessMatrix/nonzero_forged_success_rejected`
- `TestRunParentRequiresZeroExitAndBoundSuccessMatrix/zero_failure_rejected`
- `TestRunParentRequiresZeroExitAndBoundSuccessMatrix/zero_success_launches`
- `TestRunParentRequiresZeroExitAndBoundSuccessMatrix`
- `TestRunParentRejectsResultForDifferentSetupDigest`
- `TestRunParentRequiresConfirmationAndElevatesOnlyItself`
- `TestRunElevatedSetupTransactionTimeoutOccursBeforeTrustMutation`
- `TestRunElevatedReleasesSetupTransactionAfterSuccessAndRollback/success`
- `TestRunElevatedReleasesSetupTransactionAfterSuccessAndRollback/failure`
- `TestRunElevatedReleasesSetupTransactionAfterSuccessAndRollback`
- `TestRunElevatedUsesFixedSiblingsAndRollsBackOnlyNewTrust`
- `TestRunElevatedRejectsSetupPathSwapBeforeTrust`
- `TestRunElevatedRequiresElevationBeforeConsumingRequest`
- `TestRunElevatedRollsBackExactPartialTrustWhenEnsureTrustFails`
- `TestRunElevatedSurfacesPartialTrustRollbackFailure`
- `TestRunElevatedPropagatesInstallRollbackFailure`

## Publisher validation after access was restored

On 2026-10-11, both complete setup and setup-command suites passed with normal
Go vetting and the original public certificate fixture. The earlier access and
disk failures are historical: the publisher restored normal file access, and
this run used the existing Go cache plus a task-specific temporary directory on
G:. No signing identity, trust policy or test was weakened. Full JSON output is
retained at
`G:\codex-build-cache\inevitable-mobile-relay-server-2.0.8\setup-tests.jsonl`.

The owner's existing Windows-only publication instruction covers this follow-up
installer correction. Prepare a fresh 2.0.8 pilot through the guarded release
workflow, retaining frozen Mac 2.0.6 PKG/DMG and Android 2.0.3 downloads. The
reported Server 2022 installation remains a separate acceptance check; stable
promotion is not authorized.

## Shared gate timing fixture correction

The initial guarded build stopped before creating artifacts: two existing Mac
installer fixture assertions exceeded eight seconds while timing the entire
postinstall, including mocked ownership, signature and daemon setup. Sequential
execution and moving the small fixtures back to C: did not resolve the failure.
Instrumentation at the actual GUI handoff boundary established the cause:

| Fixture | Whole script | Before GUI handoff | GUI handoff |
| --- | ---: | ---: | ---: |
| Hung GUI launch | 8.615 s | 2.964 s | 5.651 s |
| Hung console lookup | 8.010 s | 2.460 s | 5.551 s |
| Hung GUI session lookup | 7.738 s | 2.280 s | 5.458 s |

The test-only correction measures the intended GUI phase, starting immediately
before the unchanged production handoff line. It preserves the eight-second
GUI assertion, the ten-second whole-script command deadline, actual shell
branching and the five-iteration production watchdog. A missing or invalid
timestamp fails the test. No Mac production script, artifact or release scope
is changed. Diagnostic logs are retained under the 2.0.8 publisher work folder.
All three corrected timing checks passed with their normal parallel execution;
the measured GUI phases were 5.567–5.623 seconds.
