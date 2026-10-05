# Separate the Client dashboard from setup

## Request and current behavior

The owner confirmed that the Windows desktop Client dashboard feels partly like setup. Its render function currently displays activation/address configuration, mode selection, pairing, connection updates and proxies together after pairing. This is a presentation/navigation correction to the shared Windows/Mac Client frontend, not a service, protocol, mobile runtime or Inevitable website change.

## Implementation and acceptance

- [x] Keep the everyday dashboard focused on connection status and proxy copies. Hide setup forms, invitations and inactive recovery controls.
- [x] Show the specific action required for unfinished setup, lost hosted authorization, a pending phone connection update or a disconnected phone. Preserve authorization-before-update priority and acknowledgement-before-Connected semantics.
- [x] Put manual connection updates and confirmed phone removal in an explicit Phone settings view with a return action. Keep Review setup as an explicit entry into the existing wizard. Removal leads to fresh pairing without automatically issuing an invitation.
- [x] Preserve paired-offline startup, Finish later, saved invitations, pending acknowledgements, manual edits, direct-mode configuration, service retry/repair and protected operations. Dashboard state reflects saved service configuration, never an abandoned mode selection.
- [x] Add state/navigation regressions, run frontend and Windows gates, visually inspect synthetic connected/offline/recovery/settings states, and obtain an independent review.
- [x] Synchronize operations/onboarding and the sibling pilot record; prepare a signed local validation installer without automatically interrupting the owner's working connection.

## Interfaces and limits

Use existing protected local operations only. No new binding, backend request, persistence field, pairing replacement, usage collection, infrastructure change, deployment, merge or public release. Keep the native Android/iOS Start labels and approved lifecycle distinction. Mobile source/evidence and signing identities remain unchanged. The existing installer preserves settings on upgrade; reverting the frontend restores the previous presentation without a state migration.

## Validation

New navigation/state tests failed against the original dashboard and passed after implementation. The full Windows gate passed Go tests, vet/build, installer/release contract checks, mobile manifest/schema validation, frontend syntax and the initial 57 frontend tests. Independent review identified contextual QR feedback remaining visible after leaving the QR view. An additional regression failed before that correction; the final 58 frontend tests and syntax check pass, including an independent rerun. No remaining actionable review findings.

Synthetic actual-asset renderer checks use installed Edge at 940×760 and 620×650 for connected, offline, pending-update and Phone settings states. All eight screens have no renderer errors or horizontal overflow; the connected dashboard fits the native window, and smaller/recovery screens scroll normally. The ignored harness contains no real service credentials and leaves no capture process running. This is browser-renderer evidence, not a native installation or new phone-traffic acceptance test.

Onboarding/operations and the sibling Inevitable pilot record are synchronized. Native mobile source and manifest evidence are unchanged. Final screenshots were recaptured after the heading-focus polish and contextual-feedback fix. The established Windows signer validates.

Committed source `3684ab003862afe58283c22e4005bc5157457a48` produced signed Windows validation build `2.0.0-hosted-validation.20261004.5`. Full guarded verification passed established-signer/timestamp, version/platform/source and embedded-payload checks. Installer SHA-256: `03FE0254D9B43AB34C6A2547690206B5D8A98EC2DD3434F497B40D9EE4038BF9`. The artifact is prepared, not installed over the owner's working Client. Native installation of this UI, native Mac validation and new physical-device acceptance remain unperformed. No deployment, main merge or public release occurred.

## Follow-up: consistent review navigation

The owner found that Review setup still said Finish later and moved/restyled the navigation compared with the dashboard. One stable, visibly selected page navigation row now serves Dashboard, Phone settings and Review setup after entering the dashboard. A fresh setup retains Finish later; explicitly revisiting setup uses Back to dashboard even when the phone is offline or the service temporarily unavailable. Navigation does not mutate configuration, reset the current review step on a repeated active-page click, lose edited inputs or overlap phone settings with the wizard. Fresh-pairing/recovery semantics and protected-action busy states are preserved.

All four new navigation regressions failed before the fix. The final 62 frontend tests and syntax check pass, including an independent review/rerun with no actionable findings. The full Windows Go/vet/build, installer/release contracts and manifest/schema gate passed. Eight synthetic renderer checks cover connected dashboard, Phone settings, Review setup and fresh setup at 940×760 and 620×650. Visual review caught and corrected a scrollbar-induced horizontal shift with a stable gutter; final page/button coordinates match across the three navigation views at both sizes. No renderer errors, horizontal overflow or capture processes remain.

Source `affd5a61d21254eb86882d43ae64c57c67443d02` produced immutable signed validation `2.0.0-hosted-validation.20261004.6`; full signer/timestamp, source/version/platform and embedded-payload verification passed. Installer SHA-256: `32E3FF8668E392E71A2568D10A4EDB714C0C9E6A0487B8406A7B2F0E7706502A`. Prepared, not installed; no service/pairing/production changes were made. Native installation/physical acceptance remain separate.
