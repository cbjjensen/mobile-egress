# Retire a former 1.x controller

Retirement is separate from installing a 2.x workload Client. A personal computer that previously hosted the controller does not need a public Client listener unless its owner deliberately chooses to run workloads there.

1. Migrate the actual workload machines with compatible Client installers and fresh direct pairing. Verify phone traffic with the old relay offline.
2. Preserve encrypted controller/relay recovery state and any historical evidence. Do not export private keys or plaintext proxy credentials.
3. Inspect startup entries and service executable paths. Stop/disable only the entry whose exact executable and protected installation path identify the old Mobile Egress controller/relay. On Mac, the old relay LaunchDaemon label is `com.cbjjensen.mobile-egress.relay`; inspect its plist before acting. On Windows, inspect service command paths and login startup entries rather than guessing from a display name.
4. Inspect Tailscale's existing Serve/Funnel configuration. Remove only the mapping whose exact target is the retired Mobile Egress loopback relay on port 8443. If ownership is ambiguous, leave it unchanged until identified. Never run a global Serve/Funnel reset or uninstall Tailscale as a shortcut.
5. Confirm unrelated Tailscale routes, services and mappings remain intact. Remove old application files only after backup and verified retirement.

There is no automatic retirement script that broadly deletes services or network configuration. The 2.x Client neither starts the old controller nor manages Tailscale. Do not deploy an old relay elsewhere as a migration bridge; 2.x has no legacy fallback.

On the phone, the upgrade removes the old active association. iOS additionally stops, disables and removes only app-owned VPN profiles. Direct Start stays blocked if that cleanup cannot be confirmed; resolve it using the app's instructions without altering another app's VPN.
