# Refract-Compatible HTTP Proxy Line Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an authenticated HTTP proxy to each EC2 Client and make `IP:PORT:USERNAME:PASSWORD` the controller's default copied proxy format.

**Architecture:** `MobileEgressClient` keeps authenticated SOCKS5 on `127.0.0.1:1080` and adds authenticated ordinary-HTTP forwarding plus HTTPS CONNECT on `127.0.0.1:1081`. Both listeners use the same retained credentials and relay session, so the existing four-stream Client limit remains authoritative without another admission layer.

**Tech Stack:** Go 1.26, Wails, React/TypeScript, Windows services, existing Mobile Egress relay protocol.

## Global Constraints

- HTTP forward/CONNECT binds only to IPv4 loopback at `127.0.0.1:1081`.
- Keep SOCKS5 unchanged at `127.0.0.1:1080`.
- Do not change AWS networking, relay/Android protocols, pairing, sealed configuration, credentials, or capacity.
- The default copied line is `127.0.0.1:1081:USERNAME:PASSWORD`.
- CONNECT-only behavior shipped in `1.0.22`; complete ordinary HTTP plus HTTPS CONNECT behavior is available on Client version `1.0.24` and later.
- Never log proxy credentials, target destinations, headers, or payloads.
- Use the Windows-only component gate; do not build Android.

---

### Task 1: HTTP forward/CONNECT listener

- [ ] Add failing tests for IPv4-loopback binding, Basic proxy authentication, ordinary HTTP request/response forwarding, proxy-header removal, CONNECT domain/IPv4/IPv6 targets, response ordering, bidirectional transfer, relay failures, the existing 30-second open timeout, pre-open data, standard HTTP header sizing, and shutdown.
- [ ] Implement both modes using the existing relay stream opener. Return `407`, `400`, `502`, or `503` without exposing private details; return `200 Connection Established` only after a CONNECT stream opens.
- [ ] Run the focused HTTP proxy tests until green.

### Task 2: Headless Client lifecycle

- [ ] Add failing service tests proving ports `1080` and `1081` start and stop together, bind only to loopback, and roll back atomically when either port is unavailable.
- [ ] Start both listeners with the existing credentials and switching relay session. Do not add a second stream quota; the existing Client session and relay enforce four streams.
- [ ] Run the node-service and existing SOCKS tests until green.

### Task 3: Controller and UI

- [ ] Add failing repository, desktop-binding, and frontend tests for the default colon-delimited line, the secondary SOCKS URL, version capability detection, copy labels, upgrade guidance, and secret-free activity messages.
- [ ] Change `NodeProxyLine(instanceID)` to return the HTTP line; add `NodeSOCKSProxyURL(instanceID)` for the existing URL.
- [ ] Make **Copy proxy line** the primary action, add **Copy SOCKS5 URL**, show `127.0.0.1:1081:***:***`, and disable HTTP copying until the managed Client version is at least `1.0.24`.
- [ ] Refresh managed-node capability after Update or Repair and run focused controller/frontend tests until green.

### Task 4: Documentation and verification

- [ ] Update the README, architecture, Windows, deployment, operations, security, and status documentation for dual loopback listeners, ordinary HTTP forwarding, HTTPS-through-CONNECT behavior, Refract setup, and the Client update requirement.
- [ ] Run focused Go/frontend checks, then `& .\scripts\test-all.ps1 -Components Windows` once.
- [ ] Review the diff for credential leakage and unintended Android, relay-protocol, AWS-networking, or capacity changes.

## Physical Acceptance

1. Update one managed EC2 Client to `1.0.24` or later.
2. Confirm `1080` and `1081` listen only on `127.0.0.1`.
3. Confirm SOCKS5, ordinary HTTP, and HTTPS CONNECT requests return the phone's cellular IP.
4. Paste **Copy proxy line** output into Refract and run its proxy test.
5. Confirm direct EC2 traffic still uses its normal EC2 route.

GitHub publication remains a separate explicitly approved action.
