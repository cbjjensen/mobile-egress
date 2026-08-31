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

Use `PASS`, `FAIL`, or `NOT RUN`. A required `FAIL` or `NOT RUN` blocks stable promotion.

| Check | Result | Sanitized note |
|---|---|---|
| Downloaded Windows ZIP hash/signatures match release evidence | | |
| Android APK hash/signer match release evidence (if Android) | | |
| iOS signed archive/TestFlight build match release evidence (if iOS) | | |
| App-only Tailscale Funnel and loopback relay setup | | |
| Relay listens only on `127.0.0.1:8443` | | |
| Selected Agent pairs and connects over cellular with Wi-Fi enabled | | |
| Two SSM-managed Clients install with distinct identities | | |
| Each SOCKS listener is authenticated and `127.0.0.1:1080` only | | |
| Node A direct/proxied egress differ; values not recorded | | |
| Node B direct/proxied egress differ; values not recorded | | |
| Both Clients route simultaneously without changing default routes | | |
| Fifth held-open stream on one Client fails closed | | |
| Cellular loss with Wi-Fi available fails closed | | |
| Guided IP rotation disconnects active streams and opens Airplane Mode settings | | Record changed/unchanged/unverified only; never record addresses. |
| Ten-second rotation reconnects the relay without Wi-Fi fallback | | |
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

## Optional extended capacity

| Check | Result | Sanitized note |
|---|---|---|
| Eight Clients hold 32 fair aggregate streams | | |
| Aggregate stream 33 fails closed | | |

The optional checks require at least eight Client identities because each Client is capped at four streams. Automated tests cover the 32/33 aggregate boundary when the physical lab has only two nodes.

## Exceptions and sign-off

Finite failure classes or approved deviations (no sensitive data):

- 

Release decision: `ACCEPT` / `REJECT`

Operator/reviewer initials and UTC date:

- 
