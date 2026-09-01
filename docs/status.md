# Current status

## Implemented

- Loopback-only Windows relay service, direct CSR Owner bootstrap, Owner-authorized Client CSR provisioning, endpoint leaf rotation, one-use Agent migration, revocation, and multi-Client routing.
- Four-stream per-Client and 32-stream aggregate enforcement with bounded session state.
- Self-contained Windows controller flow with distinct absent, installed/offline, and online Tailscale states; duplicate-MSI suppression; connect-only browser/unattended setup; raw TCP Funnel; UAC relay lifecycle; DPAPI Owner/AWS/node state; IAM Identity Center; EC2 inventory; guarded SSM IAM preparation; signed node install/update/repair; a default Refract proxy-line action; and a SOCKS5 fallback action.
- Shared React/Wails and Go desktop controller with a thin Darwin root, native menu-bar lifecycle, unchanged four-tab/backend contract, Security.framework Keychain storage, Service Management relay states/gates, strict relay-admin IPC, verified macOS Tailscale PKG/app onboarding, and macOS `tailscale up` behavior.
- Headless Windows Client service with on-node P-256/X25519 keys, sealed/replay-protected configuration, loopback authenticated SOCKS5 plus HTTP forward/CONNECT listeners, outbound reconnect, and Windows SCM support.
- Android cellular-only foreground Agent, strict enrollment/migration QRs, Android Keystore identity retention, bounded fair queues, 32-stream admission, and guided non-root cellular IP rotation with transient IPv4/IPv6 comparison.
- Windows signing plus deterministic Apple Silicon/macOS 13 staging, Developer ID/notary packaging machinery, strict local verification record, and coupled Desktop release orchestration. The public Desktop assets are the Windows controller ZIP, Windows EC2 Client, and macOS PKG at one version; Android remains independently selectable.
- Windows-to-Mac SSH build-server runbook for Desktop PKG production and separate future iOS Agent development.

## Automated validation

The full local gate covers Go unit/integration tests and vet, Windows builds, frontend typecheck/build, Android unit tests/lint/debug APK, PowerShell operation-script tests, strict protocol/crypto cases, AWS/IAM guards, single-controller enforcement, atomic node-capacity reservations/cancellation, partial-install and endpoint-rotation retry, encrypted-state migration, secret redaction, service command construction, hidden background Tailscale CLI launches, stream bounds/fairness, endpoint migration, cellular IP-rotation transitions, public-IP parsing/failure isolation, one-shot settings launch, and diagnostic redaction. Component release gates run the same relevant checks while omitting unrelated Android work from Windows releases and unrelated Windows work from Android releases.

Portable Mac-focused tests cover Keychain policy/adapters, relay framing/authorization/peer-credential selection, Service Management state gates, Tailscale package/app parsing/trust/arguments/cleanup, packaging fixtures, frontend platform copy, release-record validation, and coupled Desktop release contracts. Darwin selection/cross-compilation catches build-tag/composition errors available from the Windows host. These tests do not prove live Security.framework, Service Management/Login Items, root ACL/socket behavior, authentic current Apple/Tailscale chains, Apple Installer, Developer ID/notary service access, SSH transport, or real Funnel/cellular traffic.

Go's race detector is not available in the current Windows environment unless CGO and a supported C compiler are installed. Normal Go tests are still run. See the latest commit/CI output for release evidence.

## Required external acceptance

The repository cannot automatically prove real Tailscale browser/Funnel authorization, real AWS IAM/SSM behavior, Windows UAC/service ACLs on clean machines, Android radio behavior, or carrier egress. Mac production acceptance is also pending: the Developer ID-signed/notarized exact-commit PKG and private verification record, quarantined install on the available macOS 26.2 Apple-Silicon Mac, Service Management approval/restart, signed [Keychain continuity](macos-keychain-integration.md), Tailscale install/login/Funnel, Android pairing, one real EC2 Client, proxy traffic, rotation/update/repair, reboot recovery, and logout fail-closed behavior.

All of those Mac items are **pending/unrun** unless a separately retained record says otherwise. Follow the [signed-release and physical-acceptance runbook](deployment.md), preserve the Windows/Android two-node regression, and complete the additional Mac one-node [acceptance record](templates/physical-acceptance-record.md) before stable promotion.

## Known limits

- One operator computer, one relay, and one active Android Agent are availability dependencies. A Mac additionally requires the controlling administrator to remain logged in with Keychain/per-user Tailscale available; logout fails closed.
- At most ten managed EC2 nodes, four streams per Client, and 32 total streams.
- Windows 10/11 or Apple Silicon macOS 13+ controller; x86-64 Windows Server 2019 nodes in `us-east-1` only.
- Funnel is beta, requires browser approval, uses public `*.ts.net` names, and has non-configurable bandwidth limits. Personal-plan use must comply with Tailscale terms; commercial/bulk use needs a supported ingress arrangement.
- No automatic GitHub updater. The operator deliberately downloads a signed controller bundle/PKG; node update/repair uses release metadata embedded in that signed controller.
- No Intel/universal Mac build, Windows-to-Mac private-state migration, Mac headless Client, or ZFNF Mac App Store distribution. `v1.1.0` is clean-install-only for a new Mac bridge; same-Mac signed upgrade/repair preserves identities and remains a physical acceptance requirement.
- The Mac build host now produces the Desktop PKG. Future iOS simulator/local proof work remains separate; TestFlight, App Store, or Ad Hoc distribution requires the applicable paid Apple enrollment.
- Endpoint migration preserves the CA and identities; it is not recovery from relay-state/CA compromise.
- The app does not create/terminate EC2, open inbound rules, guarantee a carrier IP change, or route all OS traffic.
