# Task 2 Report: Windows setup and Agent QR experience

## Delivered

- Replaced the desktop Wails pairing surface with `BootstrapOwner`, `RetryClientSetup`, and `IssueAgentQr`.
- `IssueAgentQr` encodes the agent invitation only into an in-memory PNG `data:image/png;base64,...` value and returns that value with an RFC3339 expiry. Its Wails view has no invitation, bundle, or role field.
- Added the pure-Go `github.com/skip2/go-qrcode` dependency.
- Added explicit non-secret `ownerReady` and `clientReady` status fields. Proxy controls and the tray start/stop control now require Client readiness; owner-only state offers client-enrollment retry.
- Reworked the frontend into Setup, Phone, Proxy, and Owner views. Setup accepts only an Owner invitation; Phone presents an expiring QR image and can replace it; raw Agent invitation text and its copy action are absent.
- Replaced frontend error rendering with generic messages and redacted setup and QR Wails errors.

## TDD evidence

### Red

1. Added desktop tests for readiness status and Agent QR output, then ran `go test ./windows-client/internal/desktop`. It failed to build because `Status.OwnerReady`, `Status.ClientReady`, and `DesktopApp.IssueAgentQr` did not exist.
2. Added the malformed-owner-invitation redaction test and temporarily returned the decode error. The test failed with `pairing bundle is not valid JSON`, proving it catches a raw setup error.
3. Added the QR Wails-view shape test and temporarily added a `Bundle` field. The test failed because the view exposed three fields rather than only image data and expiry.

### Green

- Restored the redacted setup path and two-field QR view; `go test ./windows-client/internal/desktop` passed.
- Ran `go test ./...` successfully after the final Go changes.
- Ran `npm run check` and `npm run build` successfully after the final frontend changes.

## Files changed

- `go.mod`, `go.sum`
- `windows-client/internal/client/core.go`
- `windows-client/internal/client/state.go`
- `windows-client/internal/desktop/run_windows.go`
- `windows-client/internal/desktop/run_windows_test.go`
- `windows-client/frontend/src/api.ts`
- `windows-client/frontend/src/App.tsx`
- `windows-client/frontend/src/styles.css`

## Self-review

- Agent invitations are not returned through the Wails API or rendered/copied by the frontend.
- QR PNGs are created in memory and returned solely as data URLs; no image file is written.
- Owner invitation entry remains only on the Setup screen and invokes `BootstrapOwner`.
- Client readiness, not the selected/legacy role, gates proxy and tray actions.
- Status contains readiness booleans and a redacted proxy address only when the Client identity exists; it includes no identity material.
- The change is limited to Task 2 Windows client work, its minimal QR dependency, and this required report; no Android or design/plan document changed.

## Concerns

- A physical-device smoke test of Android scan, cellular egress, and no Wi-Fi fallback remains required outside this Windows-only task.
- The QR payload is validly encoded as a PNG data URL in automated tests; end-to-end scanning depends on the Android device test above.
