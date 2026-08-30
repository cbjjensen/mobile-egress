# Local Funnel Shared Bridge Design

## Goal

Each operator runs one Mobile Egress relay on their local Windows computer and exposes only that relay through Tailscale Funnel. Up to ten Windows Server 2019 EC2 workload nodes connect as independently authenticated Clients. Applications on each workload opt in through an authenticated SOCKS5 listener on `127.0.0.1:1080`; the paired Android Agent supplies cellular egress.

No relay EC2, inbound EC2 rule, Elastic IP, router change, or local port-forward is required. The local computer and Android phone remain availability dependencies. The design targets light, personal, interruption-tolerant traffic.

## Topology

```text
EC2 workload -> loopback SOCKS -> MobileEgressClient --\
                                                     +-> Tailscale Funnel -> local MobileEgressRelay -> Android Agent -> cellular Internet
EC2 workload -> loopback SOCKS -> MobileEgressClient --/
```

The local relay listens only on `127.0.0.1:8443`. Tailscale raw TCP Funnel publishes a stable `*.ts.net:8443` origin and forwards TLS bytes without terminating Mobile Egress mTLS. Android and EC2 Clients use that public origin. The local Owner controller uses a loopback dial override while retaining the public origin for TLS server-name and identity trust checks.

## Local Windows Responsibilities

The Wails desktop application remains the Owner/controller. One UAC-approved setup installs:

- the official Tailscale amd64 MSI after checksum and Authenticode verification;
- Tailscale unattended mode and background raw TCP Funnel forwarding to `127.0.0.1:8443`; and
- `MobileEgressRelay` as a LocalSystem Windows service with ACL-protected state under ProgramData.

The application generates the Owner private key locally, sends only a CSR to relay initialization, validates the returned certificate against the created key and CA, and stores the complete Owner identity with current-user DPAPI. No Owner bearer invitation crosses process, cloud, or logging boundaries in this path.

Tailscale login and Funnel enablement open the system browser for the operator's approval. The application reports Funnel beta/bandwidth limitations and never claims an availability or throughput SLA.

## EC2 Client Nodes

The controller supports IAM Identity Center browser authentication and DPAPI-protected access-key fallback. In `us-east-1`, it lists running x86-64 Windows Server 2019 instances. Nodes need SSM reachability and outbound HTTPS, but no public address or inbound security-group rule.

When no instance profile exists, the app may create and attach its dedicated SSM profile. When an existing role lacks Systems Manager access, the app displays the exact role and requires explicit confirmation before adding `AmazonSSMManagedInstanceCore`; it never replaces an existing profile.

SSM installs a signed `MobileEgressClient` release as a LocalSystem service. The node generates its own ECDSA Client key/CSR, SOCKS credentials, and durable X25519 configuration key. SSM returns only the CSR and X25519 public key. The Owner provisions the CSR directly, seals the resulting public identity, relay origin, and SOCKS credentials with ephemeral X25519 + HKDF-SHA256 + AES-256-GCM, and sends only ciphertext through SSM. The node rejects malformed, tampered, replayed, or wrong-key envelopes and stores its secrets under System/Administrators-only ACLs.

Each node has a distinct Client serial and revocation boundary. The controller retains DPAPI-protected node metadata and credentials so it can display a copy-on-demand proxy line, repair configuration, update the service, and revoke or replace the node identity.

## Relay and Protocol Changes

The relay adds:

- `bootstrap-owner`, a one-time CSR-based initialization command;
- `rotate-endpoint`, which atomically reissues only the relay server certificate under the existing CA;
- Owner-authenticated direct Client CSR provisioning;
- Owner issue and Agent consume endpoint-migration endpoints; and
- a Windows Service Control Manager execution path distinct from foreground `serve`.

The relay retains one active Agent session and multiple unique Client sessions. It admits at most four active streams per Client and thirty-two across the Agent. The controller manages at most ten EC2 Client nodes. Existing identity revocation, one-session-per-serial, destination policy, queue bounds, and fail-closed protocol validation remain in force.

An endpoint-migration QR is a separate, one-use, short-lived payload containing the new HTTPS origin, unchanged CA, capability, and expiry. Android accepts it only when already enrolled, the CA equals its stored CA, the destination is a strict HTTPS origin, and the new relay confirms the capability over mTLS using the existing Agent identity. It updates only the stored origin; the private key and certificate are unchanged.

## Availability and Recovery

The relay and Tailscale run as persistent Windows services. A PC shutdown, sleep, loss of Internet, Tailscale outage, or phone cellular loss makes new streams fail closed. Existing EC2 applications retain their local SOCKS configuration and reconnect when services recover.

If the Funnel FQDN changes, the controller rotates the relay endpoint, pushes sealed endpoint updates to managed EC2 nodes over SSM, and displays an Android migration QR. It does not silently destroy relay state or re-pair devices.

## Security Boundaries

- Funnel exposes only Mobile Egress TLS on port 8443; it never exposes SOCKS.
- SOCKS remains authenticated and IPv4 loopback-only on every Client node.
- Owner authority remains only on the local controller; EC2 nodes receive Client identities only.
- Private keys, SOCKS credentials, capabilities, payloads, destinations, and raw sealed configuration are excluded from logs and diagnostics.
- DPAPI protects controller secrets from other ordinary Windows users, not same-user malware or administrators.
- ProgramData ACLs protect LocalSystem service state, not a compromised EC2 or local administrator.
- Tailscale and AWS are control/connectivity dependencies; Mobile Egress mTLS remains the application authentication boundary.

## Acceptance

Automated tests cover direct CSR bootstrap/provisioning, sealed configuration, multi-Client routing and limits, endpoint rotation/migration, Windows service behavior, AWS/SSM safeguards, and Android queue/migration behavior. Physical acceptance uses a local Windows computer, Tailscale Funnel, one Android phone, and at least two SSM-managed Windows Server 2019 EC2 nodes, including reboot recovery and endpoint migration.
