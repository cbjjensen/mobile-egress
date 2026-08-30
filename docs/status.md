# Current status

## Implemented

- Loopback-only Windows relay service, direct CSR Owner bootstrap, Owner-authorized Client CSR provisioning, endpoint leaf rotation, one-use Agent migration, revocation, and multi-Client routing.
- Four-stream per-Client and 32-stream aggregate enforcement with bounded session state.
- Self-contained Windows controller flow for verified Tailscale MSI installation, browser login, unattended raw TCP Funnel, UAC relay lifecycle, DPAPI Owner/AWS/node state, IAM Identity Center, EC2 inventory, guarded SSM IAM preparation, signed node install/update/repair, and SOCKS credential display.
- Headless Windows Client service with on-node P-256/X25519 keys, sealed/replay-protected configuration, loopback authenticated SOCKS5, outbound reconnect, and Windows SCM support.
- Android cellular-only foreground Agent, strict enrollment/migration QRs, Android Keystore identity retention, bounded fair queues, and 32-stream admission.
- Signed release packaging script and app-first friend documentation.

## Automated validation

The local gate covers Go unit/integration tests and vet, Windows builds, frontend typecheck/build, Android unit tests/lint/debug APK, PowerShell operation-script tests, strict protocol/crypto cases, AWS/IAM guards, single-controller enforcement, atomic node-capacity reservations/cancellation, partial-install and endpoint-rotation retry, encrypted-state migration, secret redaction, service command construction, stream bounds/fairness, and endpoint migration.

Go's race detector is not available in the current Windows environment unless CGO and a supported C compiler are installed. Normal Go tests are still run. See the latest commit/CI output for release evidence.

## Required external acceptance

The repository cannot automatically prove real Tailscale browser/Funnel authorization, real AWS IAM/SSM behavior, Windows UAC/service ACLs on clean machines, Android radio behavior on physical hardware, or carrier egress. Complete and record the physical checklist in [deployment](deployment.md) before calling a release accepted.

## Known limits

- One operator PC, one relay, and one active Android Agent are availability dependencies.
- At most ten managed EC2 nodes, four streams per Client, and 32 total streams.
- Windows 10/11 controller and x86-64 Windows Server 2019 nodes in `us-east-1` only.
- Funnel is beta, requires browser approval, uses public `*.ts.net` names, and has non-configurable bandwidth limits. Personal-plan use must comply with Tailscale terms; commercial/bulk use needs a supported ingress arrangement.
- No automatic GitHub updater. The operator deliberately downloads a signed controller bundle; node update/repair uses release metadata embedded in that signed controller.
- Endpoint migration preserves the CA and identities; it is not recovery from relay-state/CA compromise.
- The app does not create/terminate EC2, open inbound rules, guarantee a carrier IP change, or route all OS traffic.
