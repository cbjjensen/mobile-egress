# Architecture decision: direct Client-to-phone

On 2026-10-03 the owner explicitly retired the permanent personal-relay requirement. The 2.x design uses reachable workload endpoints and outbound cellular phone sessions, with one phone per Client and ten saved Clients per phone. Central Desktop/AWS management and Tailscale/Funnel are removed from the new product.

The observed personal-relay uplink bottleneck motivated the change; historical measurements do not establish direct-mode performance. Removing that hop does not remove the phone cellular-upload or workload-network constraints.

The owner approved Android foreground-service background operation and iOS active foreground operation, with default-on idle keep-awake during sharing. Manual lock/app switching still pauses iOS. No unsupported packet-tunnel proxy lifecycle remains as a background promise.

See [the approved implementation plan](superpowers/plans/2026-10-03-direct-client-phone.md), [architecture](architecture.md), [protocol](direct-protocol-v2.md) and [status](status.md). Older dated alternatives are retained as historical evidence rather than current deployment instructions.
