# Security model

## Boundaries

- Tailscale Funnel makes the local relay reachable but does not authorize Mobile Egress roles. Relay-issued mTLS certificates do.
- The controller user is the Owner. Its private key and AWS fallback credentials are encrypted with Windows DPAPI for that user.
- The elevated helper receives only public CSR/endpoint/result files and exposes a narrow setup/rotation command surface.
- Relay CA/state is protected by Windows ACLs for SYSTEM and local Administrators. Local Administrators remain trusted and can access process/service state.
- Every EC2 node is a separate Client identity. Its private keys remain in ACL-protected node state.
- Android private keys remain non-exportable in Android Keystore. Enrollment/migration uses a cellular-bound network.

## Secret handling

| Material | Created/stored | May cross SSM? |
|---|---|---|
| Relay CA/leaf private keys | Relay ProgramData state | No |
| Owner private key | Controller process / DPAPI store | No |
| Node Client private key | EC2 node ProgramData | No |
| Node X25519 private key | EC2 node ProgramData | No |
| Android private key | Android Keystore | No |
| SOCKS username/password | Controller encrypted metadata and sealed node config | Ciphertext only |
| CSR and X25519 public key | Node bootstrap output | Yes; public only |
| Certificates/CA/endpoint | Controller then sealed config | Ciphertext in SSM by policy, despite being public identity material |
| Enrollment/migration capability | In-memory QR; hash in relay DB | No |

SSM command text contains signed release metadata or a base64 wrapper around the sealed envelope. Command output is constrained to public bootstrap JSON or fixed redacted success JSON. Errors exposed by the controller are finite and do not concatenate SSM stdout/stderr.

## AWS safeguards

The controller is hard-coded to `us-east-1`, inventories only running x86-64 Windows Server 2019, and manages at most ten nodes. It never calls EC2 creation/termination, public-address allocation, or security-group ingress APIs.

It never replaces an existing instance profile. For a profile-less instance it rechecks absence before association. A deterministic dedicated profile with an unexpected role is rejected. For an existing role it requires explicit operator confirmation and attaches only `AmazonSSMManagedInstanceCore`.

IAM Identity Center uses the browser/device authorization flow; the AWS password is never entered into Mobile Egress. Access keys are a fallback and are encrypted with DPAPI, but short-lived role credentials are preferred.

## Network safeguards

The relay and all SOCKS listeners bind loopback. Only Funnel publishes port 8443, which is TLS relay traffic—not SOCKS. EC2 needs outbound HTTPS/SSM only. Applications opt in individually; the product does not alter the OS default route.

Android requests a cellular transport and creates relay/target sockets from that `Network`. Loss/unavailability fails closed. Destination policy is enforced in both relay and Agent. DNS names, target IPs, URLs, headers, payloads, and credentials are excluded from diagnostics.

## Signing and supply chain

The controller downloads Tailscale only from the official stable package origin, checks the companion SHA-256, and requires a valid Tailscale signer before UAC install. Mobile Egress helper/relay/Client binaries require a valid Authenticode signature whose subject contains `Mobile Egress`; node releases also require the exact manifest hash and GitHub HTTPS release URL.

Protect the code-signing private key separately from build outputs. A compromised signer is a full update-path incident. Unsigned developer binaries intentionally cannot perform production service setup.

## Explicit non-goals

This is not an anonymity service, VPN/default-route replacement, high-availability proxy, bulk traffic platform, endpoint security product, or defense against a compromised local Administrator/root user. Tailscale Funnel is a public ingress with service limits. Carrier IP rotation is not guaranteed.
