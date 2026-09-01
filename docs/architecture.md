# Architecture

## Accepted topology

Every operator has one independent bridge. Its relay and control plane run on either Windows 10/11 or Apple Silicon macOS 13+, up to ten x86-64 Windows Server 2019 EC2 instances are Clients, and one Android phone is the cellular Agent.

```text
EC2 Refract -> loopback HTTP forward/CONNECT -> Client service --+
                                                        +-> public *.ts.net:8443 -> Funnel raw TCP -> 127.0.0.1:8443 relay -> Agent -> cellular target
EC2 workload -> loopback SOCKS5 -> Client service -------+
```

Tailscale passes Mobile Egress TLS bytes without replacing the relay certificate. The public Funnel name is the certificate server name. The local Owner uses `127.0.0.1:8443` as a dial override while still validating the public name.

Managed nodes remain Windows Server 2019; there is no Mac headless Client. The first Mac release does not migrate Windows private state. A Mac bridge additionally depends on its controlling administrator remaining logged in with the per-user Tailscale app and Keychain available; logout makes traffic fail closed.

## Components

### Desktop controller

The shared Wails/React and Go app is the only normal operator interface. Both thin Windows and Darwin roots retain the same four tabs, backend bindings, AWS/node logic, QR flows, and release-manifest handling.

#### Windows composition

It:

- downloads the official stable Tailscale amd64 MSI, verifies the published SHA-256 and a valid Tailscale Authenticode signer, and requests explicit UAC;
- enables unattended Tailscale and `tailscale funnel --bg --yes --tcp=8443 tcp://127.0.0.1:8443`;
- generates the Owner P-256 key in the unelevated process, sends only its CSR to the elevated helper, and stores the resulting Owner identity with Windows DPAPI;
- supports IAM Identity Center device login and DPAPI-encrypted access-key fallback;
- inventories only supported `us-east-1` instances and orchestrates installation/update/repair with SSM;
- stores encrypted node metadata and reveals proxy credentials only on an explicit HTTP-line or SOCKS-URL copy action; and
- coordinates Funnel endpoint rotation, sealed EC2 updates, and a one-use Android migration QR.

#### macOS composition

The Mac root:

- accepts a correctly signed Tailscale standalone (`io.tailscale.ipn.macsys`) or App Store (`io.tailscale.ipn.macos`) app at `/Applications/Tailscale.app`; guided installation verifies the official standalone PKG before Apple Installer;
- sets `TAILSCALE_BE_CLI=1`, invokes `/Applications/Tailscale.app/Contents/MacOS/Tailscale`, and uses `tailscale up` without Windows-only unattended arguments;
- stores Owner/AWS/node state in Security.framework data-protection Keychain service `com.cbjjensen.mobile-egress.controller`, with hashed account names, device-only/non-synchronizing items, and no file fallback; and
- registers the signed root relay through [`SMAppService`](https://developer.apple.com/documentation/servicemanagement/smappservice). Its public states are `not-registered`, `approval-required`, `enabled`, `version-mismatch`, and `unavailable`; Windows reports `not-required`. `enabled` requires both native authorization and authenticated strict-v1 status from the exact helper.

### Local relay

`MobileEgressRelay` runs as LocalSystem, listens only on `127.0.0.1:8443`, and stores its CA, server identity, SQLite authorization state, and aggregate metrics under `C:\ProgramData\MobileEgress\Relay`. The directory ACL grants only SYSTEM and local Administrators.

The Windows SCM execution path is separate from foreground CLI behavior. Public commands are `bootstrap-owner`, `rotate-endpoint`, `serve`, and `--version`. Direct Owner bootstrap signs a locally generated CSR and never creates an Owner invitation.

On macOS, `com.cbjjensen.mobile-egress.relay` is a root LaunchDaemon with fixed state `/Library/Application Support/ZFNF Mobile Egress/Relay` mode `0700`, socket `/var/run/com.cbjjensen.mobile-egress.relay.sock` owned `root:admin` mode `0660`, and the same loopback listener. Strict relay-admin protocol v1 exposes only `status`, `setup`, `rotate`, and `repair`. First setup binds a kernel-authenticated nonzero administrator UID; later management accepts only that UID or root. Bounded frames, request IDs, deadlines, typed results, and allowlisted errors prevent Owner/AWS/node/CA-key/raw-error material from crossing IPC.

The relay permits multiple simultaneous Clients, one active Agent session, four streams per Client, and 32 streams total. A rejected or revoked identity cannot open new sessions. Destination policy rejects non-public targets after resolution.

### Headless EC2 Client

`MobileEgressClient` is a LocalSystem service installed under `C:\Program Files\MobileEgress`; state is under ACL-protected `C:\ProgramData\MobileEgress\Client`. It generates and retains:

- its P-256 Client private key and CSR;
- a durable X25519 sealed-configuration private key; and
- its authenticated proxy username and password after decrypting the Owner-supplied configuration.

This role remains Windows Server 2019-only. Both same-version desktop controllers embed the same raw Windows Client manifest; no Mac Client service is included.

Bootstrap output contains only the CSR and X25519 public key. The service binds SOCKS5 to `127.0.0.1:1080` and an HTTP forward/CONNECT proxy to `127.0.0.1:1081`, so an EC2 application must explicitly opt in. Both listeners use the same retained credentials and relay session. Ordinary HTTP requests are rewritten to origin form and carried through a relay stream to the destination; repeat requests to the same destination can reuse that stream through a bounded keep-alive pool. The pool retains at most two idle streams for 15 seconds, leaving capacity for other destinations while avoiding a full mobile connection setup for every request. HTTPS clients establish end-to-end TLS through CONNECT, and Mobile Egress does not decrypt that traffic. Proxy credentials and hop-by-hop proxy headers are removed before an ordinary HTTP request reaches the destination. The Client reconnects outbound over HTTPS/WSS and needs no inbound rule or public IP.

### Android Agent

The Android app stores its P-256 identity in Android Keystore and encrypted app storage. A foreground service requests a cellular `Network` and uses that network's socket factory for the relay and every target socket. Loss of cellular closes streams; Wi-Fi is never used as fallback. Its guided IP-rotation state machine may close the relay, query ipify IPv4/IPv6 endpoints through that same cellular network, open the system Airplane Mode settings for manual toggling, observe radio loss/return, and reconnect. No relay protocol or default-route behavior changes.

Admission is capped at 32 streams. Inbound and outbound queues are bounded; outbound data scheduling is round-robin across ready streams so one stream cannot monopolize the Agent.

## Provisioning sequence

1. The controller independently models Tailscale as absent, installed/offline, or online. Windows uses verified MSI installation plus browser/unattended setup. macOS verifies the official app/PKG, opens Apple Installer when needed, guides system-extension/VPN approval and browser login, then obtains the stable Funnel FQDN.
2. Windows generates the Owner key/CSR and initializes the elevated relay as before. macOS first registers the LaunchDaemon. If Login Items approval is pending, setup returns without generating an Owner key. An ordinary status poll must prove the exact helper `enabled`; a later explicit Setup invocation generates the Keychain Owner identity and initializes relay state.
3. For each EC2 node, the controller verifies or safely prepares SSM IAM access. It never replaces an existing instance profile. It allows a 30-second passive Agent credential refresh first; if SSM remains unavailable, an explicit operator-confirmed recovery may reboot only that selected instance. The controller then requires a post-request SSM ping before provisioning, so a stale online record cannot trigger Client installation.
4. The controller durably reserves one of its ten managed-node slots before remote provisioning begins.
5. The signed controller validates node-release manifest v2, including the bounded self-signed Code Signing certificate and exact fingerprints. SSM pins the artifact hash and exact pre-trust signer bytes. Fresh Windows Server 2019 may report that already-pinned self-signed signature as `UnknownError`; that status is accepted only at this pre-trust checkpoint after both pins match. SSM then adds only the embedded public certificate to the node Root/TrustedPublisher stores when absent, requires post-trust Authenticode `Valid`, and installs the Client. Attempt-added trust is rolled back on later failure; existing exact trust is idempotent. The node returns only a CSR and X25519 public key.
6. The Owner calls the relay's direct Client-CSR endpoint.
7. The controller generates SOCKS credentials and commits encrypted `configuring` metadata before it sends anything secret-bearing to the node. It seals the endpoint/certificates/credentials to the node key using ephemeral X25519, HKDF-SHA256, and AES-256-GCM, and sends only the envelope through SSM.
8. The node rejects malformed, tampered, replayed, or wrong-key envelopes, persists the configuration, restarts its service, and starts both loopback proxy listeners atomically.
9. After that restart succeeds, the controller marks the node `installed`. Ambiguous failures retain enough encrypted metadata for **Repair** to reapply the exact same generation safely. The controller is single-instance, and an operator can explicitly cancel an abandoned pre-metadata reservation when its EC2 instance is no longer recoverable.

## Endpoint migration

When Tailscale reports a different Funnel FQDN, the controller requires AWS connectivity first if nodes are managed. Windows uses UAC; macOS requires exact helper proof and authenticated relay-admin IPC. Both rotate only the relay leaf key/certificate and stored URL under the existing CA, then update the encrypted Owner endpoint. For each node the controller first persists the desired endpoint/generation as `configuring`, then pushes the newly sealed endpoint-only configuration and marks it `installed` after restart. A failed node therefore remains repairable at the new endpoint. The controller then displays a versioned `agent-endpoint-migration` QR. The existing Agent authenticates to the new endpoint with its current certificate, consumes the one-use capability, and updates only `relayOrigin`; its key alias and certificate remain unchanged. Mac update/repair preserves CA, identities, approval, and replay state and may briefly restart the launchd helper; it does not unregister/reregister merely to upgrade.

## Availability and trust

The operator computer, Tailscale/Funnel, phone, cellular service, relay, and selected EC2 Client must all be available. A Mac additionally requires the controlling administrator to remain logged in with Keychain/per-user Tailscale available; controller quit does not erase the root daemon, but logout removes supported availability. Failure closes or prevents streams; it does not reroute an application through a different egress. Tailscale controls reachability while the relay CA and mTLS identities remain the Mobile Egress authorization boundary.
