# Mobile Egress physical acceptance record

Save a copy of this template with the private release evidence. Do not record QR payloads, capabilities, SOCKS credentials, private keys, relay/device certificates, Apple chain dumps, operator UIDs, destinations, carrier/EC2 IP addresses, or traffic payloads. Every real check starts `NOT RUN`; a required `FAIL` or `NOT RUN` blocks stable promotion.

## Release identity

| Field | Value |
|---|---|
| Release tag | |
| Component scope | |
| Source commit | |
| Test start/end (UTC) | |
| Windows ZIP filename / SHA-256 | |
| Client filename / SHA-256 | |
| Authenticode subject / thumbprint | |
| Mac PKG filename / SHA-256 | `mobile-egress-macos-1.1.0-arm64.pkg` / |
| Developer ID Application / Installer public identity | |
| Notarization / staple | `NOT RUN` |
| Node-manifest SHA-256 | |
| Mac verification-record private path / SHA-256 | Private evidence; not a GitHub asset / |
| APK filename / SHA-256 | |
| APK public signer digest | |
| Android versionCode / versionName | |
| Private same-Mac upgrade fixture | Signed/notarized `1.0.999`, built from final source; no Git tag or GitHub asset |

## Sanitized environment

| Field | Value |
|---|---|
| Controller Windows version | |
| Mac model / Apple Silicon / macOS version | Available acceptance target: macOS 26.2 / |
| Mac operator is administrator and logged in | Do not record UID / |
| Tailscale Windows version | |
| Tailscale Mac variant / version | standalone or App Store / |
| Android model / OS version | |
| EC2 node count / Windows image family | |
| AWS region | `us-east-1` |

Use lab labels such as node A/node B instead of instance IDs.

## Required results

### Coupled Windows/Android baseline

Use `PASS`, `FAIL`, or `NOT RUN`. A required `FAIL` or `NOT RUN` blocks stable promotion.

| Check | Result | Sanitized note |
|---|---|---|
| Downloaded Windows ZIP hash/signatures match release evidence | `NOT RUN` | |
| Downloaded APK hash/signer match release evidence | `NOT RUN` | |
| App-only Tailscale Funnel and loopback relay setup | `NOT RUN` | |
| Relay listens only on `127.0.0.1:8443` | `NOT RUN` | |
| Android pairs and connects over cellular with Wi-Fi enabled | `NOT RUN` | |
| Two SSM-managed Clients install with distinct identities | `NOT RUN` | |
| Each SOCKS listener is authenticated and `127.0.0.1:1080` only | `NOT RUN` | |
| Node A direct/proxied egress differ; values not recorded | `NOT RUN` | |
| Node B direct/proxied egress differ; values not recorded | `NOT RUN` | |
| Both Clients route simultaneously without changing default routes | `NOT RUN` | |
| Fifth held-open stream on one Client fails closed | `NOT RUN` | |
| Cellular loss with Wi-Fi available fails closed | `NOT RUN` | |
| Guided IP rotation disconnects active streams and opens Airplane Mode settings | `NOT RUN` | Record changed/unchanged/unverified only; never record addresses. |
| Ten-second rotation reconnects the relay without Wi-Fi fallback | `NOT RUN` | |
| Unchanged result offers and completes a 30-second retry | `NOT RUN` | |
| Rotation cancellation or timeout restores normal Agent behavior | `NOT RUN` | |
| Controller PC reboot recovery | `NOT RUN` | |
| EC2 node A/B reboot recovery | `NOT RUN` | |
| Android reboot plus explicit Start recovery | `NOT RUN` | |
| Signed Client Update retains identity/credentials | `NOT RUN` | |
| Repair restores service/config without identity change | `NOT RUN` | |
| Tailscale-name endpoint migration out and back | `NOT RUN` | |
| SSM/log review finds no plaintext secrets | `NOT RUN` | |
| No EC2/public-IP/inbound-rule mutation | `NOT RUN` | |
| Relay/node ProgramData ACL review | `NOT RUN` | |

### macOS v1.1.0 controller

Clean-install-only means the first Mac bridge begins with empty Mac controller/relay state and imports no Windows private state. Test same-Mac upgrade with the private signed/notarized `1.0.999` fixture built from the final source; do not create a tag or GitHub asset for the fixture.

| Check | Result | Sanitized note |
|---|---|---|
| GitHub PKG retains quarantine; exact filename/hash, Developer ID Installer, notarization, and staple match release evidence | `NOT RUN` | Do not bypass Gatekeeper. |
| App installs at `/Applications/ZFNF Mobile Egress.app`; controller/relay are arm64; minimum target is 13.0; identifiers/plists/layout are exact | `NOT RUN` | |
| Fresh Mac bridge contains no migrated Windows/controller private state | `NOT RUN` | |
| Verified Tailscale standalone PKG install, system-extension/VPN approval, browser login, and raw Funnel 8443 succeed; an existing authentic standalone or App Store app is also accepted | `NOT RUN` | Team ID `W5364U7YZB`; no raw chain dumps. |
| Service transitions through `not-registered`/`approval-required`; Login Items opens; no Owner exists before exact enabled proof; later Setup succeeds | `NOT RUN` | |
| Root LaunchDaemon is enabled and independently restarts; relay listens only on `127.0.0.1:8443` | `NOT RUN` | Inspect metadata, never private state. |
| Keychain Owner/AWS/node state persists across quit/relaunch, same-signed upgrade, and reboot/login; no file fallback or sync is observed | `NOT RUN` | Use the signed harness. |
| Android pairs, starts cellular-only, and reconnects to the Mac-owned bridge | `NOT RUN` | |
| One real Windows Server 2019 EC2 Client installs through SSM with private keys on-node | `NOT RUN` | |
| Ordinary HTTP, HTTPS CONNECT, and SOCKS5 use cellular egress; direct EC2 route remains unchanged | `NOT RUN` | Do not record addresses. |
| Cellular loss with Wi-Fi present fails closed and later recovers | `NOT RUN` | |
| Endpoint rotation plus Android migration preserves CA, Owner/Agent/Client identities, and proxy credentials | `NOT RUN` | |
| Client Update and local relay Repair preserve state; Mac repair restarts and exact helper health returns | `NOT RUN` | |
| Window close/menu-bar reopen and full controller quit/relaunch preserve relay/Keychain state | `NOT RUN` | |
| Mac reboot then operator login restores the bridge without identity replacement | `NOT RUN` | |
| Operator logout fails proxy traffic closed; relogin/reopen recovers the same identities | `NOT RUN` | Root daemon alone is not availability. |
| UI/activity/IPC/SSM review finds no Owner/AWS/node/proxy/CA/raw-error/destination/payload leakage | `NOT RUN` | |

## Optional extended capacity

| Check | Result | Sanitized note |
|---|---|---|
| Eight Clients hold 32 fair aggregate streams | `NOT RUN` | |
| Aggregate stream 33 fails closed | `NOT RUN` | |

The optional checks require at least eight Client identities because each Client is capped at four streams. Automated tests cover the 32/33 aggregate boundary when the physical lab has only two nodes.

## Exceptions and sign-off

Finite failure classes or approved deviations (no sensitive data):

- 

Release decision: `BLOCKED` pending required results / `ACCEPT` / `REJECT`

Operator/reviewer initials and UTC date:

- 
