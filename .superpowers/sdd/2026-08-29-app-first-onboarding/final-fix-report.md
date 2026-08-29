# App-first onboarding final fix report

## Changes

- Android scanner results now enter UI state only through the main executor. Cancelling an accepted-but-not-started handoff completes the pairing state transition, clears the transient QR value, and cannot leave pairing busy.
- Windows Owner and Client state now requires the same normalized HTTPS relay origin and exact pinned CA on save and load. A migrated Client rejects mismatched Owner trust before enrollment consumes the invitation; returned identities are checked again before persistence.
- The Owner UI now shows the safe local Client certificate serial and implements ordered recovery: record serial, revoke, **Replace Client**, restart and verify. Replacement uses Owner control to enroll a fresh Client in memory, atomically replaces only the protected Client state, detaches the old proxy session, preserves Owner state, and never exposes invitation text. Failed revocation retains the form value; desktop errors are generic.
- README, Windows, operations, deployment/checklist, and onboarding design documentation now describe the implemented recovery flow.

## RED to GREEN evidence

- Android: `gradlew.bat testDebugUnitTest --tests com.mobileegress.agent.ui.ScanPairingCoordinatorTest`
  - RED: `3 tests completed, 1 failed`; `cancelling an accepted scan cannot strand pairing in progress` failed at line 108.
  - GREEN: `BUILD SUCCESSFUL`; all focused coordinator tests passed.
- Windows relay trust: focused `go test ./windows-client/internal/client -run ... -count=1`
  - RED: mismatched save/load and both migrated-Client Owner mismatch cases failed because inconsistent trust was accepted.
  - GREEN: `ok mobile-egress/windows-client/internal/client` for normalized same-relay success and mismatch rejection tests; the full client package also passed.
- Windows recovery:
  - RED: three core replacement tests failed with `Core does not expose ReplaceClient`; desktop tests failed on the missing method and leaked `sensitive relay detail` from revocation.
  - GREEN: client and desktop focused suites passed, proving new-Client SOCKS use with Owner preservation, old-Client preservation after enrollment/persistence failure, safe post-commit cleanup behavior, and generic desktop errors.

## Final verification

- `scripts/test-all.ps1` with JDK 17 and Android SDK 35: passed Go test/vet/build, frontend typecheck/build, Compose config validation, Android unit tests, Android lint, and debug assembly; final output: `All integration checks passed.`
- Debug APK inspected: `com.mobileegress.agent.debug`, version `1.0.0-debug`, 42,078,381 bytes, SHA-256 `8D5D27F7E5D027ECDB5BC7FEF471B9FA4FC424A7BA78F76804682040EED09DD5`.
- Frontend has no test runner; recovery UI was validated by TypeScript check/production build plus Go core/desktop boundary tests.

## Commits

- `2ce3375` — `fix: harden app-first onboarding recovery`
- `docs: record final onboarding fix verification` — this report commit

## Concern

- The documented physical-device revocation/replacement and cellular fail-closed checklist still requires the owner's real relay, Windows installation, and Android phone; it was not executable in this local automated environment.
