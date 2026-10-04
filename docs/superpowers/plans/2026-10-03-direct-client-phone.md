# Mobile Egress 2.0: Direct Client-to-Phone Operation

Approved by the owner on 2026-10-03. Implementation runs in the existing checkout on **main**. This supersedes the personal-computer relay requirement and the AWS-optional controller design.

## Product contract

Application → local Client HTTP/CONNECT or SOCKS proxy ⇄ phone Agent → cellular Internet. The phone initiates one authenticated outbound connection to each reachable workload Client. No central Desktop controller, AWS integration, Tailscale, Funnel, relay fallback, or inbound phone listener remains in the new product.

- One paired phone per Client; ten saved Clients per phone, including disabled and pending records.
- Windows x64 10/11 and Server 2019+, Apple Silicon macOS 13+, existing Android minimum, and iOS 17+ remain targets.
- Listener default TCP 8443; configurable bind address and advertised HTTPS origin/public port. Public address or forwarding is required; no automatic router or cloud ingress changes.
- Local proxy addresses remain Windows 127.0.0.2 and Mac 127.0.0.1, HTTP 1081 and SOCKS 1080. No public proxy, UDP/QUIC, system proxy, or route changes.
- Android runs an owner-started foreground service. Reboot/force-stop require another Start.
- iOS serves in the active foreground app. Keep screen awake while sharing defaults on, prevents only idle auto-lock, and restores the normal idle timer when no longer applicable. Manual lock, app switching, and inactive/background phases close traffic. This is an explicitly approved lifecycle parity exception.
- Major-version migration requires fresh direct pairing. Private unreachable Clients, NAT traversal, Linux, Intel Macs, and multiple phones per Client are deferred.

## Implementation checklist

Checked implementation items mean source and applicable automated tests are present. They do not assert physical, installed-service, signed-release or performance acceptance. Those gates remain separately recorded below and in [the acceptance record](../../direct-acceptance.md).

### 1. Document and preserve

- [x] Save the approved plan before implementation.
- [x] Inventory existing dirty work and save a binary diff/status/HEAD and benchmark copy under ignored `.local/direct-v2-2026-10-03/`.
- [x] Maintain this checklist, implementation rulings, validation evidence, and release blockers.

### 2. Direct Client service

- [x] Preserve HTTP/CONNECT/SOCKS adapters behind the existing Tunnel interface; replace relay dialing with an accepted phone session.
- [x] Add configurable authenticated TLS listener, explicit listening/awaiting-phone/connected/error status, and cellular enrollment reachability proof.
- [x] Keep administration on protected local IPC and proxies loopback-only; bound public admission and requests.
- [x] Preserve Windows service-account DPAPI and Mac LaunchDaemon/file-based System Keychain, native ownership, boot/logout and repair implementation. Signed installed-service acceptance remains pending.
- [x] Add narrowly scoped host firewall setup, port conflict errors, and manual router/cloud ingress guidance.

### 3. Trust and recoverable pairing

- [x] Create independent per-Client authority/server keys, stable Client identity, and ten-minute one-use direct QR invitations.
- [x] Phone persists a non-exportable per-pair key before enrollment; Client persists CSR-bound redemption and credentials before delivery; phone persists credentials before acknowledgement.
- [x] Make interruptions and lost acknowledgements idempotent; cancellation/expiry releases pending slots without duplicate keys/identities.
- [x] Enforce one phone per Client and atomic ten-record phone limit. Replacement revokes the old pairing explicitly.
- [x] Bound handshake/request/admission resources; keep capabilities, credentials, and raw sensitive failures out of diagnostics.

### 4. Transport reuse

- [x] Extract reusable public-destination policy, bounded DNS, framing, cancellation, and bridge behavior into shared packages before relay retirement.
- [x] Resolve on the workload and preserve independent Agent candidate validation.
- [x] Negotiate direct protocol explicitly while reusing binary framing/control semantics; incompatible versions fail clearly.
- [x] Preserve existing uncommitted exact-byte/EOF, cancellation, and per-stream backpressure fixes.
- [x] One session per Client; no fixed throughput throttle or arbitrary active-stream ceiling.

### 5. Multi-Client phones

- [x] Transactional Client registry: stable identity, display name, trust/key reference, endpoint generation, enabled and pairing state.
- [x] Independent peer sessions/retries/failures under one supervisor; namespace stream ownership by Client and session generation.
- [x] Phone-global directional budgets of 8192 frames/64 MiB, 32 frames per stream, including in-flight ownership; no tenfold multiplication.
- [x] Cross-Client fairness and bounded control processing; teardown refunds every reservation exactly once.
- [x] Cellular-only tunnels and targets, no Wi-Fi fallback; Client list, per-Client controls, global Start/Stop and rotation.

### 6. Lifecycle and keep-awake

- [x] Android foreground service remains owner-started with independent reconnect on cellular recovery.
- [x] iOS main-app runtime stops sessions/targets on inactive/background, resumes on active only with retained Start intent, and clears intent on Stop.
- [x] Persist Keep screen awake while sharing, default true; disable idle timer only when preference && Start intent && active, including temporary reconnects.
- [x] Restore idle timer on Stop, preference off, inactive/background, or terminal failure; reapply only when conditions hold. Never change brightness or prevent manual lock.
- [x] Display Sharing — keep this app open, current auto-lock behavior, and the pause consequence when keep-awake is disabled.
- [x] Retire packet-tunnel serving, retaining only fail-closed migration scaffold/signing access needed to disable/remove app-owned VPN profiles. Block Start if cleanup is unconfirmed; do not touch other VPNs.

### 7. Management and recovery

- [x] Client UI: endpoint setup, Pair phone QR, status, HTTP/SOCKS copy, connection update export, Remove paired phone.
- [x] Protected IPC: configure endpoint, issue/cancel invitation, status, proxy copies, export update, revoke.
- [x] Authenticated signed updates bound to Client/pairing and monotonic generation; connected delivery plus QR/file recovery, skipped generations accepted, stale/conflicting/trust-changing updates rejected.
- [x] Pending updates remain visible until acknowledgement. Persist revoke before success and close traffic immediately; phone removal stops/distrusts only that Client, including failed credential-storage writes with visible pending cleanup.
- [x] Authenticated certificate renewal; unrecoverable expired credentials, authority replacement, or lost trust require re-pairing.

### 8. Migration and retirement

- [x] Transactional standalone/EC2 local migrations preserve usable IDs, proxy credentials, service ownership, and protected recovery state.
- [x] Support deliberate local migration of known AWS-installed Windows service without AWS calls.
- [x] Show Migration required; reject legacy QR/configuration; no relay reconnection or silent downgrade reuse.
- [x] Retire controller/AWS/relay/Funnel code and dependencies only after preserving reusable behavior/tests.
- [x] Retire former controller computers separately, never automatically expose them as workload servers.
- [x] Cleanup only exact app-owned startup/Funnel configuration; preserve unrelated Tailscale state.

## Documentation and distribution

- [x] Rewrite AGENTS, README, architecture, security, protocol, operations, installation, platform guides, status, troubleshooting, and acceptance docs for direct operation.
- [x] Explain reachable endpoints, cellular routing, local maintenance, re-pairing, and dedicated foreground iPhone operation with keep-awake; manual lock/app switch still interrupts service.
- [x] Preserve historical plans/benchmarks with historical-topology labels; record direct performance separately.
- [x] Extend mobile manifest/schema/validator with the explicitly approved lifecycle exception and tracked source/test/decision evidence. Never call foreground iOS background-equivalent.
- [x] Make signed MobileEgressClientSetup.exe and notarized mobile-egress-client-macos-<version>-arm64.pkg the primary 2.x release contract; retain signer/service/storage identities. Actual signing/publication remains gated.
- [x] New major release excludes controller/raw EC2 artifacts; update guarded scripts/downloads/contracts while preserving historical releases.
- [x] Coordinate compatible Android/iOS builds; never fall back to old Agent releases.

## Verification and completion gates

- [ ] Physical Android/iOS HTTP, CONNECT, SOCKS through direct Windows/Mac Clients with relay offline and no AWS/Tailscale dependency.
- [x] Automated expiry/replay/wrong-key, 10/11 reservation, cancellation, persistence failure, lost ACK, revoke, renewal and safe-diagnostics checks. Physical matrix remains pending.
- [x] Automated exact bytes/EOF, slow readers, duplicate IDs across peers, isolation/fairness/control responsiveness, global debt bounds and cleanup. Sustained physical soak remains pending.
- [x] Automated missed endpoint generations, stale updates, storage failures, occupied ports and invalid-certificate checks.
- [ ] Installed-device repeated/offline endpoint recovery, unavailable secure storage, occupied ports and blocked external ingress.
- [ ] Signed Windows/Mac upgrade/repair/boot/logout and EC2 service migration.
- [ ] Android background/cellular recovery; iOS inactive/lock/switch/Stop/resume and legacy VPN cleanup.
- [ ] Physical iPhone short auto-lock interval: sustained no-touch traffic beyond interval with keep-awake on; normal auto-lock after off/Stop; manual lock closes traffic; active return respects Start intent.
- [ ] Single/ten-Client sustained transfer measurements: throughput, latency, memory, CPU, fairness, thermal behavior; no predetermined Mbps promise.
- [x] Revised Windows/frontend/release checks, Android unit/lint, Mac/iOS native tests and manifest validation, including final mobile removal corrections.
- [ ] Signed physical acceptance before release-ready downloads.

Completion requires functioning direct Clients and both phones, documentation, passing applicable gates, and explicit remaining signing/physical-device blockers. Source implementation is not physical acceptance or publication.

## Execution record

- 2026-10-03: Confirmed main; baseline dirty performance work backed up before edits. No separate worktree, as expressly requested.
- Ruling: parallel platform work uses disjoint ownership and a shared wire contract. The developer's parallel-delegation instruction takes precedence over the skill's generic serial-implementer preference.
- Ruling: retain historical release behavior/tests for old versions; 2.x artifacts are direct-only. Local migration has no automatic cloud or router side effects.
- Source implementation: direct TLS/mTLS service and local management; ten-Client mobile registries/supervisors; shared phone budgets; Android foreground service; iOS active-app serving/default-on keep-awake and owned-VPN cleanup; native Client migration; retired production controller/AWS/relay/Funnel; revised primary downloads and documentation.
- Ruling: direct protocol and protected IPC interfaces are specified in [direct-protocol-v2.md](../../direct-protocol-v2.md). Renewal responses report only the currently acknowledged endpoint generation. Signed config polling at connection and every 30 seconds supplies live updates; export handles endpoints that can no longer reach the old address.
- Ruling: usable legacy UUIDs and proxy credentials survive local migration. Known AWS-installed Windows services retain their existing protected state directory and service account in place; unknown ownership/path combinations are rejected. Fresh phone pairing is mandatory.
- Ruling: phone slots with unknown enrollment outcomes retain the same non-exportable key/CSR for recovery, even beyond local invitation expiry. Definitely-unsent expiry or an exact authenticated enrollment rejection can remove only the matching unissued reservation. Record removal commits before key deletion.
- Ruling: fairness schedules actually ready work; idle Clients reserve no static share and no fixed one-tenth quota exists. Per-stream backpressure and shared queued/in-flight accounting remain. Exhausted aggregate capacity retains stream-local failure, with no bandwidth guarantee.
- Ruling: historical relay dialing helpers are test-only; legacy protected-data readers and inert 1.x release fixtures remain for migration/tests. They do not provide a production fallback. Prior relay-specific dirty fixes are retained in `docs/benchmarks/historical-relay-bridge-fixes.patch` as well as the original ignored snapshot.
- Review corrections: lost renewal response after an endpoint ACK; delayed mobile callbacks after Stop/disable; uncertain versus expired enrollment reservations; native reservation refund ordering; iOS rotation recovery; stale Client invitation QR; recoverable listener conflicts; serialized firewall configuration; accurate listener status; direct release download scope text; immediate phone-side removal despite failed credential writes, with durable independent stop intent and safe orphan-marker cleanup. Regression tests and final focused review passed.

## Validation results / blockers

### Follow-up review corrections (complete)

The owner's subsequent whole-plan review reopened the following source requirements. Earlier passing tests did not cover these integration and persistence boundaries; previous review completion does not supersede this list.

- [x] Android: replace the registry's SharedPreferences commit boundary with durable encrypted transactions; cover writes visible in memory but not committed, retry/restart, and key retention after an uncertain reservation. Archive recovery before retiring old active records; share a committed generation to avoid disk sync/decryption during unchanged traffic-status refreshes.
- [x] Client: normalize equivalent IPv6 authorities and numeric ports for configuration and endpoint acknowledgements while continuing to reject a different host or port. Preserve existing signed payload spelling at its generation.
- [x] iOS: reconstruct a recovered rotation pause before reconciling persisted Start intent after process relaunch; reject delayed activation work after inactivity, including a suspended registry read.
- [x] iOS: route an unchanged-IP retry through the required 30-second action rather than repeating the rejected 10-second action.
- [x] Android: retain dashboard Stop while the foreground service runs, including management operations and removal of the last saved Client.
- [x] Both phones: expose safe, actionable per-Client authentication, trust, protected-storage and network recovery guidance; Client storage downtime must not masquerade as revoked pairing. Ignore late callbacks from closed Android sessions.
- [x] Rerun applicable native and cross-platform gates, independently review the corrections, and update tracked parity and acceptance evidence.

See [the acceptance record](../../direct-acceptance.md) for exact commands, snapshot hashes, limits and remaining gates. Windows integration, five Client UI tests, release contracts, schema/manifest tests, native Mac Go tests/vet/race/builds, Android 279 tests/lint/debug build, and iOS 349 tests with two hardware skips plus unsigned app/extension builds passed. Android lint has zero errors and 18 warnings. Follow-up review corrections have red/green regression evidence and fresh final gates; independent review found no additional defect in the reviewed corrections.

Physical direct traffic, installed native service/storage behavior, signed upgrades/repair, iPhone keep-awake and interruption acceptance, and one/ten-Client sustained performance are NOT RUN. No Android device is connected; no physical iPhone/iPad or iOS development signing identity was found. No 2.x artifact was signed, installed or published. These blockers prevent release-ready completion; neither loopback/fixture checks nor historical relay measurements substitute for them.

### Second review corrections

The owner authorized addressing all five findings from the next review on main. These are bounded corrections to the approved interfaces and lifecycle contract; this section and the latest acceptance results supersede the preceding validation for affected code.

- [x] iOS: serialize IPv6 HTTP authorities with exactly one bracket pair and pass an unbracketed address to socket/TLS validation. Cover the actual HTTP encoder and pinned transport configuration.
- [x] iOS: retain observed cellular loss/return across inactive rotation without running sharing, probes or timers while inactive. Cover a full Control Center cycle, elapsed hold, missed observations, cancellation and stale work.
- [x] Android: make the dashboard Retry action restart eligible paused peers even when its one-shot maintenance request encounters a transient failure. Preserve disabled/removing and terminal trust protections.
- [x] Both phones: verify signed endpoint bytes before normalization, accept equivalent retained endpoint spelling, and preserve monotonic generation, identity and trust checks. Cover retained padded ports, equal-generation retries and conflicting updates.
- [x] iOS: restore independently observed cellular availability in the actual dashboard and safe status, with production-bound adapter tests and corrected parity evidence.
- [x] Run affected mobile/native gates and a scoped independent review; record exact results and unchanged physical/signing blockers in the acceptance record.

The endpoint regressions now consume actual Go-exported retained-state bundles, then exercise phone persistence and acknowledgement. Equal-generation normalization preserves pending acknowledgement and protected identity references. iOS adapts only transient Keychain lookup/staging values so valid older endpoint spellings remain usable during renewal; saved certificates, keys and trust are not replaced. Native adapter tests use disposable labels and cleanup, and do not establish entitled installed-device secure-storage acceptance.

Android Retry now obtains acceptance from the live service before handing it ongoing recovery; a stopped service performs one explicit attempt and uses manual-retry wording. iOS passively records delivered path transitions without interpreting their live effects until activation. When iOS misses observations entirely, the UI guides returning with Airplane Mode on for the countdown rather than assuming a completed cycle. The production status adapter displays Unknown until a path observation arrives and keeps cellular availability separate from sharing and per-Client status.

Final validation: Windows integration passed; Android 289 tests, zero failures/errors/skips, lint zero errors/18 warnings, debug build passed. Final native iOS Swift warnings-as-errors and Xcode package runs each passed 361 tests with two hardware skips, and unsigned iPhoneOS/Simulator app plus cleanup-extension builds passed. The 131-file source snapshot was hash-verified. Mobile manifest/schema, whitespace and scoped independent review passed. The physical/signing/performance blockers above remain unchanged; these fixes did not publish or sign a release.
