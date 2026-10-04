# Standalone Windows and Mac Clients

Mobile Egress 2 installs directly on the workload machine. Supported initial targets are x64 Windows 10/11 and Windows Server 2019+, and Apple Silicon macOS 13+. Linux and Intel Macs are deferred. Use only accepted compatible 2.x installers and phone builds from official distribution; existing 1.x releases do not support this setup.

## Install and connect

1. Install `MobileEgressClientSetup.exe` or the signed/notarized `mobile-egress-client-macos-<version>-arm64.pkg`. Preserve normal publisher/Gatekeeper checks; never bypass a signer mismatch or remove quarantine to force installation.
2. Leave **Inevitable Gateway** selected and activate this Client in your browser using your Inevitable account. Initial hosted access requires a pilot grant. Reopening resumes a pending activation; the app never asks for your account password. The service retains its scoped credential in protected storage.
3. Wait for gateway connection. The computer and phone use outbound TCP 443; this mode performs no public-IP discovery and opens no inbound workload listener or local firewall exception. Router forwarding and provider inbound rules are unnecessary for hosted mode. Activation or a connected gateway alone does not verify phone traffic.
4. Choose **Pair phone** and scan the private ten-minute QR in the phone app. The **Start on your phone** step then asks you to open Mobile Egress on your phone and tap **Start cellular Agent** (Android) or **Start sharing** (iPhone). The computer continues automatically once the phone connects. Keep the iPhone app open and unlocked. Listening or an allowed firewall alone does not establish a phone connection.
5. Use your proxy shows the HTTP proxy line and SOCKS URL copy actions. Configure the intended application on that same computer. Windows uses 127.0.0.2; Mac uses 127.0.0.1.

Finish later preserves configuration. Reopening resumes unfinished setup; Review setup on a paired Client preserves its identity. Existing paired Clients open the dashboard even while their phone is offline. Retrying firewall access does not generate a new invitation. The app waits up to 30 seconds for the installed service before showing retry/repair guidance.

The Windows installer opens the app unelevated after installation. The Mac PKG opens it only when the saved installation owner is the active GUI user; otherwise open Mobile Egress Client from Applications as that owner. A fresh Mac installation still requires its intended owner to be logged in. Headless upgrades/repair retain the owner and do not open a GUI. Never run the Mac Client app as root.

### Advanced direct mode: home routers

Choose Direct explicitly to operate without Inevitable activation. Computer address suggests an unverified public IP using ipify; the provider sees the outgoing address but no pairing credentials. Keep editable hostname/IP and separate public/local ports (8443 by default). Network access configures the existing Windows executable/service/port rule or Mac application exception where policy allows. Check/Retry preserves pairing. Direct is never an automatic fallback when hosted access fails.

Forward the advertised public TCP port to the computer's LAN address and selected local listener port. The wizard displays available local addresses; choose the adapter connected to your router and keep its LAN assignment stable. CGNAT or a private upstream network may prevent incoming access despite ordinary forwarding. A public-IP suggestion is not a diagnosis of this condition. Obtain a reachable address/route or use a supported publicly reachable server; automatic NAT traversal is not provided.

### Hosted servers and AWS EC2

This section applies only to **Advanced direct mode**, including a Client on a hosted server. In Inevitable Gateway mode the server needs outbound TCP 443, with no Mobile Egress inbound rule.

The operating-system firewall and provider firewall are separate. On EC2, select the security group attached to the instance and add an inbound **Custom TCP** rule for the Client listener port (default **8443**). The instance also needs a public IPv4/IPv6 address and an appropriate internet-gateway route; outbound Internet access through a NAT gateway alone does not establish inbound reachability. Custom network ACLs or other filtering must permit the connection and replies. See [AWS security groups](https://docs.aws.amazon.com/vpc/latest/userguide/working-with-security-group-rules.html) and [internet-gateway routing](https://docs.aws.amazon.com/vpc/latest/userguide/VPC_Internet_Gateway.html).

The allowed source must include the phone's **cellular** address. AWS's **My IP** setting uses the browser's public IP, which may belong to your administration computer rather than the phone. A narrow current-phone address rule can stop working after cellular rotation. Restrict sources where practical; broader Internet sources (`0.0.0.0/0` for IPv4 or `::/0` for IPv6) permit anyone to reach this listener, which still enforces pairing and authenticated Agent transport. If broader sources are needed, allow only the selected Client TCP port, never all ports or local proxy ports 1080/1081. Mobile Egress neither requests AWS credentials nor changes these rules.

Other hosting providers require the equivalent inbound TCP rule and reachable network route. A connection failure alone cannot identify which firewall, address or route is responsible.

One phone pairs to each Client. One phone saves at most ten Clients, including disabled and pending records. Cancel/expire pending attempts or remove a Client to release a slot. A retry resumes the existing pending identity.

## iPhone operation

Leave Mobile Egress open and unlocked. Keep screen awake while sharing defaults on; it prevents idle auto-lock while the app is active and sharing is requested. Manual lock, switching apps, Control Center or other inactive transitions pause connections. Returning active reconnects only if Start intent remains enabled. Stop restores normal auto-lock. Brightness is unchanged. A dedicated powered phone can remain on its sharing dashboard; this is foreground operation, not a background daemon.

## Upgrade from 1.x

Run the new Client installer locally. Known AWS-installed Windows services can migrate without AWS access. The installer verifies the existing service path/account and retains its protected state directory, proxy credentials and LocalSystem ownership. Unknown installations fail with recovery instructions rather than being adopted. Existing standalone repair preserves the installation owner.

The Client requires fresh phone pairing. Old relay CA/client credentials and QR/update formats are not current identities. Phone upgrades remove old active associations; iOS must also stop/disable/remove its app-owned VPN profile before Start. If cleanup fails, follow the app’s Settings guidance; do not delete another VPN. Existing paired direct 2.x installations can switch to hosted mode without replacing their authority, pairing key or proxy credentials; update the phone apps first and retain a connection-update file for recovery.

Retire the old personal controller separately after workload migration. Do not install a public Client on that computer merely to remove the controller. Preserve encrypted recovery data, stop only Mobile Egress relay startup, and remove only its exact Funnel mapping if present. Leave other Tailscale configuration intact.

## Maintenance

Run a compatible signed installer for upgrades/repair. Direct pairing, proxy credentials and secure-store ownership are preserved. The Windows service and Mac LaunchDaemon run after boot/network availability and after logout; the graphical Client app need not remain open. The phone must remain available according to its platform lifecycle.

Change mode or the direct advertised endpoint in the Client app and keep the phone connected to receive a signed update. If the old endpoint is unreachable, choose Show/Copy connection update and import it on the paired phone. Missed generations can be skipped. Pending remains until acknowledged. Trust replacement requires re-pairing. Revoking Inevitable gateway access stops hosted attachment; it does not erase local phone pairing or affect other commercial proxies.

Remove paired phone immediately revokes its access and closes traffic. Pair a replacement using a new invitation. To free a phone registry slot, also remove the saved Client there.
