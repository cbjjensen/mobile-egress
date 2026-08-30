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
| Release publisher certificate DER/fingerprints | Signed controller manifest | Yes; public only |
| Certificates/CA/endpoint | Controller then sealed config | Ciphertext in SSM by policy, despite being public identity material |
| Enrollment/migration capability | In-memory QR; hash in relay DB | No |

SSM command text contains signed release metadata or a base64 wrapper around the sealed envelope. Command output is constrained to public bootstrap JSON or fixed redacted success JSON. Errors exposed by the controller are finite and do not concatenate SSM stdout/stderr.

## AWS safeguards

The controller is hard-coded to `us-east-1`, inventories only running x86-64 Windows Server 2019, and manages at most ten nodes. It never calls EC2 creation/termination, public-address allocation, or security-group ingress APIs.

It never replaces an existing instance profile. For a profile-less instance it rechecks absence before association. Deterministic dedicated roles/profiles are tagged to the instance and reused only when their tags, exact EC2 trust policy, role membership, and absence of unexpected managed/inline policies all match. For an existing role it requires explicit operator confirmation and attaches only `AmazonSSMManagedInstanceCore`.

IAM Identity Center uses the browser/device authorization flow; the AWS password is never entered into Mobile Egress. Access keys are a fallback and are encrypted with DPAPI, but short-lived role credentials are preferred.

## Network safeguards

The relay and all SOCKS listeners bind loopback. Only Funnel publishes port 8443, which is TLS relay traffic—not SOCKS. EC2 needs outbound HTTPS/SSM only. Applications opt in individually; the product does not alter the OS default route.

Android requests a cellular transport and creates relay/target sockets from that `Network`. Loss/unavailability fails closed. Destination policy is enforced in both relay and Agent. DNS names, target IPs, URLs, headers, payloads, and credentials are excluded from diagnostics.

## Signing and supply chain

The controller downloads Tailscale only from the official stable package origin, checks the companion SHA-256, and requires a valid Tailscale signer before UAC install. Mobile Egress uses one self-signed local publisher certificate. Before first trust, friends obtain its SHA-256 fingerprint through a separate trusted channel and are encouraged to inspect the exact setup's Windows signature through **Properties → Digital Signatures** or trusted system Windows PowerShell. Setup displays the embedded fingerprint as a reminder and requires explicit **Yes** confirmation. Genuine setup holds its own executable open against write/delete/replacement while checking its exact Authenticode certificate, confirming, hashing, and waiting for the elevated child; the child repeats the request-bound digest and exact signature checks before adding trust. The parent reads the nonce/digest-bound redacted result after every completed child and launches only when both child exit code is zero and the result reports success. Local helper and relay siblings must have the same valid signer thumbprint as the running signed controller/admin helper.

The signed controller embeds node-release manifest v2: the exact GitHub HTTPS URL, artifact SHA-256, signer SHA-1 thumbprint, signer-certificate SHA-256, and bounded public DER base64 read from the tracked publisher CER at build time. Before it constructs an SSM command, the controller decodes and parses that DER and requires the fingerprint pair, cryptographic self-signature, Code Signing EKU, explicit CA=false constraint, current validity, and existing release metadata rules to agree.

On EC2, SSM first downloads the pinned artifact and verifies its SHA-256. It reconstructs only the manifest-embedded public DER, rechecks its SHA-256/thumbprint, and inspects the artifact's untrusted Authenticode signature. Unsigned, hash-mismatched, non-exact-certificate, or unexpected trust-status artifacts fail before trust changes. Only then may the exact certificate be added to `LocalMachine\Root` and `LocalMachine\TrustedPublisher`; Authenticode must subsequently be `Valid` with the same certificate bytes. Existing exact entries are idempotent. A failed attempt removes only exact entries that attempt added and never removes pre-existing or unrelated certificates.

Protect the code-signing private key separately from build outputs. A compromised signer is a full update-path incident. Unsigned developer binaries intentionally cannot perform production service setup.

## Explicit non-goals

This is not an anonymity service, VPN/default-route replacement, high-availability proxy, bulk traffic platform, endpoint security product, or defense against a compromised local Administrator/root user. Same-Windows-user malware and a local path race between optional external signature inspection and launch are also outside the setup threat model. Tailscale Funnel is a public ingress with service limits. Carrier IP rotation is not guaranteed.
