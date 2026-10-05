# Protocol

Mobile Egress uses phone-initiated cellular TLS/WebSocket sessions terminating at the workload Client. Default hosted mode carries them through Inevitable's gateway; Advanced direct uses a public workload listener. The authoritative inner API and signatures are in [the direct protocol contract](direct-protocol-v2.md), with [hosted mode and live-update additions](hosted-transport-contract.md). Existing 1.x relay endpoints and invitations remain incompatible; there is no automatic fallback.

## User-scanned codes

New Client QRs use `MEQR1:` followed by canonical unpadded base64url of one zlib stream containing the original decoded invitation or signed-update JSON bytes. This is a presentation wrapper only. Phone QR/paste imports reconstruct the exact original canonical base64url bundle before existing type, expiry, signature, identity and generation validation. Generic binary decoders and network messages do not accept this wrapper. Copyable setup codes and exported update files retain their original format.

Reject raw input above 87,384 characters before trimming, compressed or expanded data above 65,536 bytes, empty output, unknown wrapper versions, noncanonical base64url, dictionaries, truncation, invalid checksums, and trailing or concatenated streams. Expansion is bounded during decoding. Errors contain no input or decoder details. Apple additionally bounds raw UTF-8 bytes, which does not restrict the ASCII wire format.

Compact QRs require Android code 24 or a correspondingly updated iOS app. Updated apps continue accepting older uncompressed codes; copying the complete setup code remains the compatible fallback for older apps. No saved-pairing migration or transport-version change is involved. See the [compact QR repair](superpowers/plans/2026-10-05-compact-pairing-qr.md) and public `testdata/compact-qr-v1.json` interoperability cases.

## Transport semantics

The public Client listener uses TLS1.3 with independent per-Client CA trust, SAN checking and scoped Agent ClientAuth certificates. Enrollment alone uses a bounded one-use capability without an existing certificate. ACK, session, configuration and renewal require durable admission in addition to mTLS.

A direct session explicitly identifies `direct/1` and requests transport2. JSON control envelopes remain binary WebSocket messages; negotiated raw data framing avoids JSON/base64 data overhead. Senders prefer16KiB data frames and accept32KiB. Existing open/open-result/data/close/error semantics and validated address alternatives remain. The workload resolves and validates public destinations; the phone validates candidates again under one target connection deadline. Established streams are never replayed automatically.

Each Client has one active paired Agent session. Session/Client ownership scopes wire stream IDs. The phone shares directional8192-frame/64MiB retained-data budgets across all Clients,32frames perstream, including queued and in-flight ownership. Read pause/resume, bounded fair admission, exact-once refunds and ordered EOF must remain valid through cancellation and revocation. Required control failure terminates only the affected session; data overload closes the contributing stream. No fixed live-stream or Mbps limit is introduced.

## State transitions

Pairing: reserve pending phone record/key → CSR-bound durable Client issuance → durable phone identity → authenticated ACK → complete. Delivery and ACK retries are idempotent for the same key. Expiry/cancellation do not create duplicate identities.

Endpoint recovery: durable desired generation → signed payload → verify identity/signature/generation → durable phone update → ACK → clear pending. Offline phones may skip generations. Authority replacement is re-pairing, never an endpoint update.

Revocation: durable denial → close current session/streams → reject further admission. New same-pair sessions may replace old sessions only after admission. Late callbacks are generation-scoped and cannot affect another Client.

Transport format reuse does not make old relay releases compatible with direct2.x pairing. New minor versions must negotiate only supported direct capabilities and reject incompatible changes explicitly.
