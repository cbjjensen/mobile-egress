# Desktop setup simplification implementation plan

**Goal:** Implement all six findings approved in the setup review directly on main.

**Design:** Retain native installation and permission boundaries. A user-started frontend workflow sequences existing backend operations, waits for observed macOS approval, and stops on cancellation or failure. Add display-safe status for saved AWS credentials and the connected Agent. Deliver Windows setup as a signed executable containing its verified payload, retaining legacy installation compatibility.

**Constraints:** Personal Windows/Mac relay and Tailscale Funnel remain unchanged. Preserve established signing identity, transactional rollback, Keychain/DPAPI storage, and mobile interoperability. No release publication or tagging in this task.

- [x] Windows installer: embed signed payload, safely stage and verify it, update build/release contracts, correct optional fingerprint copy, and display native setup/runtime progress. Test archive validation, compatibility and trust/rollback boundaries.
- [x] Backend onboarding observations: expose saved AWS connection availability and observed Agent connection; report Tailscale download/verification/installer stages without leaking private values. Test freshness and stage ordering.
- [x] Guided bridge workflow: sequence install/connect/setup, resume only a user-started workflow after verified approval, stop on errors/cancel, and never repeat permission prompts automatically. Test platform branches, cancellation and retries.
- [x] Shared UI: actionable next step, explicit phone pairing/sharing, restored AWS validation, user-confirmed application connectivity completion, simpler permission copy and expandable diagnostics. Test guide ordering and build TypeScript/React.
- [x] Documentation and validation: update both platform quick starts and acceptance guidance, run focused Go/frontend suites then the applicable Windows gate, and review changes for commit on main.

Use the existing Go and Node toolchains resolved by scripts/test-all.ps1. Native macOS and signed installer acceptance must be reported separately from portable checks.

## Verification

`scripts/test-all.ps1 -Components Windows` passed: complete Go tests/vet/build, 53 frontend tests, TypeScript and Vite production build, release contracts, and mobile-feature manifest validation. Headless UI checks with simulated backend responses exercised saved AWS restoration, macOS approval/resume, pairing versus sharing, application confirmation, and invalid credential handling. Native Windows progress smoke test displayed and read back stage text.

Review corrected stale-health repair gating, repair cancellation/progress, and premature AWS readiness. Existing published ZIP contracts through 1.1.6 remain unchanged. No signing, release publication, or native macOS acceptance was performed. Automatic approval review rejected an additional unsigned embedding build with “blocked by policy”; archive extraction and embedded-byte binding regression checks passed independently.
