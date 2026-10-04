# Hosted transport additions to direct v2

The direct v2 TLS enrollment, certificate roles, pairing authority, inner WebSocket handshake/framing, limits and signing domain remain unchanged. Hosting carries that TLS byte stream through the workload Client's outbound gateway attachment. The phone still connects over cellular to the pinned Client certificate for the invitation/update endpoint. It never receives Inevitable account or gateway credentials.

## Mode metadata

Invitations and signed endpoint-update payloads add optional exact key `transport` with values `direct` or `hosted`. Absence means `direct` for existing v2 records/bundles. Reject null, unknown values, duplicate keys and invalid spellings. Go emits `transport:"hosted"` for hosted bundles; direct bundles may omit the key to retain compatibility with existing direct-only readers. Hosted invitations require a new app version that understands the field; older strict readers must not reinterpret them as legacy relay bundles.

Saved phone records persist the effective mode. Reservation copies invitation mode. Signed update application changes endpoint and mode atomically; a change to either at the same generation is a conflict. Larger generations may skip earlier generations, preserve Client/pairing/key identity, and retain acknowledgement recovery. An old asynchronous operation must not overwrite a changed mode/endpoint. Supervisor restart comparisons include mode. Mode never changes network selection: both the control connection and destination sockets remain cellular-only.

Workload configuration stores an optional mode beside existing bind/endpoint/display name; absent mode on existing records remains direct. Unconfigured fresh UI defaults hosted. Activating hosting uses a separate broker endpoint for the workload attachment and a route endpoint for the phone, issues a server certificate from the existing Client CA, advances the configuration generation, clears stale invitations and retains signed-update recovery. The protected service owns activation verifier/poll secret/device token. UI/status/bundles expose none of them.

## Backend/connector boundary

See sibling Inevitable requirements `2026-10-03-inevitable-hosted-connectivity/interfaces.md` and `transport-interface.md`. The backend's authorized device-link exchange must distinguish `brokerEndpoint` (outer TLS server) from `gatewayHostname`/port (phone route endpoint). The connector supplies a virtual `net.Listener` to the existing Client TLS server. Its outer system-trusted TLS and scoped activation token do not replace the inner Client-pinned TLS or phone mTLS.

Gateway status is distinct from paired/live phone state. Hosted mode never performs inbound firewall changes, public address lookup, or local public-port binding. Direct mode remains an explicit choice; there is no automatic fallback on gateway failure.

## Live endpoint update

A compatible phone advertises `mobile-egress.endpoint-update.v1` as the decoded payload of a session `pong`. An ordinary empty pong does not imply support. The Client retains only the latest pending signed bundle and sends it only after that explicit advertisement, as a binary WebSocket JSON envelope with `version:1`, `type:"endpoint_update"`, empty `streamId`, and base64url payload containing the encoded update bundle (at most 87,384 bytes). Existing direct-only peers never receive this new control type.

The phone accepts at most one live update per session, verifies it through the same pinned-authority/Client/pairing/generation path as QR/file imports, commits it before reconnecting, and acknowledges at the new endpoint. Invalid updates do not mutate trust. Controls remain bounded; late callbacks cannot change a replacement session. Configuration polling remains compatible. Offline/older peers require explicit QR/file recovery; the Client keeps the desired update pending until its authenticated acknowledgement.
