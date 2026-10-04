# Windows Client installation and repair

The 2.x Windows product is `MobileEgressClientSetup.exe`, containing only the signed Client app, Client service, and public signer metadata. It uses the established publisher identity and timestamp checks. The installer verifies the exact payload before elevated installation; private signing material is never embedded.

Run the installer normally, confirm the expected publisher, and approve the standard Windows elevation request. Do not bypass a signer mismatch. The installer transaction locks its confirmed bytes, verifies payload signatures, stages files under restricted access, and rolls back files/trust changes after failure.

## Stable installation identities

- App/service installation: `C:\Program Files\Mobile Egress Client`.
- Windows service: `MobileEgressClient`, running as LocalSystem.
- New protected state: `C:\ProgramData\MobileEgressClient`.
- Existing owner SID and service-account DPAPI data remain protected during repair/upgrade.
- Local app/service communication uses the protected named-pipe boundary; no public administration port exists.

Installation opens the Client app unelevated. New installations enter the shared setup wizard: Computer address, Network access, Pair phone, Verify connection and Use your proxy. Address discovery is an optional setup aid, not proof of reachability; saved/edited values are preserved. Existing paired installations open their dashboard, with Review setup available. Finish later preserves unfinished setup.

The default Agent TLS listener is TCP 8443. Its host-firewall rule is scoped to the exact Client executable, service and selected listener port. An unrelated rule with the same name is not adopted. Check/Retry firewall actions operate on the saved listener without regenerating pairing or endpoint state. Router forwarding and cloud ingress remain manual, including the additional EC2 security-group rule described in the [server networking guide](standalone-clients.md#hosted-servers-and-aws-ec2). Local HTTP 1081 and SOCKS 1080 stay on `127.0.0.2`.

## Migrating an AWS-installed 1.x Client

Run the signed 2.x installer locally on that workload. The recognized legacy service points to `C:\Program Files\MobileEgress\mobile-egress-client.exe` with protected state at `C:\ProgramData\MobileEgress\Client`. Migration verifies the exact known command/account, changes the service executable transactionally, and continues using that protected state directory. It does not decrypt/copy DPAPI state to a different account.

Unknown commands, extra arguments, or unexpected service accounts fail closed with recovery guidance. Repair must not take over another service. The app displays **Migration required** until fresh direct pairing completes. Existing proxy credentials and usable stable Client IDs are retained, but relay-issued credentials cannot authorize direct serving.

## Failures

Occupied proxy ports produce an actionable error and do not silently move. Local management remains available while service startup retries, so a blocked public listener can be reconfigured. Free a conflicting local proxy port before retrying. Unavailable secure storage requires repair under the original ownership; do not delete protected state to suppress the error.

Former controller computers follow [separate retirement instructions](controller-retirement.md). This installer does not automatically convert one into a workload endpoint.
