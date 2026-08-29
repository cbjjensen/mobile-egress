# Architecture

## Purpose

The system provides selective mobile egress. A Windows application opens a local SOCKS5 proxy. Only applications explicitly configured with that proxy route traffic through the Android phone; all other traffic remains unchanged.

```text
Selected Windows application
  -> 127.0.0.1 SOCKS5 listener (Windows app)
  -> encrypted client tunnel
  -> relay container
  -> encrypted agent tunnel
  -> Android cellular-bound TCP socket
  -> public Internet target
```

## Components

| Component | Responsibility | Does not do |
| --- | --- | --- |
| Relay | Enrollment, certificate issuance/revocation, stream matching, policy enforcement, aggregate health | Expose SOCKS or persist destinations/payloads |
| Windows client | Pair, host loopback SOCKS5, request relay streams, show local status | Change the device's default route |
| Android agent | Pair, maintain a user-visible foreground tunnel, open approved public targets through cellular | Accept unsolicited inbound Internet traffic or fall back to Wi-Fi |
| Owner mode | Issue one-time pairing codes and revoke identities | Inspect payloads or destination history |

## Trust and persistence

The relay owns a private certificate authority in its mounted state directory. Pairing creates a key pair on the device, signs the public key with the private CA, and records the certificate serial and role in SQLite. The private key never leaves Android Keystore or Windows DPAPI-backed storage.

The relay stores identity status, aggregate counters, last-seen times, and redacted error classes. It never stores target hostnames, destination addresses, SOCKS credentials, tunnel payloads, or certificates' private keys.

## Default operating limits

- One active Android agent.
- Four concurrent streams per Windows client.
- Eight concurrent streams through the Android agent.
- Thirty-second connect timeout and five-minute idle timeout.
- Public Internet TCP only.

All limits are explicit relay configuration values with these defaults.
