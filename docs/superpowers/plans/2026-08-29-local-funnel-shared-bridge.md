# Local Funnel Shared Bridge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run the relay on the operator's local Windows computer behind Tailscale Funnel and provision up to ten SSM-managed Windows Server 2019 EC2 Client nodes that share one Android cellular Agent.

**Architecture:** The desktop controller installs and owns a loopback-only local relay and raw TCP Funnel. It remotely installs sealed-config headless Clients on EC2. The relay accepts direct Owner/Client CSRs, retains one Agent and multiple Clients, and migrates endpoints without replacing device keys.

**Tech Stack:** Go 1.26, Wails v2/React, AWS SDK for Go v2, Windows SCM/DPAPI, Tailscale Windows CLI, X25519/HKDF/AES-GCM, Kotlin/Android Keystore.

**Spec:** `docs/superpowers/specs/2026-08-29-local-funnel-shared-bridge-design.md`

## Global Constraints

- Work in the current tree; do not create a worktree.
- Write behavior tests first and observe the expected failure before implementation.
- Automatic EC2 support is `us-east-1`, x86-64 Windows Server 2019, and SSM only.
- Never replace an existing instance profile, create an EC2 instance, allocate a public IP, or add inbound EC2 rules.
- Never expose SOCKS beyond IPv4 loopback or log credentials, capabilities, keys, destinations, or payloads.
- Managed limit: ten EC2 Client nodes, four streams per Client, thirty-two total Agent streams.

---

### Task 1: Relay Bootstrap, Provisioning, Rotation, and Capacity

- [ ] Add failing relay service/CLI tests for direct Owner CSR bootstrap, Owner-authorized Client CSR provisioning, endpoint rotation under the same CA, Windows foreground/service separation, and thirty-two aggregate streams.
- [ ] Implement the minimal relay service, store, control API, CLI, and Windows SCM changes; retain existing enrollment and revocation behavior.
- [ ] Run `go test ./relay/...` and `go test -race ./relay/...`.

### Task 2: Sealed Configuration and Headless Client

- [ ] Add failing tests for X25519/HKDF/AES-GCM round trips and malformed, replayed, tampered, and wrong-key envelopes.
- [ ] Add failing tests for a Windows Client service that owns its key/CSR/credentials, accepts sealed configuration, binds SOCKS to `127.0.0.1:1080`, and connects using only a Client identity.
- [ ] Implement focused sealed-config and node-service packages plus the `mobile-egress-client` command.
- [ ] Run targeted Go tests, Windows cross-build, and `go vet ./...`.

### Task 3: Local Relay and Tailscale Controller

- [ ] Add failing tests around Tailscale status/Funnel JSON parsing, raw TCP command construction, installer verification policy, relay setup state, and loopback dial override.
- [ ] Implement the UAC helper boundary, local service setup, Tailscale browser-login/status flow, unattended mode, and raw TCP Funnel configuration.
- [ ] Extend the Wails API/UI with a first-run local bridge wizard and actionable health/repair states.
- [ ] Run controller Go tests and frontend typecheck/build.

### Task 4: AWS/SSM Multi-Node Management

- [ ] Add failing tests with AWS-boundary fakes for Identity Center/access-key authentication, Windows Server filtering, instance-profile safeguards, SSM readiness, node bootstrap, sealed configuration, update, and secret redaction.
- [ ] Implement AWS SDK adapters and a node orchestrator; persist controller credentials and per-node metadata with DPAPI.
- [ ] Add node inventory, install/repair/update, copy-proxy, and revoke/replace flows to the desktop UI.
- [ ] Run targeted controller tests and frontend typecheck/build.

### Task 5: Android Capacity and Endpoint Migration

- [ ] Add failing JVM tests for thirty-two-stream admission, bounded queues, migration QR parsing, CA/origin/expiry validation, one-use consumption, and atomic origin-only persistence.
- [ ] Implement migration networking/UI and raise Agent limits without changing cellular socket binding or Wi-Fi fail-closed behavior.
- [ ] Run Android unit tests, lint, and debug assembly.

### Task 6: Release, Documentation, and Verification

- [ ] Add signed Windows relay/node/admin-helper release manifest generation and verification tests.
- [ ] Update README, architecture, protocol, security, deployment, operations, status, and component READMEs for the local Funnel topology and friend quick start.
- [ ] Run `scripts/test-all.ps1`, Windows artifact builds, Android validation, `git diff --check`, and the documented physical acceptance checklist when devices/accounts are available.
- [ ] Review the final diff for secret material, unsupported claims, and destructive AWS/Tailscale behavior before committing.
