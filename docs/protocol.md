# Protocol

Mobile Egress2 uses direct workload listeners and phone-initiated cellular TLS/WebSocket sessions. The authoritative API, bundle and signature shapes are in [the direct protocol contract](direct-protocol-v2.md). Existing1.x relay endpoints and invitations are incompatible; there is no automatic fallback.

## Transport semantics

The public Client listener uses TLS1.3 with independent per-Client CA trust, SAN checking and scoped Agent ClientAuth certificates. Enrollment alone uses a bounded one-use capability without an existing certificate. ACK, session, configuration and renewal require durable admission in addition to mTLS.

A direct session explicitly identifies `direct/1` and requests transport2. JSON control envelopes remain binary WebSocket messages; negotiated raw data framing avoids JSON/base64 data overhead. Senders prefer16KiB data frames and accept32KiB. Existing open/open-result/data/close/error semantics and validated address alternatives remain. The workload resolves and validates public destinations; the phone validates candidates again under one target connection deadline. Established streams are never replayed automatically.

Each Client has one active paired Agent session. Session/Client ownership scopes wire stream IDs. The phone shares directional8192-frame/64MiB retained-data budgets across all Clients,32frames perstream, including queued and in-flight ownership. Read pause/resume, bounded fair admission, exact-once refunds and ordered EOF must remain valid through cancellation and revocation. Required control failure terminates only the affected session; data overload closes the contributing stream. No fixed live-stream or Mbps limit is introduced.

## State transitions

Pairing: reserve pending phone record/key → CSR-bound durable Client issuance → durable phone identity → authenticated ACK → complete. Delivery and ACK retries are idempotent for the same key. Expiry/cancellation do not create duplicate identities.

Endpoint recovery: durable desired generation → signed payload → verify identity/signature/generation → durable phone update → ACK → clear pending. Offline phones may skip generations. Authority replacement is re-pairing, never an endpoint update.

Revocation: durable denial → close current session/streams → reject further admission. New same-pair sessions may replace old sessions only after admission. Late callbacks are generation-scoped and cannot affect another Client.

Transport format reuse does not make old relay releases compatible with direct2.x pairing. New minor versions must negotiate only supported direct capabilities and reject incompatible changes explicitly.
