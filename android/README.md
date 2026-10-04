# Android direct cellular Agent

Android 10+ (API 29+) supports up to ten direct workload Clients using one owner-started foreground service. The phone initiates every connection over cellular. Targets also use cellular; loss of cellular closes traffic even when Wi-Fi is available. The phone accepts no inbound connections.

## Version 2 migration

Version 2.0.0 is a direct-only major-version change. Old relay/Funnel enrollment and endpoint-migration QRs are rejected. On upgrade, encrypted old identity data is retained for recovery but removed from active configuration. The app displays that each workload Client must be paired again. It never reconnects to the old relay. Downgrading is unsupported.

## Pair workload Clients

1. Install and configure the version-2 Client on each workload machine. Its advertised HTTPS endpoint must be reachable from the phone's cellular network and match its certificate. This first direct release does not provide NAT traversal or an ingress relay.
2. Generate an invitation on that Client. On Android, choose **Scan QR**, or paste the invitation in the direct Client import field.
3. The app stores a non-exportable P-256 Android Keystore key and pending CSR before enrollment. It pins that Client's CA, validates the endpoint hostname/IP, stores the issued identity before acknowledging, and retries interrupted delivery with the same key.
4. Use **Retry** for an interrupted pairing or acknowledgement. Ten records are allowed, including pending and disabled records. **Remove** releases a phone slot; revoke the previous phone on the workload Client before pairing that workload again.
5. Tap **Start**. Each enabled Client receives its own authenticated direct connection. Configure the intended application to use the authenticated local proxy on its own workload machine: Windows uses `127.0.0.2`, Mac uses `127.0.0.1`, HTTP/CONNECT port `1081`, SOCKS5 port `1080`.

**Disable** stops one Client without removing its pairing. **Stop** stops all phone connections and remains available while the service runs, including pairing/import and removal of the last Client. Removing a Client closes its streams and forgets its phone-side key; workload-side revocation remains a local Client administrative action. Saved names and endpoints are not included in copied diagnostics.

The encrypted direct registry uses an atomic file in app-private, non-backup storage, with file and directory durability barriers before state becomes eligible for acknowledgement. It does not treat a SharedPreferences in-memory update as committed credentials. An uncertain reservation keeps its key until a durable read proves it unreferenced. Existing encrypted registry and legacy recovery data migrate before old active preference records are retired; failed retirement remains retryable and blocks completion.

**Remove** stops traffic before saving the removal. If credential storage fails, the record and key stay intact but the Client displays **Stopped · Removal pending**. A separate private, nonsecret stop latch prevents reconnection after **Start** or app restart. Use **Retry Remove** when storage recovers, or explicitly choose **Enable** to trust that saved Client again. If even the stop latch cannot be saved, the app reports that the stop is limited to the current session and must be retried before restarting the app. Other Clients continue independently.

## Endpoint recovery and renewal

The Agent polls direct configuration when connecting and every 30 seconds while active. A signed endpoint update is bound to the Client ID and pairing ID, preserves trust and keys, and uses a monotonic generation. Missed generations are supported; stale, conflicting, tampered, and wrong-peer updates are rejected.

When the old endpoint is unreachable, import the workload Client's connection update through **Scan QR**, paste/import, or **Import update file**. Desired state is saved before the new endpoint is contacted, so an unavailable endpoint remains visibly pending and can be retried. Certificates with less than seven days remaining are renewed using the same key; expired trust requires fresh pairing. Renewal cannot acknowledge an endpoint generation the phone has not received in a verified update.

Each Client shows its own recovery action for rejected/expired credentials, unavailable phone storage, incorrect clocks, unavailable workload storage or a transient connection failure. Unlock/recover phone storage and use **Retry** when prompted. Re-pair only when the existing trust is unusable; a timeout is not proof of revocation. Other Clients continue. See [operations and recovery](../docs/operations.md) for the complete recovery procedure.

## Background operation and IP rotation

Android keeps direct sessions in its visible foreground service, including when the app UI is backgrounded. Start is explicit; `START_NOT_STICKY` does not promise automatic reboot or force-stop recovery. After reboot, open the app and choose **Start**. The notification provides **Stop**.

**Rotate cellular IP** is global: confirm interruption of active streams, follow Android's public Airplane Mode settings, wait for the displayed hold interval, then restore cellular. The Agent probes before/after, restores enabled Client connections, and reports Changed, Unchanged, or Unverified without copying the addresses. A normal attempt holds for ten seconds; unchanged results offer a 30-second retry. Cancellation and timeout restore ordinary operation where cellular is available. Removing or disabling a Client during rotation prevents its reconnection.

The approved platform exception is explicit: this Android version supports background work through its foreground service, while the corresponding first direct iOS release operates only while its app is active in the foreground. Do not describe their background guarantees as equivalent.

## Bounds and security

Each phone has separate 8,192-frame/64-MiB target-bound and Client-bound data budgets shared by all sessions, including queued and transport-owned debt. Each stream retains at most 32 data frames. Live stream ownership has no fixed count ceiling; device sockets and memory remain finite, and these lane limits are not a total process-memory bound. Required controls and reactor control commands are globally bounded. Native read turns are FIFO across contending peers; idle saved Clients reserve no capacity. Existing per-stream mailbox fairness and EOF ordering remain.

All peer HTTP, TLS, WebSocket, and target traffic uses the selected cellular Network. Direct TLS requires TLS 1.3, the pinned private CA, normal hostname/IP verification, and mTLS after enrollment. HTTP redirects are disabled. Production sessions use only `/v2/direct/session?transport=2` with the `direct/1` protocol header; there is no relay fallback. Target destinations retain the public-address policy. Copied diagnostics contain finite health and count information, never capabilities, certificates, endpoint addresses, target content, or compared public IPs.

## Verification and outstanding acceptance

Run `gradlew.bat testDebugUnitTest lintDebug assembleDebug` with JDK 17 and Android SDK 35. The repository mobile manifest is maintained alongside iOS and validated by the shared component gates. Source/unit/lint/debug-build success is not a signed release, physical cellular test, or background acceptance.

Physical acceptance must cover real no-relay HTTP/CONNECT/SOCKS traffic, ten simultaneous Clients, cellular loss with Wi-Fi present, screen-off/background operation, rotation, restart with explicit Start, signed upgrade migration, secure-storage failure, revocation, endpoint recovery, and sustained resource use. No connected Android device was available during this implementation's initial direct-mode checks. Preserve the established APK signing identity; version 2.0.0 uses versionCode 21 pending release-history reconciliation before publication.
