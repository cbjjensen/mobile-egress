# Inevitable Mobile Relay

Formerly Mobile Egress. The new name applies to upcoming app builds and the website. Existing published installers and already-installed apps retain their original names until upgraded; pairing and credentials are preserved.

**Use your phone’s cellular connection from your Windows PC or Mac.**

Inevitable Mobile Relay routes traffic from proxy-compatible applications through your own Android phone or iPhone. Browse, run automation, or test an application over a mobile connection while keeping the application on your computer.

One product, two companion apps: **Inevitable Mobile Relay** runs on your computer or server, and the **Inevitable Mobile Relay phone app** supplies the cellular connection. You provide the computer, phone, and mobile data plan.

> **Version 2.x is in pilot validation.** Signed Windows, Mac and Android downloads are available in the [2.0.3 prerelease](https://github.com/cbjjensen/mobile-egress/releases/tag/v2.0.3) and through the Inevitable Mobile Relay dashboard. Full signed-installation and physical-device acceptance, including iPhone traffic and lifecycle checks, remains incomplete. Published 1.x builds use the previous architecture and cannot be used with this setup.

## What you can do

- **Use familiar proxy settings.** Connect applications that support authenticated HTTP/HTTPS CONNECT or SOCKS5 over TCP.
- **Use your own cellular connection.** Traffic exits through your phone’s carrier network. The Client-to-phone connection is encrypted and authenticated.
- **Connect several computers.** Save up to ten Clients on one phone, with independent connection status and enable/disable controls. They share the same phone and cellular capacity.
- **Manage access locally.** Activate the Client with Inevitable, pair your phone by QR, copy proxy details, and remove a paired phone locally.
- **Connect without router setup.** Hosted mode uses outbound connections through Inevitable's existing gateways. No separate relay computer, customer AWS account, Tailscale, or Funnel setup is required. Advanced direct mode is also available.

## How it works

```text
Your application → local Client proxy ⇄ Inevitable gateway ⇄ your phone → cellular Internet
```

Your computer and phone connect outbound to Inevitable on TCP 443. The gateway carries the phone-to-Client encrypted connection; its keys remain on your devices. Your application connects to the local proxy on that computer. Only applications configured to use the proxy send traffic through Inevitable Mobile Relay, and their Internet traffic exits through your phone.

**Hosted mode needs no incoming port on your computer or router.** Both devices need outbound access to Inevitable. Your phone uses cellular, and the devices need not be on the same network. The current controlled pilot requires an Inevitable account with Inevitable Mobile Relay access; the phone needs no separate login. Advanced direct mode connects without Inevitable but requires a reachable workload endpoint.

## What you need

The initial 2.0 platform targets are:

| Device | Requirements |
|---|---|
| Windows computer or server | x64 Windows 10/11 or Windows Server 2019+ |
| Mac | Apple Silicon, macOS 13+ |
| Android phone | Android 10+ with working cellular data |
| iPhone | iOS 17+ with working cellular data; app must remain open and active |

You also need permission to install the Client, outbound gateway access, and an application with authenticated proxy support. Linux and Intel Macs are outside the initial release.

### Advanced direct mode networking

The following inbound network setup applies only when you explicitly select direct mode. Existing direct installations stay direct until you switch them. Hosted mode does not run public-IP discovery, bind the direct listener, or open an inbound host firewall rule.

The setup wizard configures the Client's local firewall access where permitted. Windows uses an executable/service/port rule; macOS uses an application exception for the Client daemon. Existing managed or block-all policies can require administrator help.

For a computer behind a home router, forward **TCP 8443** to that computer, or use the public/local ports you chose in Advanced settings. Inevitable Mobile Relay does not change your router automatically.

**Hosted servers, including AWS EC2, may require an additional firewall rule.** Allow inbound TCP on your Client's configured listener port—8443 by default—in the provider firewall or EC2 security group. Your server must also have a publicly reachable address and network route. The installer does not change cloud security groups or router settings. Never expose proxy ports 1080 or 1081. See the [server networking guide](docs/standalone-clients.md#hosted-servers-and-aws-ec2).

If your ISP uses carrier-grade NAT (CGNAT), ordinary router forwarding may not make the computer reachable. Arrange a reachable address with your provider or use a supported computer/server that already has one. Automatic NAT traversal is not included.

## Setup

Use matching 2.x computer and phone pilot builds. iPhone testing uses an invitation through TestFlight; external availability depends on Apple's beta review. The public installer release does not include an Android APK.

1. **Install both apps.** Install the Windows Client or Mac Client on the computer running your applications, and the compatible phone app on your phone.
2. **Run the installer and follow the setup wizard.** Activate the Client in your Inevitable browser account. The Client keeps activation credentials in protected service storage. If the Mac app does not open automatically, open Inevitable Mobile Relay from Applications.
3. **Pair your phone.** Scan the Client's private pairing QR in the compatible phone app; invitations expire after ten minutes for initial pairing. The phone does not sign in to Inevitable.
4. **Verify the connection.** Tap **Start** on the phone and wait for **Connected**. Account activation and a gateway connection alone do not prove that your phone is connected. Follow the platform requirements below.
5. **Connect your application.** Use **Copy HTTP proxy** or **Copy SOCKS URL**, then enter those details in your application's proxy settings. Copies include the username and password; keep them private.

Advanced direct setup retains editable hostname/port, local firewall checks, and router/provider instructions. Its ipify address suggestion receives your outgoing public IP and may describe a VPN/NAT gateway instead of a reachable computer; manual entry remains available. **Finish later** preserves setup, and **Review setup** revisits settings without resetting pairing. Gateway failure never silently switches modes.

The HTTP copy uses `host:port:username:password`; enter these as separate fields if your application requires them. The SOCKS copy is a URL.

| Computer | HTTP / HTTPS CONNECT | SOCKS5 |
|---|---|---|
| Windows | `127.0.0.2:1081` | `127.0.0.2:1080` |
| Mac | `127.0.0.1:1081` | `127.0.0.1:1080` |

These proxy addresses are local to the computer. **Keep ports 1080 and 1081 private.** Only Advanced direct mode needs incoming access to its authenticated phone listener. Configure individual applications rather than a system-wide proxy.

## Keeping your phone connected

**Android:** after you tap Start, sharing runs through a foreground service with a visible notification. You can leave the app or turn off the screen. After rebooting or force-stopping the app, open it and tap Start again.

**iPhone:** leave Inevitable Mobile Relay open, active, and unlocked while sharing. **Keep screen awake while sharing** is enabled by default and prevents automatic locking during active sharing. Manually locking the phone, switching apps, or an interruption that makes the app inactive pauses traffic. Returning to the app reconnects if sharing is still requested; tapping Stop clears that request and restores normal automatic locking. Turning keep-awake off allows normal auto-lock, which pauses sharing when it occurs. Keep-awake does not enable background operation.

For longer iPhone sessions, use a dedicated phone and keep it powered. The computer must also remain awake and connected. Its Client service can continue after logout; the Client management window does not need to stay open.

## Performance and limits

- **Cellular only.** The phone uses cellular for both its Client connection (through Inevitable in hosted mode) and outgoing Internet traffic. If cellular becomes unavailable, traffic stops; it does not fall back to phone Wi-Fi.
- **Speed depends on your connection.** There is no fixed Mbps throttle. Cellular upload and download, carrier congestion, computer networking, and device resources affect performance. Even downloading a page requires the phone to upload that data back to your computer. No minimum speed is guaranteed.
- **Bring your own data plan.** Traffic consumes your phone’s mobile data. Inevitable Mobile Relay does not supply cellular service or a pool of proxy IP addresses.
- **Access, not data metering.** Inevitable Mobile Relay gateway access depends on entitlement, not traffic volume. It submits no customer traffic usage or destinations to Inevitable, and has no included-data allowance or overage charges. Your carrier's data-plan terms still apply. Inevitable's other proxy products retain their own accounting.
- **Hosted availability.** Hosted mode depends on Inevitable gateways and access/configuration services. Gateways can observe connection addresses, timing and routing metadata, but do not hold the keys for the phone-to-Client TLS connection. Operational health monitoring is separate from traffic usage reporting.
- **One phone per Client.** Each Client pairs with one phone. Each phone saves up to ten Clients, including pending and disabled entries.
- **Public Internet destinations over TCP.** Private-network destinations, UDP, and QUIC are unsupported. Applications must send the intended traffic through their configured proxy.
- **Carrier-controlled addresses.** Guided cellular IP rotation requires manual Airplane Mode steps and interrupts every connected Client. A new address is not guaranteed. Separate Clients on one phone do not receive separate dedicated IPs.

## Downloads and existing installations

The Inevitable Mobile Relay page provides **Download for your computer**, **Get the phone app**, and account access management. When configured, Windows, Apple Silicon Mac and Android downloads use verified public R2 files. Unavailable downloads are shown as unavailable; owning an installer does not activate hosted access.

Subscription support offers monthly or yearly billing for one account, covering its activated computers without paid seats or data quotas. Prices and new sales remain disabled until an administrator explicitly opens them. Cancellation keeps access through the paid term; changing billing periods requires that term to end before subscribing again. Complimentary pilot access remains separate.

**The iPhone TestFlight pilot is free and requires approval.** Purchasing a subscription does not enroll you. Approved pilot accounts can save a TestFlight invitation email with consent to share it with Apple; no Apple password is requested. Apple review, build expiry and tester capacity can delay availability. The website distinguishes a saved request from an invitation or a build ready to install. Production iPhone distribution will use the approved App Store link.

The [2.0.3 pilot release](https://github.com/cbjjensen/mobile-egress/releases/tag/v2.0.3) provides the signed **Windows Client installer** (`InevitableMobileRelaySetup.exe`), signed/notarized **Apple Silicon Mac Client PKG**, and signed **Android APK**. The [Inevitable Mobile Relay dashboard](https://inevitableproxies.com/dashboard/mobile-egress) links to the same verified downloads on R2. iPhone builds use TestFlight: 2.0.3 (8) is available internally and awaiting external beta review; approved 2.0.0 (7) remains compatible for external testers. Existing 2.x pairings can be retained when updating. The pilot-validation notice above remains in effect; historical 1.x downloads are not substitutes.

Publishing details and integrity checks are documented in the [R2 download guide](docs/r2-downloads.md). Subscription and invitation source changes do not themselves open sales, enable invitations or promote pilot downloads to stable.

Upgrading from 1.x requires fresh pairing. Existing direct 2.x installations keep their settings until you explicitly switch to hosted mode. Follow the [installation and migration guide](docs/standalone-clients.md). Retire a former controller computer using the [separate retirement instructions](docs/controller-retirement.md).

## Help and technical documentation

- [Install, pair, upgrade, and repair](docs/standalone-clients.md)
- [Connection troubleshooting and endpoint recovery](docs/operations.md)
- [Android guide](android/README.md) and [iPhone guide](ios/README.md)
- [Architecture](docs/architecture.md), [security](docs/security-model.md), and [protocol](docs/protocol.md)
- [Release validation status](docs/direct-acceptance.md) and [release process](docs/deployment.md)
- [Development plan](docs/superpowers/plans/2026-10-03-direct-client-phone.md) and [historical relay measurements](docs/latency-benchmarks.md)
