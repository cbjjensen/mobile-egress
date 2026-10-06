# Inevitable Mobile Relay 2 direct physical acceptance record

Hosted addendum: record gateway image/source, Client/phone versions and selected mode. Prove outbound-only Windows/Mac setup with no router/provider ingress; preserve Advanced direct results separately. Cover IPv4/IPv6/NAT64, cross-node routing, account revocation versus local pairing revocation, live/offline signed mode updates, and the iOS active/keep-awake exception. Attach combined Core/Mobile contention results and confirmation that existing proxy usage continues while Mobile submits none. Mark unavailable checks NOT RUN or BLOCKED.

Copy into private release evidence. Every unexecuted check remains NOT RUN. FAIL, NOT RUN or PENDING on a required gate blocks stable promotion. Do not record invitations, capabilities, proxy passwords, private keys, device identifiers, public endpoint/target addresses or traffic payloads.

## Release identity

| Field | Value |
|---|---|
| Exact source commit and release tag | |
| Selected component scope | |
| Windows Client installer filename / SHA-256 / public signer digest | |
| Mac Client PKG filename / SHA-256 / public Developer ID identities | |
| Notarization, staple and private verification-record hash | NOT RUN |
| Android APK hash / public signer digest / versionCode / versionName | |
| iOS app and cleanup extension build / signing profile match | |
| Test start/end UTC and operator sign-off | |

## Environment

Record sanitized lab labels, workload OS/architecture, phone model/OS, carrier category, test duration and thermal/power conditions. Do not record addresses or account/device identifiers. Test Windows x64 and Apple Silicon Mac, plus physical Android and iPhone. Confirm old relay is offline; AWS and Tailscale are not dependencies.

## Required checks

| Check | Result | Sanitized evidence |
|---|---|---|
| Downloaded artifact hashes/signatures match frozen source and records | NOT RUN | |
| Windows/Mac clean install, boot and logout service operation | NOT RUN | |
| Signed upgrade/repair preserves identity, proxy credentials and ownership | NOT RUN | |
| Windows installer opens unelevated Client wizard; Mac launches only saved active GUI owner and never root | NOT RUN | |
| Mac missing GUI/different foreground user/headless repair or failed app handoff preserves installed service and owner | NOT RUN | |
| Five-step setup resumes after close/Finish later without new identities; existing offline pairing opens dashboard | NOT RUN | |
| Address discovery/manual override, VPN/NAT misleading suggestion and differing public/local ports remain clearly unverified | NOT RUN | |
| Windows scoped rule and Mac application exception read back correctly; block-all/managed policy and unrelated rules preserved | NOT RUN | |
| Check/Retry firewall preserves pairing capability, endpoint generation and proxy credentials | NOT RUN | |
| Wizard verifies only a live authenticated cellular session; blocked router/provider ingress never reports Connected | NOT RUN | |
| AWS EC2 missing security-group rule/source mismatch and public-route requirement produce actionable guidance | NOT RUN | |
| Known AWS-installed Windows service migrates locally; unknown service rejected | NOT RUN | |
| Migration requires new trust; no old relay fallback | NOT RUN | |
| System Keychain native CRUD and same-signed upgrade continuity | NOT RUN | |
| DPAPI/service-owner continuity and local IPC unauthorized-user rejection | NOT RUN | |
| HTTP, CONNECT and SOCKS exact-byte traffic through each phone/workload combination | NOT RUN | |
| Proxy endpoints remain local; public listener exposes no proxy/admin API | NOT RUN | |
| Cellular-only pairing/tunnel/targets with Wi-Fi available; cellular loss fails closed | NOT RUN | |
| Invitation expiry, replay, wrong key, cancellation and interrupted persistence | NOT RUN | |
| Lost issued response/ACK retries recover one identity | NOT RUN | |
| Android failed durable registry write cannot ACK uncommitted credentials or delete a possibly referenced key; retry/relaunch recovers | NOT RUN | |
| Concurrent 10/11 phone reservations; pending/disabled slots included | NOT RUN | |
| Revocation/reconnect race and authenticated renewal | NOT RUN | |
| Repeated/offline endpoint updates, skipped generations, stale/conflicting/trust-changing updates | NOT RUN | |
| IPv6 spelling and numeric-port normalization permit pairing/update ACK; wrong authority still rejects | NOT RUN | |
| IPv6 literal Client pairing uses valid HTTP/TLS authority; signed retained padded-port updates recover on both phones | NOT RUN | |
| Occupied ports, blocked ingress, unavailable secure storage and invalid certificates | NOT RUN | |
| Rejected pairing, expired trust, locked phone storage and unavailable Client storage show distinct safe recovery actions; other Clients continue | NOT RUN | |
| Exact EOF/tail, slow reader, cancellation and complete debt cleanup | NOT RUN | |
| Duplicate stream IDs across Clients, peer failure isolation and control responsiveness | NOT RUN | |
| Android screen-off/background operation, cellular recovery, reboot requiring Start | NOT RUN | |
| Android dashboard Stop remains available during pairing/import and after last Client removal | NOT RUN | |
| Android explicit Retry after storage/clock recovery resumes retrying through a temporary network failure; disabled/removed peers stay stopped | NOT RUN | |
| Both dashboards distinguish cellular availability from per-Client connection and sharing intent | NOT RUN | |
| iOS only app-owned VPN cleanup; failure blocks Start; other profiles unchanged | NOT RUN | |
| iOS inactive/app switch/manual lock closes sessions and targets | NOT RUN | |
| iOS active return resumes only retained Start intent; Stop clears intent | NOT RUN | |
| Keep-awake default on; no-touch sharing beyond short auto-lock interval | NOT RUN | |
| Preference off and Stop restore normal auto-lock; brightness unchanged | NOT RUN | |
| Manual lock still pauses with keep-awake on | NOT RUN | |
| Phone-wide rotation restores only still-enabled Clients | NOT RUN | |
| iOS relaunch during rotation restores the pause before sharing; explicit Stop survives recovery | NOT RUN | |
| iOS full Airplane Mode cycle while inactive retains delivered observations; missing observations require the guided foreground sequence | NOT RUN | |
| iOS unchanged-IP retry starts the offered 30-second attempt from the dashboard | NOT RUN | |
| Diagnostics/logs contain no secrets or traffic payloads | NOT RUN | |

## Sustained measurements

| Scenario | Duration / bytes / concurrency | Goodput Mbps / latency | CPU / memory / thermal | Fairness / debt cleanup |
|---|---|---|---|---|
| One Client download/upload | NOT RUN | | | |
| Ten Clients mixed workloads | NOT RUN | | | |
| Slow-reader and failure isolation | NOT RUN | | | |
| Browser traffic under sustained transfer | NOT RUN | | | |

Attach sanitized measured summaries and exact build references. Compare historical relay evidence explicitly as historical. A successful automated or unsigned build is not a PASS for any physical row. Record unresolved blockers and operator approval before marking downloads release-ready.
