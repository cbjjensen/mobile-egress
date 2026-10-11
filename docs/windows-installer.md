# Windows Client installation and repair

The Windows installer is `InevitableMobileRelaySetup.exe` starting at 2.0.2; frozen 2.0.0/2.0.1 packages keep `MobileEgressClientSetup.exe`. It contains only the signed Client app, Client service, and public signer metadata, using the established publisher identity and timestamp checks. The installer verifies the exact payload before elevated installation; private signing material is never embedded.

Run the installer normally, confirm the expected publisher, and approve the standard Windows elevation request. Do not bypass a signer mismatch. The installer transaction locks its confirmed bytes, verifies payload signatures, stages files under restricted access, and rolls back files/trust changes after failure.

Windows 2.0.7 supports interactive Administrator desktops that already
run with full privileges, including Windows Server 2019/2022 installations using
the built-in Administrator without Admin Approval Mode. A normal double-click
can be elevated on these desktops even when **Run as administrator** is unchecked.
Frozen 2.0.6 still rejects that case. Version 2.0.7 distinguishes an unsplit
desktop token from an explicitly elevated split token; the latter must still be
restarted from the normal desktop. Service accounts and session zero are rejected.
No UAC policy, account permissions, publisher verification or install transaction
is changed. The fix is published as a pilot; installation on the reported Server 2022 host remains unverified. See the [release evidence](superpowers/plans/2026-10-10-windows-server-elevation.md).

## Diagnosing an installation failure

For the generic `install_failed` dialog in 2.0.7, the publisher-trust wording does
not identify the failed step. The same dialog covers child signature inspection,
payload staging, trust, file/shortcut installation and service registration.
WebView2 preparation and application launch happen after this stage. Setup rolls
back changes and removes its request/result exchange, so missing files or trust
entries afterward do not establish the original cause.

An [unpublished source correction](superpowers/plans/2026-10-11-windows-powershell-signature-status.md)
addresses Windows PowerShell 5.1 labeling the native untrusted-root condition
`UnknownError`. Published 2.0.7 can reject that label before adding confirmed
publisher trust. The correction still requires the precise native untrusted-root
code, exact timestamped signer and successful post-trust verification; a generic
`UnknownError` alone is never sufficient to accept an installer.

Use the read-only [setup diagnostic](../scripts/diagnose-windows-client-setup.ps1)
on the affected server, passing its downloaded installer:

```powershell
& .\diagnose-windows-client-setup.ps1 -InstallerPath "$env:USERPROFILE\Downloads\InevitableMobileRelaySetup.exe"
```

Send the resulting JSON to the publisher. It compares the exact frozen 2.0.7
bytes and reports signature, trust-store access, OS build and scoped service
failure facts without account/machine names, paths, raw event messages or keys.
Unavailable queries remain unknown. It does not run setup, import certificates,
change services or alter security settings. Windows signature verification may
consult network services under the machine's existing policy. An absent
`TrustedPublisher` store is a diagnostic lead, not proof of this reported cause.

## Stable installation identities

- App/service installation: `C:\Program Files\Mobile Egress Client`.
- Windows service: `MobileEgressClient`, running as LocalSystem.
- New protected state: `C:\ProgramData\MobileEgressClient`.
- Existing owner SID and service-account DPAPI data remain protected during repair/upgrade.
- Local app/service communication uses the protected named-pipe boundary; no public administration port exists.

The visible app, service display name and Start-menu shortcut use **Inevitable Mobile Relay**. An upgrade replaces only a verified old shortcut pointing at this installation, preserving its original copy for rollback if installation fails. Unknown shortcut targets are rejected. The internal installation directory and service ID above do not change; existing pairing and DPAPI data remain in place.

Installation opens the Client app using the setup parent's desktop token: normally unelevated, or with the existing privileges of an unsplit Administrator desktop from Windows 2.0.7. New installations default to Inevitable Gateway: activate in your browser, pair your phone, verify its live connection, then copy proxy details. Hosted mode uses outbound TCP 443 and opens no workload listener or firewall rule. Advanced direct retains Computer address and Network access; discovery is only an unverified suggestion. Existing direct configurations remain direct, and paired installations open their dashboard with Review setup available. Finish later preserves configuration and pairing.

The default Agent TLS listener is TCP 8443. Its host-firewall rule is scoped to the exact Client executable, service and selected listener port. An unrelated rule with the same name is not adopted. Check/Retry firewall actions operate on the saved listener without regenerating pairing or endpoint state. Router forwarding and cloud ingress remain manual, including the additional EC2 security-group rule described in the [server networking guide](standalone-clients.md#hosted-servers-and-aws-ec2). Local HTTP 1081 and SOCKS 1080 stay on `127.0.0.2`.

## Migrating an AWS-installed 1.x Client

Run the signed 2.x installer locally on that workload. The recognized legacy service points to `C:\Program Files\MobileEgress\mobile-egress-client.exe` with protected state at `C:\ProgramData\MobileEgress\Client`. Migration verifies the exact known command/account, changes the service executable transactionally, and continues using that protected state directory. It does not decrypt/copy DPAPI state to a different account.

Unknown commands, extra arguments, or unexpected service accounts fail closed with recovery guidance. Repair must not take over another service. The app displays **Migration required** until fresh direct pairing completes. Existing proxy credentials and usable stable Client IDs are retained, but relay-issued credentials cannot authorize direct serving.

## Failures

Occupied proxy ports produce an actionable error and do not silently move. Local management remains available while service startup retries, so a blocked public listener can be reconfigured. Free a conflicting local proxy port before retrying. Unavailable secure storage requires repair under the original ownership; do not delete protected state to suppress the error.

Former controller computers follow [separate retirement instructions](controller-retirement.md). This installer does not automatically convert one into a workload endpoint.
