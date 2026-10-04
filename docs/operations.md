# Operations and recovery

## Reading connection state

- Activation required: activate hosted access in your browser, or explicitly choose Advanced direct mode.
- Gateway connecting / unavailable: inspect account access, outbound TCP 443 and service readiness; this is separate from phone pairing.
- Set up endpoint / Migration required: complete the selected mode and pair a phone.
- Listening / Awaiting phone: local listener is ready; cellular reachability is not yet proved.
- Connected: the paired phone has an authenticated direct session. A target may still reject a particular request.
- Connection update pending: desired endpoint is persisted but not acknowledged by the phone.
- Service unavailable / Needs attention: use the stated storage, installation, port, trust or network recovery action.

Status is deliberately secret-safe. Copy proxy credentials or invitations only through the explicit app actions. Never paste credentials, QR capabilities or private diagnostic dumps into issue reports.

## Hosted connection recovery

Check Inevitable access and the workload's outbound Internet connection. Resume pending browser activation from the Client app; its proof stays in the service. Expired/denied activation requires a new request. If the one-time authorization response is lost before durable storage, reactivate the same Client. Account device revocation stops hosted access; local pairing is retained. A gateway outage never silently changes mode or opens a public workload listener. Only an authenticated phone connection completes verification.

On Inevitable, use the separate Mobile health/readiness and sanitized connection/error/resource indicators. Keep existing Core traffic and accounting enabled. Mobile gateway deployment/drain/rollback uses its own service and target registrations; disabling it must not replace healthy Core ASG nodes. Follow the sibling Inevitable requirements/runbook for certificates, fleet scope, signed configuration expiry and route-owner recovery. No Mobile usage dispatch is part of recovery.

## Unreachable endpoint in Advanced direct mode

Use Review setup to inspect the address and Network access guidance without removing the paired phone. Public-address discovery is a suggestion from the computer's outgoing network path, and can report a VPN/NAT gateway. It never proves ingress and is not required during normal sharing. Manual hostname/IP entry remains available.

Check firewall reports the existing local policy; Retry firewall reapplies only Mobile Egress's own rule/exception for the saved configuration. Windows uses executable/service/port scope. Mac uses an application exception for the validated installed daemon; its global firewall, block-all, managed policies, PF and other filters are preserved. Disabled means the native firewall is off, not that incoming access was verified. Blocked, unknown and unavailable states need the displayed recovery step; do not assume router or cloud policy can be inferred from local checks.

Verify the Client service is running and the listener port is free. Allow its chosen TCP port in the workload firewall; verify public address, router forwarding and any cloud security-group rule. Keep local proxy ports 1080/1081 private. The public port may differ from the local listener. Test using the phone’s cellular path, not another machine on the same LAN. No Tailscale/Funnel/AWS login can repair a direct endpoint.

On EC2, an allowed Windows/Mac firewall is not sufficient: the attached security group must allow the selected Client TCP port from the phone's cellular source, and the instance needs a public route/address. See [hosted servers and AWS EC2](standalone-clients.md#hosted-servers-and-aws-ec2), including why AWS My IP may select the wrong source. The wizard provides instructions, not AWS provisioning.

If a port is occupied, stop the conflicting owned service or choose an explicit different bind/public mapping. Mobile Egress never silently selects another port. An old relay may still occupy 8443 on a former controller host.

## Phone availability

Android must have sharing started and its foreground service/cellular access available. Reboot and force-stop require Start again. Wi-Fi availability does not substitute for cellular.

iOS must stay active, open and unlocked. Keep-awake defaults on during sharing and prevents only idle auto-lock. Stop or disabling the option restores normal auto-lock. Manual lock, app switching and inactive interruptions disconnect current streams; returning active creates new sessions only when Start intent remains enabled. Existing browser transfers may need retrying.

Airplane Mode rotation affects every Client. Stop sessions, follow the native rotation guidance, and restore only still-enabled Clients after cellular recovery (and active foreground on iOS). Rotation is best-effort and does not guarantee a new carrier address.

On iOS, return to Mobile Egress with Airplane Mode still on to observe the hold countdown, then turn it off and return again. The app retains cellular transitions it receives while inactive, but cannot guarantee observations while suspended. If no disconnect was observed, repeat the guided sequence. The separate cellular availability status helps distinguish phone connectivity from a particular Client's endpoint failure.

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

The 2.x local installer can migrate a recognized EC2-installed Client without AWS. Unknown paths/accounts reject. Former controller/relay hosts are retired separately; archive encrypted recovery state, disable only app-owned startup and remove only the exact Mobile Egress Funnel rule. Preserve unrelated Tailscale/VPN state and historical tagged artifacts.

## Performance

There is no fixed Mbps throttle. Direct mode removes the former personal-relay uplink from remote workload traffic. Phone cellular upload still carries downloaded page data back to the workload; carrier congestion, workload networking and finite device resources still matter. Historical roughly 5 Mbps relay-path measurements are not a direct-mode ceiling or acceptance result.

Record sanitized throughput, latency, CPU, memory and thermal measurements for one and ten Clients. Include slow-reader/fairness/cancellation behavior; do not increase transport connections or buffers without contention evidence.
