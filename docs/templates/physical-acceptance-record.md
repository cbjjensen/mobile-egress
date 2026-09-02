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
| Mac PKG filename / SHA-256 (if Desktop) | `mobile-egress-macos-<version>-arm64.pkg` / |
| Developer ID Application / Installer public identity | |
| Notarization / staple | `NOT RUN` |
| Node-manifest SHA-256 | |
| Mac verification-record private path / SHA-256 | Private evidence; not a GitHub asset / |
| Android APK filename / SHA-256 (if selected or linked fallback) | |
| Android public signer digest (if selected or linked fallback) | |
| Android versionCode / versionName (if selected or linked fallback) | |
| iOS bundle ID / TestFlight build (if iOS) | |
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
| iOS/iPadOS model / OS version (if tested) | |
| EC2 node count / Windows image family | |
| AWS region | `us-east-1` |

Use lab labels such as node A/node B instead of instance IDs.

## Required results

### v1.1.1 Windows / v1.1.0 Android baseline

For the Windows-only v1.1.1 hotfix, record the current Windows ZIP and Client plus the published v1.1.0 Android APK linked by the managed release notes; Android is fallback evidence, not part of the v1.1.1 component scope. Use `PASS`, `FAIL`, or `NOT RUN` for executed required checks. Keep the two Android capacity-host rows below as `PENDING` until actually run. A required `FAIL`, `NOT RUN`, or `PENDING` blocks the affected stable promotion.

| Check | Result | Sanitized note |
|---|---|---|
| Downloaded Windows ZIP hash/signatures match release evidence | `NOT RUN` | |
| Downloaded APK hash/signer match release evidence | `NOT RUN` | |
| App-only Tailscale Funnel and loopback relay setup | `NOT RUN` | |
| Relay listens only on `127.0.0.1:8443` | `NOT RUN` | |
| Android pairs and connects over cellular with Wi-Fi enabled | `NOT RUN` | |
| Two SSM-managed Clients install with distinct identities | `NOT RUN` | |
| Each SOCKS listener is authenticated and `127.0.0.2:1080` only | `NOT RUN` | Application opt-in on the same EC2 node; no `.1` compatibility listener and not system-wide/VPN/public/UDP/QUIC. |
| Each ordinary-HTTP/HTTPS-CONNECT listener is authenticated and `127.0.0.2:1081` only | `NOT RUN` | Application opt-in on the same EC2 node; no `.1` compatibility listener and not controller-host or system-wide. |
| Node A direct/proxied egress differ; values not recorded | `NOT RUN` | |
| Node B direct/proxied egress differ; values not recorded | `NOT RUN` | |
| Both Clients route simultaneously without changing default routes | `NOT RUN` | |
| Stream 33 on one Client identity fails closed with `client_stream_limit` | `NOT RUN` | The first 32 share one session across SOCKS, ordinary HTTP, HTTPS CONNECT, active requests, and retained idle HTTP streams. |
| Cellular loss with Wi-Fi available fails closed | `NOT RUN` | |
| Guided IP rotation warns before disconnecting active streams and opens public Airplane Mode settings after confirmation | `NOT RUN` | Record only the bounded stream count and changed/unchanged/unverified outcome; never record addresses. |
| Ten-second rotation reconnects the relay without Wi-Fi fallback | `NOT RUN` | |
| Unchanged result offers and completes a 30-second retry | `NOT RUN` | |
| Rotation cancellation or timeout restores normal Agent behavior | `NOT RUN` | |
| Controller PC reboot recovery | `NOT RUN` | |
| EC2 node A/B reboot recovery | `NOT RUN` | |
| Android reboot plus explicit Start recovery | `NOT RUN` | |
| Signed Client Update activates the `.2` listeners and retains identity/credentials; both values are recopied | `NOT RUN` | |
| Repair restores service/config without identity change | `NOT RUN` | |
| Tailscale-name endpoint migration out and back | `NOT RUN` | |
| SSM/log review finds no plaintext secrets | `NOT RUN` | |
| No EC2/public-IP/inbound-rule mutation | `NOT RUN` | |
| Relay/node ProgramData ACL review | `NOT RUN` | |

### Future macOS controller

This section is not applicable to the Mac-free v1.1.0 interim release or the v1.1.1 Windows hotfix. Complete it only for a later release whose component scope includes Desktop; the first Mac-bearing version must be later than v1.1.1.

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

## Android 256-stream physical acceptance

Keep these as two separate evidence rows and follow the [authenticated capacity runbook](../capacity-acceptance.md). For each desktop host, all 256 streams must verify an exact 16 KiB echo and then remain live for 15 minutes; the ninth legitimate Client identity's aggregate stream 257 must reject with `agent_stream_limit`; closing one held stream must permit one verified replacement. A passing row also requires no corruption, process or Agent restart, queue overflow, continuously growing memory, or leaked socket. This is not a throughput benchmark and has no throughput floor.

| Desktop bridge host | Result | Sanitized evidence |
|---|---|---|
| Windows-hosted bridge | PENDING | |
| macOS-hosted bridge | PENDING | |

For each bridge row, attach the following sanitized numeric evidence. Use one ordered comma-separated series for the baseline, 15 once-per-minute hold samples, and up-to-seven post-cleanup samples (immediate plus every 10 seconds for up to 60 seconds). The runner series ends when its required bounded exit is observed. Record aggregate counts only; do not attach raw process/socket-tool output or any endpoint, address, port, hostname, identity, destination, token, certificate path, command line, or payload.

| Resource-stability field | Windows-hosted bridge | macOS-hosted bridge |
|---|---|---|
| Harness final aggregate JSON | | |
| Relay/Agent/target/runner process start markers unchanged (`YES`/`NO`) | | |
| Relay memory: baseline / 15 hold samples / post-cleanup samples | | |
| Agent memory: baseline / 15 hold samples / post-cleanup samples | | |
| Target memory: baseline / 15 hold samples / post-cleanup samples | | |
| Runner memory: protected-input baseline / 15 hold samples | | |
| Relay established sockets: baseline / 15 hold samples / post-cleanup samples | | |
| Agent established sockets: baseline / 15 hold samples / post-cleanup samples | | |
| Target established sockets: baseline / 15 hold samples / post-cleanup samples | | |
| Runner established sockets: protected-input baseline / 15 hold samples | | |
| Runner exited with no attributable socket within cleanup budget (`YES` required) | | |
| Saturation-related closure or queue overflow observed (`NONE` required) | | |
| Post-cleanup relay health `connectedClients` / `activeStreams` (`0 / 0` required) | | |
| No component rose in every final five hold samples (`YES` required) | | |
| No surviving component rose in every post-cleanup sample (`YES` required) | | |
| Acceptance sockets returned to pre-run counts within 60 seconds (`YES` required) | | |

## iOS physical acceptance

Current status is `unverified—no device`, and TestFlight promotion is deferred. Complete every row for an iOS release on signed physical iPhone and iPad hardware as applicable. Package tests, an unsigned build, a simulator, Archive validation, or TestFlight upload cannot substitute for these results. Keep Wi-Fi enabled unless a row explicitly changes cellular state, and never record public addresses or private connection material.

| Check | Result | Sanitized note |
|---|---|---|
| Signed iPhone starts the enrolled Agent with Wi-Fi enabled while relay and target traffic remain cellular-only | `NOT RUN` | |
| Starting rotation with active streams presents native confirmation; declining preserves streams and confirming closes them | `NOT RUN` | Record only a bounded stream count. |
| Normal 10-second Control Center attempt observes cellular loss/return and restores the relay with no Wi-Fi fallback | `NOT RUN` | The app must not toggle Airplane Mode or open a private Settings URL. |
| Result is presented as changed, unchanged, or unverified without copying/logging either public address | `NOT RUN` | |
| Unchanged result offers and completes the 30-second retry path | `NOT RUN` | Record the second categorical outcome only. |
| Cancellation while waiting/holding returns to a terminal state and restores the prior Agent/on-demand intent | `NOT RUN` | |
| Two-minute cellular-loss timeout and three-minute cellular-return timeout each attempt Agent restoration | `NOT RUN` | Exercise separately when release time permits; `NOT RUN` blocks stable iOS promotion. |
| Backgrounding for Control Center and returning to the foreground resumes the in-flight attempt or bounded checkpoint recovery | `NOT RUN` | |
| Completion, cancellation, and recoverable failure leave the Agent/on-demand state restored; explicit restoration failure requires a manual Agent start | `NOT RUN` | |
| Cellular loss while Wi-Fi remains usable closes streams and relayed requests continue to fail closed until cellular returns | `NOT RUN` | Confirms continued no-Wi-Fi fallback. |
| VoiceOver reads the status hierarchy, health values, metrics, errors, and every action with an unambiguous label/value | `NOT RUN` | Exercise pairing, running, failed, rotation, and restoration states. |
| Largest Dynamic Type keeps all finite copy readable and every primary, rotation, scanner, and diagnostic action reachable | `NOT RUN` | No clipped copy or hidden required control. |
| Reduce Motion removes nonessential animation without hiding or delaying state, confirmation, countdown, or recovery feedback | `NOT RUN` | |
| Scanner camera-permission denial presents finite recovery guidance; granting permission later restores native scanning | `NOT RUN` | Do not record QR contents. |
| Signed physical iPad preserves readable dashboard layout and reachable actions in portrait, upside-down portrait, landscape left, and landscape right | `NOT RUN` | Exercise rotation/restoration copy in every orientation. |

## Optional cross-check capacity

| Check | Result | Sanitized note |
|---|---|---|
| Eight Client identities hold 256 fair aggregate streams for 15 minutes after verified 16 KiB echoes | `NOT RUN` | |
| Ninth identity's aggregate stream 257 fails closed | `NOT RUN` | |
| Close one held stream and immediately open one verified replacement | `NOT RUN` | |

The cross-check requires eight holding Client identities because each is capped at 32 streams, plus a ninth legitimate probe identity for aggregate stream 257. Automated tests cover the 256/257 aggregate boundary when the physical lab has only two nodes. There is no throughput floor.

## Exceptions and sign-off

Finite failure classes or approved deviations (no sensitive data):

- 

Release decision: `BLOCKED` pending required results / `ACCEPT` / `REJECT`

Operator/reviewer initials and UTC date:

- 
