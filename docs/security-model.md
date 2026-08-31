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
| Proxy username/password | Controller encrypted metadata and sealed node config | Ciphertext only |
| CSR and X25519 public key | Node bootstrap output | Yes; public only |
| Release publisher certificate DER/fingerprints | Signed controller manifest | Yes; public only |
| Certificates/CA/endpoint | Controller then sealed config | Ciphertext in SSM by policy, despite being public identity material |
| Enrollment/migration capability | In-memory QR; hash in relay DB | No |

SSM command text contains signed release metadata or a base64 wrapper around the sealed envelope. Command output is constrained to public bootstrap JSON or fixed redacted success JSON. Errors exposed by the controller are finite: release failures may preserve only an allowlisted stage identifier and never concatenate SSM stdout/stderr.

## AWS safeguards

The controller is hard-coded to `us-east-1`, inventories only running x86-64 Windows Server 2019, and manages at most ten nodes. It never calls EC2 creation/termination, public-address allocation, or security-group ingress APIs. SSM recovery can call `ec2:RebootInstances` only for a supported instance already present in the refreshed inventory and only after an explicit interruption confirmation. The controller waits for a post-request SSM ping before installing; cancelling makes no AWS change.

It never replaces an existing instance profile. For a profile-less instance it rechecks absence before association. Deterministic dedicated roles/profiles are tagged to the instance and reused only when their tags, exact EC2 trust policy, role membership, and absence of unexpected managed/inline policies all match. For an existing role it requires explicit operator confirmation and attaches only `AmazonSSMManagedInstanceCore`.

IAM Identity Center uses the browser/device authorization flow; the AWS password is never entered into Mobile Egress. Access keys are a fallback and are encrypted with DPAPI, but short-lived role credentials are preferred.

## Network safeguards

The relay and all proxy listeners bind loopback. Each EC2 Client exposes authenticated SOCKS5 on `127.0.0.1:1080` and authenticated HTTP forward/CONNECT on `127.0.0.1:1081`; neither is reachable through an EC2 security group. Only Funnel publishes port 8443, which is Mobile Egress TLS relay traffic—not a public proxy. EC2 needs outbound HTTPS/SSM only. Applications opt in individually; the product does not alter the OS default route. HTTPS tunneled through CONNECT remains end-to-end encrypted between the EC2 application and destination. Ordinary HTTP remains plaintext on the cellular-to-destination leg, as it would without Mobile Egress; the Client strips proxy authentication and hop-by-hop proxy headers before forwarding it.

Android requests a cellular transport and creates relay/target sockets from that `Network`. Loss/unavailability fails closed. Destination policy is enforced in both relay and Agent. DNS names, target IPs, URLs, headers, payloads, and credentials are excluded from diagnostics.

Guided IP rotation queries `api.ipify.org` and `api6.ipify.org` through the selected cellular network, including network-specific DNS. ipify therefore observes the public source address and normal request metadata. Before/after addresses exist only in transient runtime/UI state and are excluded from storage, application logs, copied diagnostics, and physical-acceptance records. Failure of one address family is isolated, and failure of both produces an unverified result rather than a false success. The app opens system settings but has no privileged ability to change Airplane Mode, mobile data, APNs, or SIM state.

## Signing and supply chain

The controller downloads Tailscale only from the official stable package origin, checks the companion SHA-256, and requires a valid Tailscale signer before UAC install. Its internally generated MSI staging path is passed through a dedicated child-process environment value rather than appended to PowerShell command text; ambient values for that name and incompatible inherited PowerShell module paths are removed first. The desktop exposes only the bounded failing installation stage. Mobile Egress uses one self-signed local publisher certificate. Before first trust, friends obtain its SHA-256 fingerprint through a separate trusted channel and may independently inspect the exact setup's Windows signature through **Properties → Digital Signatures** or trusted system Windows PowerShell. That inspection is optional under the approved convenience model; direct double-click setup remains supported and no external verifier or launcher is required. Friends should use only the official GitHub Releases source plus the separately shared fingerprint. Malicious substitution of the download source before first trust is outside this relaxed boundary. Setup displays the embedded fingerprint as a reminder and requires explicit **Yes** confirmation. Genuine setup holds its own executable open against write/delete/replacement while checking its exact Authenticode certificate, confirming, hashing, and waiting indefinitely for actual elevated-child completion; the child repeats the request-bound digest and exact signature checks before adding trust. The parent reads the nonce/digest-bound redacted result after every completed child and launches only when both child exit code is zero and the result reports success. Local helper and relay siblings must have the same valid signer thumbprint as the running signed controller/admin helper.

The elevated setup child acquires a fixed bounded `Global\MobileEgressSetupTransaction` mutex before any trust mutation and holds it through publisher trust, exact signed-sibling verification, installation, and any trust/install rollback. Lock timeout fails closed before trust changes, abandoned ownership is accepted, and every success or failure path releases and closes the mutex. After every staged executable passes exact Authenticode verification, setup enumerates same-named processes and terminates only the controller whose full executable path equals the fixed installed path, waiting for exit before changing the installation. An inspection, termination, or wait failure aborts before backup or promotion. Existing install files and the Start Menu shortcut then move into a SYSTEM/Administrators-only recovery directory before promotion. That backup is deleted only after successful promotion or successful rollback; failed restoration preserves it, propagates a typed rollback failure, and emits only redacted recovery guidance to the unelevated parent.

The signed controller embeds node-release manifest v2: the exact GitHub HTTPS URL, artifact SHA-256, signer SHA-1 thumbprint, signer-certificate SHA-256, and bounded public DER base64 read from the tracked publisher CER at build time. Before it constructs an SSM command, the controller decodes and parses that DER and requires the fingerprint pair, cryptographic self-signature, Code Signing EKU, explicit CA=false constraint, current validity, and existing release metadata rules to agree.

On EC2, a bounded machine-global mutex serializes the complete download/trust/install-or-update transaction; abandoned ownership is recovered, while lock timeout fails closed before the transaction starts. SSM first downloads the pinned artifact and verifies its SHA-256. It reconstructs only the manifest-embedded public DER, rechecks its SHA-256/thumbprint, and inspects the artifact's untrusted Authenticode signature. Fresh Windows Server 2019 may classify an otherwise exact self-signed pre-trust signature as `UnknownError`; only this pre-trust status is permitted after both the artifact hash and exact signer bytes match. Unsigned, hash-mismatched, non-exact-certificate, or every other unexpected status fails before trust changes. Every same-thumbprint store entry is inspected before an exact certificate is accepted. Only then may the exact certificate be added to `LocalMachine\Root` and `LocalMachine\TrustedPublisher`; Authenticode must subsequently be `Valid` with the same certificate bytes. Existing exact entries are idempotent. A failed attempt removes exact DER only from stores absent when that mutex-owned transaction began and never removes pre-existing or unrelated certificates. ACL, PowerShell service-management, and Client bootstrap failures are checked immediately so they enter rollback and cannot emit success.

Protect the code-signing private key separately from build outputs. A compromised signer is a full update-path incident. Unsigned developer binaries intentionally cannot perform production service setup.

## Explicit non-goals

This is not an anonymity service, VPN/default-route replacement, high-availability proxy, bulk traffic platform, endpoint security product, or defense against a compromised local Administrator/root user. Same-Windows-user malware, malicious download-source substitution before the publisher is first trusted, and a local path race between optional external signature inspection and launch are also outside the relaxed setup threat model. Tailscale Funnel is a public ingress with service limits. Carrier IP rotation is not guaranteed.
