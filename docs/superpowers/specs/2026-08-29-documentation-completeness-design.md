# Documentation Completeness Design

**Date:** 2026-08-29  
**Status:** Proposed for implementation  
**Scope:** Documentation only. This work describes the implemented system accurately; it does not add missing product capabilities.

## Goal

Make the repository documentation trustworthy for four audiences:

- the relay administrator deploying and recovering the service;
- the Windows and Android user pairing devices and using the local proxy;
- the maintainer changing relay, protocol, desktop, or Android behavior; and
- the contributor preparing a reproducible local environment and validation run.

An instruction is documented as supported only when the current code and UI can perform it. A flow that is incomplete or needs out-of-band access is explicitly presented as a limitation, not implied to work.

## Documentation authority

| Document | Canonical responsibility |
| --- | --- |
| `README.md` | Product boundary, safety summary, glossary, prerequisites, task-based navigation, and concise current limitations. |
| `docs/architecture.md` | Component responsibilities, topology, trust/persistence boundaries, data flow, defaults, and observability surface. |
| `docs/security-model.md` | Threat model, authentication and enrollment boundaries, storage/trust assumptions, privacy, QR/credential handling, revocation scope, and fail-closed boundaries. |
| `docs/protocol.md` | Normative v1 control API, TLS/session model, WebSocket framing, directional message schema/state rules, limits, errors, and reconnect behavior. |
| `docs/deployment.md` | Relay host prerequisites, initialization, artifacts, signing/release, backup, rollback/compromise recovery, and cross-device acceptance. |
| `docs/operations.md` | Daily operation, restart/health interpretation, troubleshooting decision paths, partial setup/recovery, and explicit unsupported cases. |
| `windows-client/README.md` | Windows development and runtime behavior: Owner/Client separation, loopback SOCKS limits, QR semantics, tray lifecycle, and Client recovery. |
| `android/README.md` | Android development/install/release behavior: QR scan, permissions, foreground lifecycle, per-socket cellular binding, non-VPN boundary, and device-only validation. |
| `CONTRIBUTING.md` | Contributor bootstrap, source-of-truth hierarchy, validation matrix, dependency/secrets rules, and component ownership. |
| `docs/status.md` | Current automated validation, required device acceptance, and known product/documentation limitations. |

The dated design and plan documents remain historical records. Their status must be labeled so no reader mistakes an unchecked historical plan or old analysis for current behavior.

## Truth and security rules

- Do not publish invitation payloads, private keys, SOCKS credentials, or real relay values in examples, screenshots, logs, or command output.
- Treat an Agent QR code as secret material while valid. Regenerating the displayed code does not revoke an earlier unexpired code.
- Describe desktop state as Windows-current-user DPAPI protected, not protected against malware or a compromised interactive user session.
- Describe relay CA and SQLite state as filesystem-protected operational secrets, not encrypted-at-rest state. Backups require equivalent protection.
- Distinguish public TLS bootstrap/health endpoints from authenticated control and tunnel sessions. The public relay is never a public SOCKS endpoint.
- Describe Android routing precisely: the Agent binds its own relay, DNS, and target sockets to cellular; it does not install an Android VPN, alter the phone default route, or control unrelated phone traffic.
- Label physical relay/device tests as acceptance work that automated validation cannot prove.

## Current limitation policy

Documentation must make these conditions discoverable in `docs/status.md`, the relevant runbook, and relevant component documentation:

1. The shipped desktop UI does not expose creation/import of an additional Windows Client identity. Multi-Windows deployment is not a supported app-first workflow.
2. Android does not display its certificate serial and relay v1 has no identity-list endpoint. A lost-phone targeted revocation cannot be completed through the shipped UI; routine re-pairing does not revoke the prior Agent identity.
3. An Owner invitation is initially issued by relay initialization. Expiry, loss, or failed initial Owner bootstrap has no self-service renewal flow and needs an explicit relay-administrator recovery boundary.
4. The supplied Compose configuration builds from the current checkout rather than selecting a pinned image, so image-tag rollback is not an implemented operational workflow.
5. Eight active Agent streams are relay-wide capacity; one Windows Client locally caps streams at four. The documented physical check must not imply that a single shipped Windows Client can create all eight.
6. No CI, automated publishing, Android instrumentation run, Wails runtime test, or physical-device validation currently proves a release end to end.

No document may substitute invented commands for these gaps. Where an owner-only, out-of-band relay procedure is not specified by source, the documentation states that it requires maintainership intervention rather than fabricating a runbook.

## Content changes

### Core reference corrections

- Replace the obsolete implementation-state claims in `docs/analysis.md` with a clearly historical status pointer, and mark `docs/plan.md` plus dated completed plans as historical/completed without rewriting their original task history.
- Correct `docs/protocol.md` to define one JSON envelope per WebSocket binary message, directional `open` payloads, client-generated stream IDs, actual role/state transitions, limits, timeouts, queue behavior, endpoint/auth matrix, and the implementation-specific tombstone behavior.
- Correct `docs/architecture.md` to distinguish hard-coded defaults from configurable settings, describe public read-only health, and link to protocol/operations for endpoint/procedure details.
- Expand `docs/security-model.md` with the dual Owner/Client boundary, enrollment vs mTLS authorization boundary, QR visibility risks, local-adversary caveats, relay-state protection, revocation scope, and log-output exception for the one-time initialization invitation.

### Operator documentation

- Restructure `docs/deployment.md` around relay host readiness, TLS ingress, DNS/public-name consistency, state/backup custody, signed artifacts, and the canonical cross-device acceptance record.
- Restructure `docs/operations.md` around normal daily flow, system states, partial Owner-ready/Client-missing recovery, Client replacement, QR expiration/re-pairing semantics, health interpretation, restart rules, and an explicit limitations/escalation table.
- Use links rather than duplicate procedural text between deployment and operations.

### Platform and contributor documentation

- Make `windows-client/README.md` the concise authority for Windows runtime constraints: separate Owner/Client identities, loopback IPv4 SOCKS5 with local authentication, CONNECT-only behavior, four-client-stream limit, tray behavior, QR handling, and client recovery.
- Make `android/README.md` the concise authority for Android installation and development: permissions, QR-only pairing, visible foreground start/stop, no boot restart, service/task lifecycle, cellular per-socket scope, non-VPN boundary, release script, and device-only validation.
- Add `CONTRIBUTING.md` for `npm ci` bootstrap, tool/version requirements, Android SDK environment-variable precedence, local validation scope, Windows-specific Wails behavior, secret hygiene, and the documentation authority table.
- Add `docs/status.md` as the single current validation/limitations ledger, replacing stale analysis claims and avoiding repeated “not yet physically tested” text.

## Navigation and terminology

`README.md` gains a task-oriented documentation index: deploy relay, operate/recover, understand security, implement protocol, develop Windows, develop Android, and contribute.

It also defines these terms:

- **Owner** — the privileged relay certificate role held by the Windows owner application.
- **Client** — the certificate role used by the local Windows SOCKS tunnel, distinct from the Windows application as a whole.
- **Agent** — the Android certificate role/application that supplies cellular-bound egress.
- **Relay administrator** — the person with host/relay-state access; this can be the owner but has distinct operational authority.
- **Agent QR code** — a displayed, short-lived Agent invitation; it has no text fallback in the Android application.
- **Local SOCKS5 proxy** — the authenticated listener on Windows loopback, not an Internet-facing relay service.

Every canonical document adds a short related-documents navigation line. Component READMEs link to the canonical system and runbook documents rather than duplicating their full policy.

## Validation

Documentation is validated by:

1. Source-citing every behavior or command that could affect security, routing, enrollment, deployment, or release.
2. Checking internal links and commands against the current repository layout/scripts.
3. Running the documentation link/terminology scan added by this work, if feasible without adding a dependency; otherwise use repository search plus `git diff --check`.
4. Running `scripts/test-all.ps1` unchanged to show the documentation work did not disturb application sources or build configuration.
5. Reviewers confirming that unsupported journeys are labeled as limitations and no secret-bearing example was introduced.

## Non-goals

- Implementing Agent identity discovery/revocation, additional Windows Client enrollment, Owner renewal, image-tag rollback, CI, signing automation, or Android instrumentation tests.
- Replacing source-of-truth behavior with documentation claims.
- Publishing production secrets, real endpoints, or certificate material.
