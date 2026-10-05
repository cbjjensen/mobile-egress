# Make the desktop Client read like a consumer product

## Intent and scope

The owner says the Client reads too technically. Rewrite the shared Windows/Mac frontend around connecting an account, connecting a phone, starting sharing and configuring an app. Keep the established Dashboard / Phone settings / Review setup navigation. No service, transport, authentication, mobile runtime or website changes. Preserve existing pairing, drafts and protected operations.

## Implementation

- [x] Replace everyday infrastructure terms with clear actions and short explanations; use Show QR code and show the exact native phone Start actions.
- [x] Present approved account state as already completed, without asking the owner to activate again. Keep the existing continue behavior.
- [x] Present normal connection states in plain language; keep diagnostic messages available under Connection details. Computer/account readiness must never imply the phone is sharing. Preserve actionable errors without guessing network causes.
- [x] Keep connection diagnostics and direct mode behind explicit details/Advanced choices. Preserve router, provider firewall, privacy, port and iPhone lifecycle requirements where they apply.
- [x] Explain proxy use in terms of the user's app, keep HTTP/SOCKS choices and formats accurate, and state that only configured apps use the phone's data.
- [x] Keep recovery/removal instructions precise: updates repair saved phone connections, removal requires a new QR, and expired or pending setup never claims completion.
- [x] Validate state truthfulness and existing navigation, inspect connected/setup/recovery/error screens at normal and narrow sizes, run the Windows gate and independent review.
- [x] Update onboarding/operations and sibling pilot evidence; prepare a signed immutable local build. Do not automatically install it, deploy, merge or publish.

## Acceptance and rollback

Routine screens should explain the next action without requiring knowledge of gateways, services, sessions, authentication or enrollment. Necessary protocol choices and actionable failure details stay accessible. No success from socket binding, account approval, pairing alone or an unacknowledged update. Android background operation and iPhone active-app operation remain distinct, with exact current Start button labels. Frontend rollback requires no state migration. Native Mac/phone acceptance remains separate from synthetic renderer evidence.

## Validation

Nine new presentation/state regressions failed before their respective fixes. Final 71 frontend tests and JavaScript syntax pass, including an independent rerun. Existing label expectations were updated while retaining navigation, pairing, expiry, acknowledgement, credential-copy and service-recovery assertions. Review caught normal hosted-progress copy overriding a native connection error; errors now take priority and cannot display Connected. Completed browser-approval instructions also clear without clearing unrelated feedback.

The full Windows gate passed Go tests/vet/build, installer/release contracts, mobile manifest/schema validation and the 71 frontend tests. Independent review has no remaining actionable findings. Sixteen synthetic actual-asset screenshots cover connected dashboard, fresh hosted setup, approved account review, phone connection, Start sharing, pending recovery, native error and Phone settings at 940×760 and 620×650. No renderer errors or horizontal overflow; navigation remains stable. Diagnostics/advanced choices stay collapsed normally, while a new error opens its exact native repair instructions once. Redundant copy identified during visual review was removed. Synthetic rendering used no live credentials or service operations and left no capture processes running.

Current onboarding/operations and sibling pilot records are synchronized. Native phone behavior, manifest evidence and existing API/protocol/storage contracts are unchanged. Final wording-only dashboard polish passed all 71 tests and both viewport captures again.

Source `02e0ba302e0c9355d7f2e4739421c608758cfcdd` produced signed Windows validation `2.0.0-hosted-validation.20261004.7`. Established-signer/timestamp, source/version/platform and embedded-payload verification passed. Installer SHA-256: `0192042EEE215FFC81225AD1028ADB2B3D17B544306588DF32768D498F077963`. The artifact is prepared, not installed. This change has no new native Mac or physical-device acceptance and does not interrupt the owner's working Client. No production deployment, main merge or public release occurred.
