# Mobile Egress

**Use your phone’s cellular connection from your Windows PC or Mac.**

Mobile Egress routes traffic from proxy-compatible applications through your own Android phone or iPhone. Browse, run automation, or test an application over a mobile connection while keeping the application on your computer.

One product, two companion apps: **Mobile Egress Client** runs on your computer or server, and the **Mobile Egress phone app** supplies the cellular connection. You provide the computer, phone, and mobile data plan.

> **Version 2.0 is in pre-release validation.** Signed installation and physical-device testing are still pending; 2.0 downloads are not release-ready. Published 1.x builds use the previous architecture and cannot be used with this setup.

## What you can do

- **Use familiar proxy settings.** Connect applications that support authenticated HTTP/HTTPS CONNECT or SOCKS5 over TCP.
- **Use your own cellular connection.** Traffic exits through your phone’s carrier network. The Client-to-phone connection is encrypted and authenticated.
- **Connect several computers.** Save up to ten Clients on one phone, with independent connection status and enable/disable controls. They share the same phone and cellular capacity.
- **Manage access locally.** Pair by QR code, copy proxy details, remove a paired phone, and recover after a computer’s public address changes.
- **Run directly.** No separate relay computer, AWS account, Tailscale, or Funnel setup is required.

## How it works

```text
Your application → local Client proxy ⇄ your phone → cellular Internet
```

The phone connects to the Client on your computer over cellular. Your application connects to a local proxy on that same computer. Only applications configured to use the proxy send their traffic through Mobile Egress.

**Your computer must be reachable from the phone’s cellular network.** It needs a public address or router port forwarding. The phone does not need an inbound port, and the devices do not need to be on the same network.

## What you need

The initial 2.0 platform targets are:

| Device | Requirements |
|---|---|
| Windows computer or server | x64 Windows 10/11 or Windows Server 2019+ |
| Mac | Apple Silicon, macOS 13+ |
| Android phone | Android 10+ with working cellular data |
| iPhone | iOS 17+ with working cellular data; app must remain open and active |

You also need permission to install the Client and allow its incoming connection, plus an application with authenticated proxy support. Linux and Intel Macs are outside the initial release.

The setup wizard configures the Client's local firewall access where permitted. Windows uses an executable/service/port rule; macOS uses an application exception for the Client daemon. Existing managed or block-all policies can require administrator help.

For a computer behind a home router, forward **TCP 8443** to that computer, or use the public/local ports you chose in Advanced settings. Mobile Egress does not change your router automatically.

**Hosted servers, including AWS EC2, may require an additional firewall rule.** Allow inbound TCP on your Client's configured listener port—8443 by default—in the provider firewall or EC2 security group. Your server must also have a publicly reachable address and network route. The installer does not change cloud security groups or router settings. Never expose proxy ports 1080 or 1081. See the [server networking guide](docs/standalone-clients.md#hosted-servers-and-aws-ec2).

If your ISP uses carrier-grade NAT (CGNAT), ordinary router forwarding may not make the computer reachable. Arrange a reachable address with your provider or use a supported computer/server that already has one. Automatic NAT traversal is not included.

## Setup

Use matching 2.x computer and phone builds once accepted downloads are available.

1. **Install both apps.** Install the Windows Client or Mac Client on the computer running your applications, and the compatible phone app on your phone.
2. **Run the installer and follow the setup wizard.** Mobile Egress suggests your public address, configures local access where permitted, and guides you through pairing your phone. You can edit the address and port before continuing. If the Mac app does not open automatically, open Mobile Egress Client from Applications.
3. **Complete network access and pair your phone.** Follow the wizard's Home/router, AWS EC2, or Other hosted server instructions. Scan its private pairing QR in the phone app; invitations expire after ten minutes for initial pairing.
4. **Verify the connection.** Tap **Start** on the phone and wait for **Connected** in the wizard. A suggested address or a successful firewall check alone does not prove the phone can reach your computer. Follow the platform requirements below.
5. **Connect your application.** Use **Copy HTTP proxy** or **Copy SOCKS URL**, then enter those details in your application's proxy settings. Copies include the username and password; keep them private.

Address suggestions use ipify, which receives your computer's outgoing public IP. Suggestions may describe a VPN or NAT gateway instead of a reachable computer. You can enter a hostname or IP manually if discovery fails; ongoing sharing does not depend on the lookup service. **Finish later** preserves setup, and **Review setup** lets you revisit settings without resetting an existing pairing.

The HTTP copy uses `host:port:username:password`; enter these as separate fields if your application requires them. The SOCKS copy is a URL.

| Computer | HTTP / HTTPS CONNECT | SOCKS5 |
|---|---|---|
| Windows | `127.0.0.2:1081` | `127.0.0.2:1080` |
| Mac | `127.0.0.1:1081` | `127.0.0.1:1080` |

These proxy addresses are local to the computer. **Keep ports 1080 and 1081 private.** Only the phone connection listener needs incoming access. Configure individual applications rather than a system-wide proxy.

## Keeping your phone connected

**Android:** after you tap Start, sharing runs through a foreground service with a visible notification. You can leave the app or turn off the screen. After rebooting or force-stopping the app, open it and tap Start again.

**iPhone:** leave Mobile Egress open, active, and unlocked while sharing. **Keep screen awake while sharing** is enabled by default and prevents automatic locking during active sharing. Manually locking the phone, switching apps, or an interruption that makes the app inactive pauses traffic. Returning to the app reconnects if sharing is still requested; tapping Stop clears that request and restores normal automatic locking. Turning keep-awake off allows normal auto-lock, which pauses sharing when it occurs. Keep-awake does not enable background operation.

For longer iPhone sessions, use a dedicated phone and keep it powered. The computer must also remain awake and connected. Its Client service can continue after logout; the Client management window does not need to stay open.

## Performance and limits

- **Cellular only.** Both the connection to your computer and outgoing Internet traffic use cellular data. If cellular becomes unavailable, traffic stops; it does not fall back to phone Wi-Fi.
- **Speed depends on your connection.** There is no fixed Mbps throttle. Cellular upload and download, carrier congestion, computer networking, and device resources affect performance. Even downloading a page requires the phone to upload that data back to your computer. No minimum speed is guaranteed.
- **Bring your own data plan.** Traffic consumes your phone’s mobile data. Mobile Egress does not supply cellular service or a pool of proxy IP addresses.
- **One phone per Client.** Each Client pairs with one phone. Each phone saves up to ten Clients, including pending and disabled entries.
- **Public Internet destinations over TCP.** Private-network destinations, UDP, and QUIC are unsupported. Applications must send the intended traffic through their configured proxy.
- **Carrier-controlled addresses.** Guided cellular IP rotation requires manual Airplane Mode steps and interrupts every connected Client. A new address is not guaranteed. Separate Clients on one phone do not receive separate dedicated IPs.

## Downloads and existing installations

Check [official releases](https://github.com/cbjjensen/mobile-egress/releases) for availability and compatibility notes. The 2.x computer packages are the **Windows Client installer** (`MobileEgressClientSetup.exe`) and **Apple Silicon Mac Client PKG**, paired with compatible Android or iPhone builds. The pre-release notice above remains in effect; historical 1.x downloads are not substitutes.

Upgrading from 1.x requires fresh pairing and a reachable Client endpoint. Follow the [installation and migration guide](docs/standalone-clients.md). Retire a former controller computer using the [separate retirement instructions](docs/controller-retirement.md).

## Help and technical documentation

- [Install, pair, upgrade, and repair](docs/standalone-clients.md)
- [Connection troubleshooting and endpoint recovery](docs/operations.md)
- [Android guide](android/README.md) and [iPhone guide](ios/README.md)
- [Architecture](docs/architecture.md), [security](docs/security-model.md), and [protocol](docs/protocol.md)
- [Release validation status](docs/direct-acceptance.md) and [release process](docs/deployment.md)
- [Development plan](docs/superpowers/plans/2026-10-03-direct-client-phone.md) and [historical relay measurements](docs/latency-benchmarks.md)
