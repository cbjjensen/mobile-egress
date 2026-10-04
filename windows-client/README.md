# Workload Client development

This directory contains the Windows and Apple Silicon Mac direct Client service/app. Its historical name does not imply Windows-only support. The 2.x product has no central controller, AWS management, Tailscale or Funnel dependency.

Use the [Client installation guide](../docs/standalone-clients.md), [architecture](../docs/architecture.md), [security model](../docs/security-model.md), and [direct protocol contract](../docs/direct-protocol-v2.md). Existing 1.x published downloads remain historical and incompatible with direct pairing.

Production entrypoints are `cmd/mobile-egress-client` (native background service) and `cmd/mobile-egress-client-app` (graphical local administration). The service exposes authenticated Agent TLS on a configured reachable endpoint and local-only HTTP/CONNECT/SOCKS proxies. Protected OS IPC carries administration; the GUI never reads service private storage. Legacy bootstrap/apply-config commands reject with migration guidance.

Windows uses LocalSystem DPAPI and `127.0.0.2:1081`/`:1080`; Mac uses LaunchDaemon/System Keychain and `127.0.0.1:1081`/`:1080`. Listener 8443 is configurable and distinct from the advertised public port. A phone connects outbound on cellular.

Run Go tests/vet, Client app Node tests, installer product/migration tests, release contract tests and native Mac secure-store/service tests. Unit/loopback results do not establish signed upgrade, boot/logout, public reachability or physical phone acceptance. The approved plan tracks remaining gates.
