# Controller status monitoring

Windows and macOS controllers collect status once at startup and every four
seconds. The window and tray/menu bar read the same sanitized snapshot. Reads
never invoke Tailscale, contact the relay, or access Keychain/DPAPI. The existing
bridge, managed-node, and reservation getters remain snapshot-backed wrappers.

Tailscale, helper, relay health, and managed-node metadata update independently.
Each component has one worker slot and at most one pending refresh. A slow
native call retains its slot until it returns, including after cancellation.
Initial status is **Checking**. Results become stale after twelve seconds without
a successful check; the last information remains visible alongside freshness
and a sanitized error. Failed, stale, or invalidated bridge checks cannot report
**Ready**. Node metadata has independent freshness and does not gate local bridge
readiness or prevent node installation from refreshing its own metadata.

Actions cancel obsolete observations and invalidate their generations. Known
outcomes, including Login Items approval state, publish immediately. Setup,
repair, and rotation retain their fresh signed-helper authorization gates; node
installation also performs a live bridge preflight before remote installation.
Display readiness is not authorization.

Owner identity comes from the already-loaded internal Core state and never enters
the Wails snapshot. The relay-health worker owns one reusable authenticated HTTP
client and replaces it when any identity field, trust material, endpoint, or
loopback dial override changes. Managed-node views and reservations use one
repository lock and one secure-store read per refresh. Views remain redacted.

## macOS trust cache

A successful full Tailscale verification retains its execution guard for sixty
seconds, measured from successful verification, not the last status read.
Disconnected status is a valid observation and can reuse that verification.
Every CLI invocation still revalidates the executable, protected path, and open
descriptor identity, including hashing the executable. Expiry, executable/path
change, verification failure, or failed CLI work invalidates the cached guard.
Installation checks and mutating Tailscale actions always verify fresh; actions
discard their verification afterward, including on failure.

Accepted bundle identifiers, pinned signers, path protections, and fail-closed
behavior are unchanged. The agreed tradeoff is that changes elsewhere in the app
bundle may remain undetected until the next full verification, up to sixty
seconds later. This is not a cache of permission to run arbitrary executables.

## Shutdown

Quit cancels the app lifetime, stops scheduling and menu updates, and rejects late
results. The monitor waits at most one second. An uninterruptible native operation
owns its resources until it actually returns; background cleanup then closes idle
health connections and retained verification guards. Core cleanup and main-thread
menu removal remain in place. Interrupted node installation keeps an independent
ten-second reservation-cleanup budget so cancellation does not itself abandon a
durable reservation.

See [native measurements and validation](controller-status-measurements.md) for
the exact revisions, environment, results, and remaining native coverage limits.
No wire protocol, persistent schema, mobile binary, or release process changed.
