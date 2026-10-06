# Operations and recovery

## Subscription and pilot access

Hosted access can come from a valid paid Inevitable Mobile Relay subscription or an independent approved pilot grant. Account/user suspension and computer revocation still apply. Subscription cancellation preserves the paid term; failed renewal does not extend it. Full refunds remove the affected invoice contribution and open disputes suspend paid access. These changes never delete phone pairing or Advanced direct settings, and the existing bounded gateway configuration refresh controls enforcement timing.

The Inevitable page owns subscription management, nullable public platform downloads and the complimentary iPhone pilot request. TestFlight requires Admin pilot approval independently of purchasing. Invitation automation uses a consented email and an explicitly selected compatible, unexpired externally approved build; delivery and installation are never inferred from an API response. Keep iOS open/active/unlocked and Android foreground-service behavior unchanged.

See [R2 publication](r2-downloads.md) and the synchronized [subscription plan](superpowers/plans/2026-10-05-mobile-egress-subscriptions.md). Rollback closes new sales/invitations while preserving reconciliation, existing paid/pilot grants and immutable artifacts. Production configuration and physical acceptance remain separate gates.

## Reading connection state

- **Setup needed**: complete **Your account** and **Connect this computer**, or explicitly choose Advanced direct mode, then connect a phone. Legacy installations still require fresh pairing.
- **Waiting for phone**: the phone has not established a usable connection. **Connection details** distinguishes account/gateway readiness, a direct listener and the saved phone; none alone proves cellular traffic.
- **Adding phone**: leave Inevitable Mobile Relay open on the phone while pairing finishes; do not create another code merely because acknowledgement is pending.
- **Connected**: the paired phone has an authenticated session and required connection updates are acknowledged. A target may still reject a particular request.
- **Reconnect your phone**: the saved connection changed and the phone has not acknowledged its update.
- **App unavailable / Needs attention**: use the stated storage, installation, port, trust or network recovery action. The app retains native error details under **Connection details** and opens them when a connection error first appears.

Status is deliberately secret-safe. Copy proxy credentials or invitations only through the explicit app actions. Never paste credentials, QR capabilities or private diagnostic dumps into issue reports.

## Dashboard and setup

The paired Client opens a dashboard with connection status and local proxy copy actions. An offline phone does not restart setup: follow the dashboard's phone-app Start instructions. **Phone settings** is a separate view for manual connection updates and confirmed phone removal. **Review setup** returns to the wizard for activation, connection mode, address and network guidance while preserving the existing pairing. **Finish later** keeps unfinished work visible through **Continue setup**.

The hosted setup headings are **Your account**, **Connect this computer**, **Connect phone**, **Start sharing** and **Use in your apps**. They describe customer actions; the underlying account approval, gateway attachment and authenticated phone checks are unchanged. Approved account review says **Your account is connected** without displaying the activation name field or requesting a new sign-in. **Advanced connection settings** contains mode selection, and **Connection details** retains diagnostic information rather than making protocol terms the main dashboard copy.

**Show QR code** creates or resumes a phone-pairing invitation; **Copy setup code** is its text fallback. **Copy HTTP proxy** and **Copy SOCKS5 proxy** retain their existing credential-bearing formats. Only applications configured to use these local proxies use the phone's mobile data; the Client does not redirect all computer traffic. Keep copied details private.

Pending connection updates surface **Reconnect your phone** on the dashboard. If hosted access needs reactivation, restore account access first; only then export the current connection update. A bound listener or connected gateway does not substitute for an authenticated phone session.

## Hosted connection recovery

Check Inevitable access and the workload's outbound Internet connection. Resume pending browser activation from the Client app; its proof stays in the service. Expired/denied activation requires a new request. If the one-time authorization response is lost before durable storage, reactivate the same Client. Account device revocation stops hosted access; local pairing is retained. A gateway outage never silently changes mode or opens a public workload listener. Only an authenticated phone connection completes verification.

Removing the computer in Inevitable and activating it again can change its gateway address. The phone keeps its pairing but may still have the retired address. After browser activation completes, use **Reconnect your phone → Show update QR** in the Client and scan that QR in the phone app; **Copy update** retains the text export. Keep the saved Client enabled and start sharing if stopped. The update preserves pairing and remains pending until the phone acknowledges it; do not remove the phone or create another invitation for an address update. If another account removal interrupts recovery, complete browser activation again before exporting the new update.

If the Client was also removed from the phone app, its pairing key is gone and a connection update cannot restore it. On the desktop dashboard, open **Phone settings → Remove phone** and confirm. The Client returns to **Connect phone**, where **Show QR code** creates a fresh invitation to scan on the phone. Merely opening Phone settings or removing the old association does not generate an invitation. This replaces the phone association while retaining Inevitable activation and proxy settings. It does not stop other computers saved on the phone.

On Inevitable, use the separate Mobile health/readiness and sanitized connection/error/resource indicators. Keep existing Core traffic and accounting enabled. Mobile gateway deployment/drain/rollback uses its own service and target registrations; disabling it must not replace healthy Core ASG nodes. Follow the sibling Inevitable requirements/runbook for certificates, fleet scope, signed configuration expiry and route-owner recovery. No Mobile usage dispatch is part of recovery.

## Unreachable endpoint in Advanced direct mode

Use Review setup to inspect the address and Network access guidance without removing the paired phone. Public-address discovery is a suggestion from the computer's outgoing network path, and can report a VPN/NAT gateway. It never proves ingress and is not required during normal sharing. Manual hostname/IP entry remains available.

Check firewall reports the existing local policy; Retry firewall reapplies only Inevitable Mobile Relay's own rule/exception for the saved configuration. Windows uses executable/service/port scope. Mac uses an application exception for the validated installed daemon; its global firewall, block-all, managed policies, PF and other filters are preserved. Disabled means the native firewall is off, not that incoming access was verified. Blocked, unknown and unavailable states need the displayed recovery step; do not assume router or cloud policy can be inferred from local checks.

Verify the Client service is running and the listener port is free. Allow its chosen TCP port in the workload firewall; verify public address, router forwarding and any cloud security-group rule. Keep local proxy ports 1080/1081 private. The public port may differ from the local listener. Test using the phone’s cellular path, not another machine on the same LAN. No Tailscale/Funnel/AWS login can repair a direct endpoint.

On EC2, an allowed Windows/Mac firewall is not sufficient: the attached security group must allow the selected Client TCP port from the phone's cellular source, and the instance needs a public route/address. See [hosted servers and AWS EC2](standalone-clients.md#hosted-servers-and-aws-ec2), including why AWS My IP may select the wrong source. The wizard provides instructions, not AWS provisioning.

If a port is occupied, stop the conflicting owned service or choose an explicit different bind/public mapping. Inevitable Mobile Relay never silently selects another port. An old relay may still occupy 8443 on a former controller host.

## Phone availability

On Android, tap **Start cellular Agent** in Inevitable Mobile Relay. Sharing continues through its foreground service while cellular access remains available. Reboot and force-stop require Start again. Wi-Fi availability does not substitute for cellular.

On iPhone, tap **Start sharing** and keep Inevitable Mobile Relay active, open and the phone unlocked. Keep-awake defaults on during sharing and prevents only idle auto-lock. Stop or disabling the option restores normal auto-lock. Manual lock, app switching and inactive interruptions disconnect current streams; returning active creates new sessions only when Start intent remains enabled. Existing browser transfers may need retrying. Keep-awake does not provide background sharing.

Airplane Mode rotation affects every Client. Stop sessions, follow the native rotation guidance, and restore only still-enabled Clients after cellular recovery (and active foreground on iOS). Rotation is best-effort and does not guarantee a new carrier address.

On iOS, return to Inevitable Mobile Relay with Airplane Mode still on to observe the hold countdown, then turn it off and return again. The app retains cellular transitions it receives while inactive, but cannot guarantee observations while suspended. If no disconnect was observed, repeat the guided sequence. The separate cellular availability status helps distinguish phone connectivity from a particular Client's endpoint failure.

## Pairing, revocation and updates

Invitations expire after ten minutes for first redemption and are bound to the first CSR key. If an attempt may already have reached the Client, retry the existing phone entry with the same key; a lost response can recover even after the invitation's first-redemption deadline. Definitely-unsent expired invitations or a confirmed cancellation/expiry rejection release the matching unissued slot. Create a fresh invitation only when that old reservation is canceled or conclusively expired. Pending and disabled phone records count toward ten. A replacement phone requires explicit removal of the old phone on the Client.

Client revocation persists before it reports success and closes active traffic. Phone-side removal disables reconnection immediately. Other Clients continue independently.

If phone credential storage cannot finish removal, the Client remains stopped with a visible pending-removal state. Retry Remove after storage recovers. A separate private record of stopped Client IDs preserves that decision across restart without containing credentials. If even that record cannot be saved, the app explains that the stop is guaranteed only for the current session; retry before restarting the app, or revoke the phone from the workload Client.

Mode and endpoint changes are signed by the existing Client authority and carry monotonic generations. Compatible connected phones receive an update over their existing authenticated session, persist it and reconnect. Configuration checks on connection and every 30 seconds remain supported. Copy/scan a connection update for a phone that missed the change or uses an older direct build. Skipped generations work; stale, conflicting, wrong-Client or trust-changing updates reject. A pending status remains until acknowledgement.

Certificate renewal uses existing authenticated trust. Expired/unrecoverable credentials, lost keys or a new authority require re-pairing. Never bypass CA pinning, certificate validity or secure-store errors to recover.

Per-Client recovery keeps these cases separate:

- Confirmed rejected pairing or expired credentials: check the phone and Client clocks. If the credentials are revoked, expired with correct clocks, or lost, remove the pairing in the workload Client and create a fresh invitation; remove the old phone entry and pair again.
- Phone protected storage unavailable: unlock the phone, restore storage availability and retry. Do not delete the saved association merely because the key is temporarily inaccessible.
- Client protected storage unavailable: repair storage or service access on that workload Client, then retry. This does not establish that pairing was revoked.
- Network or TLS connection failure: check cellular access, ingress, endpoint and clocks. Import a signed connection update if the endpoint moved. If the Client authority was deliberately replaced, pair again; a timeout alone does not justify replacing trust.

Failures affect the corresponding Client. The app never deletes trust automatically in response to a network error. Stop remains available while sharing is running, including during phone management operations.

## Installation and retirement

Signed update/repair preserves protected direct pairing and local proxy credentials. Windows LocalSystem DPAPI and Mac file-based System Keychain errors require restoring correct service identity/access. Do not copy plaintext credentials out or recreate keys silently.

The 2.x local installer can migrate a recognized EC2-installed Client without AWS. Unknown paths/accounts reject. Former controller/relay hosts are retired separately; archive encrypted recovery state, disable only app-owned startup and remove only the exact Inevitable Mobile Relay Funnel rule. Preserve unrelated Tailscale/VPN state and historical tagged artifacts.

## Performance

There is no fixed Mbps throttle. Direct mode removes the former personal-relay uplink from remote workload traffic. Phone cellular upload still carries downloaded page data back to the workload; carrier congestion, workload networking and finite device resources still matter. Historical roughly 5 Mbps relay-path measurements are not a direct-mode ceiling or acceptance result.

Record sanitized throughput, latency, CPU, memory and thermal measurements for one and ten Clients. Include slow-reader/fairness/cancellation behavior; do not increase transport connections or buffers without contention evidence.
