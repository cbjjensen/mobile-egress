# Mobile Egress physical acceptance record

Save a copy of this template with the private release evidence. Do not record QR payloads, capabilities, SOCKS credentials, private keys, relay/device certificates, destinations, carrier/EC2 IP addresses, or traffic payloads.

## Release identity

| Field | Value |
|---|---|
| Release tag | |
| Source commit | |
| Test start/end (UTC) | |
| Windows ZIP filename / SHA-256 | |
| Client filename / SHA-256 | |
| Authenticode subject / thumbprint | |
| Android APK filename / SHA-256 (if Android) | |
| Android public signer digest (if Android) | |
| Android versionCode / versionName (if Android) | |
| iOS bundle ID / TestFlight build (if iOS) | |

## Sanitized environment

| Field | Value |
|---|---|
| Controller Windows version | |
| Tailscale version | |
| Agent platform, model, and OS version | |
| EC2 node count / Windows image family | |
| AWS region | `us-east-1` |

Use lab labels such as node A/node B instead of instance IDs.

## Required results

Use `PASS`, `FAIL`, or `NOT RUN` for executed required checks. Keep the two Android capacity-host rows as `PENDING` until actually run. A required `FAIL`, `NOT RUN`, or `PENDING` blocks the affected stable promotion.

| Check | Result | Sanitized note |
|---|---|---|
| Downloaded Windows ZIP hash/signatures match release evidence | | |
| Android APK hash/signer match release evidence (if Android) | | |
| iOS signed archive/TestFlight build match release evidence (if iOS) | | |
| App-only Tailscale Funnel and loopback relay setup | | |
| Relay listens only on `127.0.0.1:8443` | | |
| Selected Agent pairs and connects over cellular with Wi-Fi enabled | | |
| Two SSM-managed Clients install with distinct identities | | |
| Each SOCKS listener is authenticated and `127.0.0.1:1080` only | | Application opt-in on the same EC2 node; not system-wide/VPN/public/UDP/QUIC. |
| Each ordinary-HTTP/HTTPS-CONNECT listener is authenticated and `127.0.0.1:1081` only | | Application opt-in on the same EC2 node; not controller-host or system-wide. |
| Node A direct/proxied egress differ; values not recorded | | |
| Node B direct/proxied egress differ; values not recorded | | |
| Both Clients route simultaneously without changing default routes | | |
| Stream 33 on one Client identity fails closed with `client_stream_limit` | | The first 32 share the same session across SOCKS, HTTP, CONNECT, and retained idle HTTP streams. |
| Cellular loss with Wi-Fi available fails closed | | |
| Guided IP rotation warns before disconnecting active streams | | Record only the stream count and confirmation outcome. |
| Platform guidance supports the required manual Airplane Mode changes | | Android opens public system settings; iOS instructs the operator to use Control Center. |
| Ten-second rotation reconnects the relay without Wi-Fi fallback | | Record changed/unchanged/unverified only; never record addresses. |
| Unchanged result offers and completes a 30-second retry | | |
| Rotation cancellation or timeout restores normal Agent behavior | | |
| Controller PC reboot recovery | | |
| EC2 node A/B reboot recovery | | |
| Agent restart/relaunch behavior matches its platform expectation | | |
| Signed Client Update retains identity/credentials | | |
| Repair restores service/config without identity change | | |
| Tailscale-name endpoint migration out and back | | |
| SSM/log review finds no plaintext secrets | | |
| No EC2/public-IP/inbound-rule mutation | | |
| Relay/node ProgramData ACL review | | |

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
| Signed iPhone starts the enrolled Agent with Wi-Fi enabled while relay and target traffic remain cellular-only | | |
| Starting rotation with active streams presents native confirmation; declining preserves streams and confirming closes them | | Record only a bounded stream count. |
| Normal 10-second Control Center attempt observes cellular loss/return and restores the relay with no Wi-Fi fallback | | The app must not toggle Airplane Mode or open a private Settings URL. |
| Result is presented as changed, unchanged, or unverified without copying/logging either public address | | |
| Unchanged result offers and completes the 30-second retry path | | Record the second categorical outcome only. |
| Cancellation while waiting/holding returns to a terminal state and restores the prior Agent/on-demand intent | | |
| Two-minute cellular-loss timeout and three-minute cellular-return timeout each attempt Agent restoration | | Exercise separately when release time permits; `NOT RUN` blocks stable iOS promotion. |
| Backgrounding for Control Center and returning to the foreground resumes the in-flight attempt or bounded checkpoint recovery | | |
| Completion, cancellation, and recoverable failure leave the Agent/on-demand state restored; explicit restoration failure requires a manual Agent start | | |
| Cellular loss while Wi-Fi remains usable closes streams and relayed requests continue to fail closed until cellular returns | | Confirms continued no-Wi-Fi fallback. |
| VoiceOver reads the status hierarchy, health values, metrics, errors, and every action with an unambiguous label/value | | Exercise pairing, running, failed, rotation, and restoration states. |
| Largest Dynamic Type keeps all finite copy readable and every primary, rotation, scanner, and diagnostic action reachable | | No clipped copy or hidden required control. |
| Reduce Motion removes nonessential animation without hiding or delaying state, confirmation, countdown, or recovery feedback | | |
| Scanner camera-permission denial presents finite recovery guidance; granting permission later restores native scanning | | Do not record QR contents. |
| Signed physical iPad preserves readable dashboard layout and reachable actions in portrait, upside-down portrait, landscape left, and landscape right | | Exercise rotation/restoration copy in every orientation. |

## Optional cross-check capacity

| Check | Result | Sanitized note |
|---|---|---|
| Eight Client identities hold 256 fair aggregate streams for 15 minutes after verified 16 KiB echoes | | |
| Ninth identity's aggregate stream 257 fails closed | | |
| Close one held stream and immediately open one verified replacement | | |

The cross-check requires eight holding Client identities because each is capped at 32 streams, plus a ninth legitimate probe identity for aggregate stream 257. Automated tests cover the 256/257 aggregate boundary when the physical lab has only two nodes. There is no throughput floor.

## Exceptions and sign-off

Finite failure classes or approved deviations (no sensitive data):

- 

Release decision: `ACCEPT` / `REJECT`

Operator/reviewer initials and UTC date:

- 
