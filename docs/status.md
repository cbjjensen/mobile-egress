# Current status

## Implemented

- Loopback-only Windows relay service, direct CSR Owner bootstrap, Owner-authorized Client CSR provisioning, endpoint leaf rotation, one-use Agent migration, revocation, and multi-Client routing.
- Four-stream per-Client and 32-stream aggregate enforcement with bounded session state.
- Self-contained Windows controller flow with distinct absent, installed/offline, and online Tailscale states; duplicate-MSI suppression; connect-only browser/unattended setup; raw TCP Funnel; UAC relay lifecycle; DPAPI Owner/AWS/node state; IAM Identity Center; EC2 inventory; guarded SSM IAM preparation; signed node install/update/repair; a default Refract proxy-line action; and a SOCKS5 fallback action.
- Headless Windows Client service with on-node P-256/X25519 keys, sealed/replay-protected configuration, loopback authenticated SOCKS5 plus HTTP forward/CONNECT listeners, outbound reconnect, and Windows SCM support.
- Android cellular-only foreground Agent with strict enrollment/migration QRs, Android Keystore identity retention, bounded fair queues, 32-stream admission, guided non-root cellular IP rotation, ZFNF OLED status presentation, safe copied diagnostics, and separate cellular/relay visibility.
- iOS/iPadOS 17+ Agent with VisionKit scanning, Secure Enclave/shared-Keychain identity retention, cellular-required pinned/mTLS relay and target transports, an app-managed on-demand packet tunnel with no included routes, bounded runtime, guided Control Center cellular-IP rotation, ZFNF OLED dashboard/assets, safe copied diagnostics, separate cellular/relay visibility, and the same QR/protocol/32-stream limits as Android.
- Versioned mobile parity manifest with tracked Android/iOS source and test evidence for every recorded user-facing capability.
- Signed release packaging script and app-first friend documentation.
- Windows-to-Mac SSH build-server runbook for the iOS Agent's exact-tree verification.

## Automated validation

The full local gate covers Go unit/integration tests and vet, Windows builds, frontend typecheck/build, Android unit tests/lint/debug APK, PowerShell operation-script tests, strict protocol/crypto cases, AWS/IAM guards, single-controller enforcement, atomic node-capacity reservations/cancellation, partial-install and endpoint-rotation retry, encrypted-state migration, secret redaction, service command construction, hidden background Tailscale CLI launches, stream bounds/fairness, endpoint migration, cellular IP-rotation transitions, public-IP parsing/failure isolation, one-shot settings launch, and diagnostic redaction. Component release gates run the same relevant checks while omitting unrelated Android work from Windows releases and unrelated Windows work from Android releases.

`scripts/test-ios.ps1` runs portable Swift tests on Windows through Docker and then reports Xcode validation as unsupported unless `-UseMacBuildServer` is selected. That selected path requires a clean committed tree, transfers a disposable bundle for that exact commit to the Mac, and runs both Swift suites (including warnings as errors), Xcode project/workspace listing, an unsigned iPhoneOS host-plus-extension build, and Xcode-hosted package tests. Only a known final-package-test `testmanagerd` invalidation is retried, once; a persistent retry failure remains a failed Mac environment result.

The full and mobile component gates validate [the mobile feature manifest](mobile-feature-manifest.json), so a missing platform, unsupported status, or untracked evidence path blocks the gate.

Go's race detector is not available in the current Windows environment unless CGO and a supported C compiler are installed. Normal Go tests are still run. See the latest commit/CI output for release evidence.

## Required external acceptance

The repository cannot automatically prove real Tailscale browser/Funnel authorization, real AWS IAM/SSM behavior, Windows UAC/service ACLs on clean machines, Android or iOS radio behavior on physical hardware, carrier egress, iOS active-stream rotation and foreground recovery, iOS provisioning, TestFlight upload, or empty-route packet-tunnel acceptance. Follow the [signed-release and physical-acceptance runbook](deployment.md), the [iOS real-device checklist](../ios/README.md#real-device-acceptance), and save a sanitized copy of the [acceptance record](templates/physical-acceptance-record.md) before promoting a prerelease to stable.

## Known limits

- One operator PC, one relay, and one active Android or iOS Agent are availability dependencies.
- At most ten managed EC2 nodes, four streams per Client, and 32 total streams.
- Windows 10/11 controller and x86-64 Windows Server 2019 nodes in `us-east-1` only.
- Funnel is beta, requires browser approval, uses public `*.ts.net` names, and has non-configurable bandwidth limits. Personal-plan use must comply with Tailscale terms; commercial/bulk use needs a supported ingress arrangement.
- No automatic GitHub updater. The operator deliberately downloads a signed controller bundle; node update/repair uses release metadata embedded in that signed controller.
- iOS Agent work requires a Mac build host with Xcode. Simulator/local proof work can start without a paid Apple Developer Program membership, but friend distribution through TestFlight, App Store, or Ad Hoc requires paid Apple enrollment.
- Guided IP rotation cannot guarantee carrier reassignment. Android and iOS both require manual Airplane Mode interaction; iOS guides Control Center without changing Airplane Mode or opening a private Settings URL.
- Endpoint migration preserves the CA and identities; it is not recovery from relay-state/CA compromise.
- The app does not create/terminate EC2, open inbound rules, guarantee a carrier IP change, or route all OS traffic.
