# Inevitable hosted connectivity acceptance

Implementation record opened 2026-10-04 under the [approved plan](superpowers/plans/2026-10-03-inevitable-hosted-connectivity.md). Work uses feature branches in the existing checkouts. The preserved direct baseline is `14c9d35`; independently reviewed Inevitable Core CONNECT fixes are `43a66d43`. No deployment, merge or release is part of this change.

## Contract and review

New Client setup uses outbound Inevitable Gateway by default; existing direct configurations are retained until explicitly changed. Hosted mode exposes no public workload listener or firewall rule. The existing Client authority/phone pairing terminates inner TLS; one outbound yamux attachment carries virtual phone connections. Initial account access is an admin pilot grant. Phone apps have no Inevitable login. Mobile sends no traffic-usage/accounting events; commercial proxy usage remains unchanged.

Both phones persist explicit transport mode with old records defaulting direct. Signed updates cover exact mode/endpoint bytes, Client/pairing identity and generation. Go-generated hosted signatures are tested by Android and Apple Security. A negotiated bounded live control delivers updates before the old listener is replaced; phones save before reconnecting. Offline/older peers use QR/file recovery. Shared budgets, cellular-only sockets and the Android/iOS lifecycle exception remain unchanged.

Independent reviews found and prompted regression fixes for concurrent gateway claim/install ordering, authorization expiry delayed by blocked backend calls, stale old-mode sessions falsely verifying new setup, dropped gateway authorization-rejection guidance, and the wizard remaining on activation after browser approval. Real TLS socket tests now distinguish confirmed access rejection from configuration expiry/backend outages. Wizard review continues without replacing activation, and delayed approval preserves Finish later and explicitly selected direct settings. Source review does not substitute for the physical gates below.

## Validation record

- Preserved baseline Windows/Android gate passed before the first commit.
- Inevitable Core buffered-byte and EOF regressions failed before repair and passed afterward; accounting byte assertions and existing gateway suite passed. See sibling `core-transport-report.md`.
- Inevitable backend: 189 suites / 2,339 tests passed; 13 opt-in suites / 131 tests skipped. Root contracts/backend/UI build passed; initial focused UI regression coverage passed 59 tests. Centralizing Mobile hooks in the required `ui/src/api/queries.ts` was followed by 47 relevant tests and another complete build. See sibling `backend-report.md`.
- Gateway and connector socket tests, vet/build, bounded SNI fuzz seed tests and a single-worker fuzz run (43,855 executions) passed. Tests include opaque pinned TLS, private cross-node forwarding, exact bytes/half-close, cancellation, reconnect, owner replacement and expiry. Native race qualification passed as recorded below.
- Default-off infrastructure isolation, actual drain script with local AWS recorder, existing release/scaling contracts, Bash parsing and full Terraform validation passed. Two offline provider-mocked plans cover disabled/enabled settings, both Core ASGs and EC2 user-data bounds. These commands make no production change.
- Windows/Android integration: Go tests/vet/build, installer/release contracts and 36 Client frontend tests passed. Final wizard review added four cases; two reproduced the missing continuation before repair, and all 40 frontend tests passed afterward, including a refreshed complete Windows gate. Android passed 293 unit tests with zero failures/errors/skips, lint zero errors/18 existing warnings, and debug APK build.
- Native Swift warnings-as-errors and Xcode package tests: 366 tests, two hardware skips, zero failures. Final unsigned iPhoneOS and simulator app/cleanup-extension builds passed. Input archive SHA-256 `16aba0432d344d02f18c8e14d2715cbf40d417751b1c07528b27334681b57f08` matched before extraction.
- Native Apple Silicon pinned Go 1.26.7: full Mobile tests/vet/race and both gateway module tests/vet/race/build passed. Unsigned Mac daemon/GUI builds were verified arm64 with minimum macOS 13.0. Mobile input archive SHA-256 `d976679446cba10d28b08107b1a8a993393d13279ba200bf790a5bf0e73314d9`; details in sibling `native-go-report.md`. Installed secure-store/signing acceptance is still separate.
- Final gateway negative-admission wire fix was refreshed and hash-verified on Mac (`07b729fe1f0bef95a40dc82fc96b04a91db7d0b9c73760a72766758d8068b2aa`); uncached full tests/vet/race/build passed again. Confirmed rejection sends only a bounded negative acknowledgement; expired configuration, backend outages and ownership conflicts retain unavailable status.
- Final wizard assets were independently rebuilt on Mac from verified snapshot `ff8de494b02de011ec37631370656cfe4e757e42ab7c33c8040fceeb9c44f93c`: Client GUI tests/vet/race, all 40 frontend cases and production GUI build passed; arm64/minimum macOS 13.0 confirmed. Logs were copied to the local evidence directory before removing owned native scratch directories.
- Changed-document relative file-link check passed (60 links at that checkpoint), and manifest/schema and whitespace checks passed. Final review corrections are recorded before branch commits.

## Required deployment/release blockers

| Gate | State and required evidence |
|---|---|
| Real PostgreSQL concurrency | BLOCKED: no Windows/Mac localhost Postgres/test database; Docker Desktop API returns 500 and its service is stopped. Four dedicated opt-in tests remain skipped. Synthetic service/HTTP tests do not prove real SQL locking. |
| Deployment and recovery | NOT RUN: reviewed AWS plan, image/Compose startup, actual cross-node loss/scale-in, private TLS renewal, backend outage and configured revocation timing. Infrastructure remains disabled. |
| Physical traffic | NOT RUN: Windows/Mac with Android/iPhone HTTP/CONNECT/SOCKS over cellular through Gateway, with no router changes; include IPv6/NAT64 and offline mode recovery. |
| Signed installers | NOT RUN: Windows install/upgrade/repair/boot/logout/DPAPI and Mac notarized PKG/LaunchDaemon/file-based System Keychain acceptance with established identities. |
| iOS and Android lifecycle | NOT RUN on hardware: Android screen-off/cellular recovery, iOS short-auto-lock no-touch keep-awake, manual lock/app switching, Stop/resume and legacy VPN cleanup. |
| Capacity and coexistence | NOT RUN: sustained one-/ten-Client throughput, latency, fairness, CPU/memory and thermal measurements, ordinary proxy usage intact, no Mobile usage submissions, and at most 5% agreed Core throughput/p95 regression under combined load. |

Downloads remain pre-release and are not release-ready. Historical relay/direct benchmark results remain historical; no Mbps rate is promised. Use [capacity acceptance](capacity-acceptance.md) and the [physical record](templates/physical-acceptance-record.md) for sign-off.

## Rollback

Disable the separate Mobile service/listener/target registration without changing Core health ownership or commercial proxy paths. Retain additive backend records and signing/service identities. Existing direct Clients stay direct. A hosted Client returns to direct only through an explicit local change and signed phone update; gateway outage never opens ingress or automatically downgrades trust. Reverting source is not authorization to drop account or pairing records.
