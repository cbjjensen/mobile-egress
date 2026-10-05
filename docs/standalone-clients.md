# Standalone Windows and Mac Clients

Mobile Egress 2 installs directly on the workload machine. Supported initial targets are x64 Windows 10/11 and Windows Server 2019+, and Apple Silicon macOS 13+. Linux and Intel Macs are deferred. Use only accepted compatible 2.x installers and phone builds from official distribution; existing 1.x releases do not support this setup.

## Install and connect

1. Install `MobileEgressClientSetup.exe` or the signed/notarized `mobile-egress-client-macos-<version>-arm64.pkg`. Preserve normal publisher/Gatekeeper checks; never bypass a signer mismatch or remove quarantine to force installation.
2. In **Your account**, choose **Continue in browser** to sign in to Inevitable and approve this computer. Initial hosted access requires a pilot grant. Keep Mobile Egress open; it continues after approval. Reopening resumes a pending request. The app never asks for your account password; its scoped credential stays in protected storage.
3. **Connect this computer** checks the connection to Inevitable. You do not need to change router settings. The computer and phone use outbound TCP 443; hosted mode performs no public-IP discovery and opens no inbound workload listener or local firewall exception. Account approval or a connected gateway alone does not verify phone traffic.
4. In **Connect phone**, choose **Show QR code**. On your phone, open Mobile Egress, tap **Scan QR** and scan the screen. Keep this private, one-use code to yourself; it expires after ten minutes. **Start sharing** then asks you to tap **Start cellular Agent** (Android) or **Start sharing** (iPhone) in the phone app. The computer continues automatically once the phone connects. Keep the iPhone app open and unlocked. Listening or an allowed firewall alone does not establish a phone connection.
5. **Use in your apps** provides **Copy HTTP proxy** and **Copy SOCKS5 proxy**. Paste the appropriate details into your intended application's proxy settings on this computer. Only apps configured with these details use the phone's mobile data. **Which option should I choose?** explains the formats, including apps that require separate address, port, username and password fields. Windows uses 127.0.0.2; Mac uses 127.0.0.1.

Finish later preserves configuration and provides **Continue setup**. Reopening resumes unfinished setup. The app waits up to 30 seconds for the installed service before showing retry/repair guidance.

After pairing, the dashboard shows connection status and proxy copy actions. A phone going offline keeps you on the dashboard with instructions to start sharing in the phone app. **Phone settings** holds connection updates and confirmed phone removal; **Review setup** opens the wizard to review activation, connection mode or address without resetting pairing. Routine dashboard use does not show activation forms or pairing invitations. A pending connection update or rejected hosted access shows the recovery action currently needed. Retrying firewall access does not generate a new invitation.

Dashboard, Phone settings and Review setup share the same page navigation, with the current page highlighted. Reviewing setup offers **Back to dashboard**; **Finish later** is reserved for the initial setup flow. Moving between these pages does not save edited settings or issue a new invitation.

The main screens describe the next action in everyday language. **Connection details** retains technical status and actionable error instructions; it opens when a connection error first appears. **Advanced connection settings** holds connection-mode choices. Reviewing an approved account shows **Your account is connected**, hides the activation name field and does not ask you to sign in again. It does not change the saved account connection.

The Windows installer opens the app unelevated after installation. The Mac PKG opens it only when the saved installation owner is the active GUI user; otherwise open Mobile Egress Client from Applications as that owner. A fresh Mac installation still requires its intended owner to be logged in. Headless upgrades/repair retain the owner and do not open a GUI. Never run the Mac Client app as root.

Mac owner detection uses macOS's active session record, including remote desktops on hosted Macs. The physical `/dev/console` device can remain root-owned during a valid remote login and is not used to select the Client owner. Fresh installation requires a valid user session; upgrades and repair retain the saved owner.

### Advanced direct mode: home routers

Open **Advanced connection settings → Direct connection → Set up direct connection** explicitly to operate without Inevitable activation. **Computer address** suggests an unverified public IP using ipify; the provider sees the outgoing address but no pairing credentials. Keep editable hostname/IP and separate public/local ports (8443 by default). **Network access** configures the existing Windows executable/service/port rule or Mac application exception where policy allows. Check/Retry preserves pairing. Direct is never an automatic fallback when hosted access fails.

Forward the advertised public TCP port to the computer's LAN address and selected local listener port. The wizard displays available local addresses; choose the adapter connected to your router and keep its LAN assignment stable. CGNAT or a private upstream network may prevent incoming access despite ordinary forwarding. A public-IP suggestion is not a diagnosis of this condition. Obtain a reachable address/route or use a supported publicly reachable server; automatic NAT traversal is not provided.

### Hosted servers and AWS EC2

This section applies only to **Advanced direct mode**, including a Client on a hosted server. When connected through Inevitable, the server needs outbound TCP 443, with no Mobile Egress inbound rule.

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

Use **Review setup** to change mode or the direct advertised endpoint and keep the phone connected to receive a signed update. If the old endpoint is unreachable, use **Reconnect your phone → Show update QR**, or open **Phone settings** for **Show update QR / Copy update**, and import it on the paired phone. Missed generations can be skipped. Pending remains until acknowledged. Trust replacement requires re-pairing. Revoking Inevitable gateway access stops hosted attachment; it does not erase local phone pairing or affect other commercial proxies.

Open **Phone settings → Remove phone** and confirm to revoke its access and close this computer's phone connection. The Client returns to **Connect phone**; choose **Show QR code** to pair again or add a replacement. Other computers saved on the phone are unaffected. To free a phone registry slot, also remove this saved Client there.

Removing the computer from Inevitable only revokes hosted access; it does not remove its local phone pairing. After reactivation, **Reconnect your phone** appears if the gateway address changed. Choose **Show update QR**, scan it in the phone app and start sharing if stopped. This updates the saved pairing rather than creating a second Client. If you also removed the computer from the phone app, use **Phone settings → Remove phone**, then **Connect phone → Show QR code** to pair again; an update cannot restore a deleted pairing.
