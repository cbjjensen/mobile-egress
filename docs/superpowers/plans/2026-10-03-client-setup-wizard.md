# Guided Windows and Mac Setup Implementation Plan

Approved 2026-10-03. Implement on `main`, preserving existing work, installation identities, protected credentials and pairing. This work does not publish a release.

## Goal and approved requirements

Make installation lead into one shared setup wizard on Windows and Apple Silicon Mac:

**Install → configure address → allow connections → pair phone → verify connection → copy proxy details.**

The wizard automates local setup. Router forwarding, AWS security groups and other provider firewalls remain owner-managed. No AWS credentials, cloud-management APIs, router changes or UPnP are introduced.

### Shared setup wizard

- Replace the initial endpoint form with Computer address, Network access, Pair phone, Verify connection and Use your proxy steps.
- Suggest the computer name and public address; default local/public ports to 8443. Keep hostname/IP editable and advanced bind/public port settings available.
- Reuse existing configuration, invitation, cancellation, status and proxy-copy operations. Keep endpoint updates and revocation in the normal dashboard.
- Resume from service configuration and pairing state without discarding configuration, replacing invitations or duplicating pairing. Existing paired installations open the dashboard even when offline; Review setup does not reset pairing.
- Wait up to 30 seconds for service readiness, then offer Retry/Repair guidance. Distinguish Installed, Listening, Paired and Connected.
- Verify only after an authenticated live phone session; discovery, firewall success and a bound listener never prove external reachability.
- Finish later preserves unfinished setup. Clear displayed invitations when expired, cancelled or invalidated by configuration changes.

### Automatic address suggestion

- Unprivileged desktop discovery queries the existing ipify IPv4 and IPv6 providers concurrently and prefers IPv4. Use a five-second overall timeout, bounded responses, no redirects, public-address validation and correct IPv6 formatting.
- Discover once on the initial address step, with explicit Retry. Explain the lookup service sees the outgoing public IP; label the result Suggested address—not yet verified.
- VPN/NAT/CGNAT can make the observed address unsuitable for incoming connections. Never overwrite edited/saved settings; manual entry remains available after failure.
- Send no Client identity, capabilities or credentials. Normal operation never depends on discovery.

### Installer handoff and local firewall

- Preserve Windows unelevated launch and its executable/service/port-scoped firewall rule.
- After Mac installation/daemon startup, open the app only for the saved owner when that owner is the active GUI user. Enter the GUI session and explicitly drop UID; launchctl asuser alone is insufficient.
- Mac launch failure is nonfatal with an Applications-folder fallback. Preserve logged-in-owner requirements for new installs and skip GUI on headless upgrade/repair. Reject root GUI execution; add no login agent or daemon boot GUI launch.
- Replace the Mac firewall no-op with socketfilterfw through protected service IPC. Validate the installed daemon, add/unblock only that executable and read back results.
- Mac exceptions are application-scoped. Preserve global firewall/block-all/managed/PF/unrelated settings.
- Return structured allowed, disabled, blocked, unavailable or unknown states. Separate Check/Retry from configuration so retries preserve invitation/configuration generations.
- Add local setup information, discovery and firewall desktop bindings; no public administration/probe endpoints.

### Network access guidance

- Home/router (default), AWS EC2 and Other hosted server selections change instructions only. Show actual local/public ports and relevant LAN addresses; keep 1080/1081 private.
- AWS requires inbound Custom TCP on the Client listener port (normally 8443), the host firewall, a public address and internet-gateway routing. The source must include the phone cellular address; AWS My IP may be the wrong computer. Explain changing cellular addresses and broader source access for this authenticated listener only.
- Failed pairing presents address, local firewall, router/cloud ingress, CGNAT and cellular checks without inventing a root cause.

### Documentation

Update README setup to: **Run the installer and follow the setup wizard.** Mobile Egress suggests your public address, configures local access where permitted and guides phone pairing; address/port remain editable.

Add: **Hosted servers, including AWS EC2, may require an additional firewall rule.** Allow inbound TCP on the configured Client listener port—8443 by default—in the provider firewall or EC2 security group. A publicly reachable address and network route are also required. The installer does not change cloud security groups or router settings. Never expose proxy ports 1080 or 1081.

Synchronize installation, Windows installer, operations and physical acceptance documentation. Preserve Android/iPhone lifecycle differences and pre-release status until signed/device gates pass.

## Work checklist and interfaces

- [x] Record approved plan and preserve the starting working tree.
- [x] Desktop setup metadata and bounded address discovery, with tests.
- [x] Shared resumable wizard and behavioral UI tests.
- [x] Structured Windows/Mac firewall checking and independent retry, with protected IPC and tests.
- [x] Mac owner-session installer handoff and root GUI guard, with tests.
- [x] README, operations, installation and acceptance documentation.
- [x] Independent code review and applicable automated gates.
- [ ] Signed Windows/Mac installation and physical Android/iPhone acceptance (release blocker until actually run).

Desktop interfaces: `SetupInfo()` returns platform, suggestedName, localAddresses, defaultBindAddress and defaultPublicPort. `DiscoverPublicAddress()` returns address, endpoint, ipv4, ipv6 and provider, or a sanitized error. `CheckFirewall()` and `RetryFirewall()` return state, scope, port and message through the protected service. Existing configuration/pairing APIs and direct phone protocol remain unchanged.

Implementation ownership: root owns desktop metadata/discovery, IPC/app bindings, documentation and integration. Independent implementers own shared frontend assets/tests, firewall implementation/tests, and Mac installer/root-GUI changes respectively.

Ruling: Use the explicitly approved main checkout rather than a worktree. Preserve the extensive pre-existing migration and bridge changes; no commit, push, install or publication is implied by this task.

## Verification

- Wizard: fresh/existing/offline configurations, interrupted/resumed setup, expired/cancelled invitation, lost ACK, endpoint changes and live connection.
- Discovery: IPv4/IPv6, malformed/private/oversized responses, redirects, timeout, unavailable provider, manual override and preserved edits; no normal-operation lookups.
- Firewall: exact executable/port targeting, port changes, Mac disabled/block-all, managed denial, timeout/read-back mismatch, and retries preserving generations/invitations.
- Installers: Windows unelevated launch; Mac correct owner/effective UID, missing GUI, wrong foreground user, failed launch, upgrade/repair ownership.
- Networking: real cellular access, blocked host/cloud ingress, missing forwarding, different public/local ports and misleading observed public IP.
- Run relevant Go, frontend, installer/release, native Mac, links and mobile-manifest checks. Signed Windows/Mac and real Android/iPhone HTTP/CONNECT/SOCKS remain explicit gates, not inferred from unit tests.

## Sources

- [ipify API](https://www.ipify.org/)
- [Apple firewall guidance](https://support.apple.com/guide/mac-help/block-connections-to-your-mac-with-a-firewall-mh34041/mac)
- [AWS security groups](https://docs.aws.amazon.com/vpc/latest/userguide/working-with-security-group-rules.html)
- [AWS internet connectivity](https://docs.aws.amazon.com/vpc/latest/userguide/VPC_Internet_Gateway.html)

## Execution and validation record

Implementation started on main at commit `04d8c4c69b49d840aaca8b86dc7c87dd88b4c054`. The starting dirty state was preserved under ignored `.local/client-setup-wizard-*` before code changes. Automated and physical results will be recorded here without treating unsigned builds as signed acceptance.

Initial targeted discovery/IPC tests passed after missing-operation red tests. Frontend tests expanded from five to 28, with behavioral red/green coverage and a browser smoke check at the native 760×760 window size. The browser check caught and corrected a global `go` function collision with Wails' `window.go`; the harness now models `window === globalThis`. Review also corrected unchanged default-443 endpoint normalization, late discovery/polling/firewall-result races and unfinished dashboard visibility.

Mac handoff fixtures execute the actual installer script with replaced privileged boundaries: saved/other owner, missing GUI, headless repair, launch failure and stalled launch/query cases passed. A real entry-point fixture proves root rejection happens before GUI startup. These do not replace a signed native installation.

Review rulings: native Mac firewall output was checked read-only and differs from the initial generic fixture; parse only known replies for the exact daemon. Windows must inspect active profiles rather than unrelated configured profiles, preserve/report overriding block rules and require the service SID needed by its service-scoped rule. Set `SERVICE_SID_TYPE_UNRESTRICTED` and explicit `SERVICE_WIN32_OWN_PROCESS` in the existing owned installer configuration before service start; preserve the complete previous configuration during rollback. This repairs the existing service/firewall contract without changing LocalSystem ownership, DPAPI storage or proxy credentials. See [Microsoft service-rule requirements](https://learn.microsoft.com/en-us/windows/security/operating-system-security/network-security/windows-firewall/configure#create-an-inbound-program-or-service-rule).

### Final automated results — 2026-10-03

| Gate | Result and scope |
|---|---|
| `scripts/test-all.ps1 -Components Windows` | PASS: mobile manifest and validator fixtures, release orchestration/direct release contracts, Go tests, Client installer contracts, Go vet/build, and frontend syntax/behavior. Release checks used test fixtures; no assets were published. |
| Final frontend regression run | PASS: all 30 Node tests and `node --check`. This run followed the full Windows gate and includes the final same-tick invitation/status race fix. |
| Native Apple Silicon Mac | PASS: `go test ./...`, `go vet ./...`, focused race tests for clientapp, macosrelease and the GUI entry point, plus unsigned service and production-tagged GUI builds targeting macOS 13. Existing file-based Keychain SDK deprecation warnings remain. |
| Independent review | PASS after fixes: active Windows profiles, required service SID, overriding firewall block rules, Mac handoff/identity, discovery/IPC, and invitation polling races reviewed. No remaining findings from the scoped re-review. |
| Browser smoke | PASS with synthetic service bindings at the native 760×760 window size. This verifies wizard layout and interactions, not installed IPC or cellular reachability. |
| Documentation | Local links and scoped whitespace checked; README and installation/operations/physical acceptance instructions synchronized. |

The final QR regression resolves the invitation and an older status request in the same event-loop turn. Status responses are now rejected during a mutation as well as after its revision changes. This prevents both immediate and delayed stale polls from clearing the new QR. The regression failed before the fix and passed afterward.

Windows evidence is retained under ignored `.local/client-setup-wizard-7b4a385703e147fbb8db208b6ab1d647/`. Native evidence is retained under ignored `.local/client-wizard-1a81e41ef6124d80abfd444e2f03629c/`, including source hashes, log and exit code 0; the transferred source archive SHA-256 is `32f2a6ce7c9caba207a7d6253c3d463c69606e2074f6d265dcf9f694d786e62d`. That native run preceded the final Windows-only firewall changes and frontend-only polling fix; native/shared Go and installer source remained unchanged. The subsequent Windows gate and final frontend run cover those follow-ups. The initial native snapshot omitted the root pairing package; the snapshot list was corrected and the complete native suite then passed. Both owned remote snapshots were removed after retaining local evidence.

### Remaining acceptance and release blockers

- Signed Windows installer and signed/notarized Mac PKG clean-install, upgrade and repair tests are **NOT RUN** for this wizard. Native unit tests and unsigned builds do not satisfy those gates.
- Physical Android/iPhone pairing and HTTP/CONNECT/SOCKS through both workload platforms are **NOT RUN**, including blocked host/cloud/router ingress, misleading outgoing-IP suggestions and differing public/local ports. At verification time this Windows PC had no installed `MobileEgressClient` service and no authorized Android device attached.
- Native installed firewall effectiveness, managed-policy denial, GUI owner handoff and persistence across install/repair require signed physical acceptance. An unprivileged Windows firewall inspection returned Access denied; no host policy was changed to bypass it.
- The [physical acceptance template](../../templates/physical-acceptance-record.md) retains NOT RUN for these checks. The pre-release notice remains; the wizard is not described as release-ready.

No protocol, platform minimum, proxy address, phone limit or approved iOS lifecycle exception changed. Implementation remains on `main`; pre-existing work and recovery patches are preserved. No installer was installed, no release was published, and this task did not commit or push changes.

### Subsequent review fixes — 2026-10-03

A further review found two P2 defects, both addressed on `main`:

- Windows owned-rule readback now checks package, interface, interface type and security constraints before returning allowed. Additional or missing constraints return unknown; query failures return unavailable. Check and Retry preserve administrator restrictions. The actual production PowerShell script is exercised through mocked OS commands: 26 restricted/missing-filter cases and two query-failure cases reproduced the false allowed result before the fix; all 30 cases, including unrestricted controls, pass afterward. Tests compare the constraints before and after Retry to prevent a fix that silently removes them.
- Retry address lookup now replaces the previous automatic suggestion only while it remains unedited and unsaved. A regression reproduced Save submitting the obsolete suggestion before the fix. The 33-test frontend suite now also covers saving the refreshed address, editing while a retry is pending, and a retry completing after the address was saved.

Both fixes passed independent scoped re-review. A fresh `scripts/test-all.ps1 -Components Windows` completed successfully: Go tests, installer contracts, vet/build, all 33 frontend tests and syntax, release contracts/orchestration, and mobile manifest/schema checks. The full log is retained in ignored `.local/client-setup-wizard-7b4a385703e147fbb8db208b6ab1d647/review-fixes-windows-gate.log`. Scoped whitespace checks also passed.

These follow-ups change Windows firewall readback and shared frontend logic only; native Mac service/installer code is unchanged, and native Mac/signing/device tests were not rerun for them. The signed-installation and physical-phone acceptance blockers above remain. No host firewall policy, installation, Git commit or published release was changed by this follow-up.
