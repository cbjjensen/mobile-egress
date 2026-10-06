# iPhone pairing response compatibility

## Evidence and scope

The owner reported iOS TestFlight build 5 scanning a Mac invitation, with the Mac waiting at setup step 4 while the iPhone shows Sharing, cellular available and Retry pairing. That button indicates unfinished enrollment/acknowledgement, not a completed tunnel. The Client advances to verification when credentials are issued, before acknowledgement.

The Client's existing `directReply` uses Go's JSON HTTP response writer without an explicit Content-Length. The public enrollment fixture exceeds Go's 2-KiB automatic Content-Length threshold. An actual Go HTTP socket reproduction returns `201 Created`, `Connection: close` and `Transfer-Encoding: chunked`, without Content-Length: 2,303 JSON bytes and 2,315 chunk-framed bytes. This corrects the initial close-delimited hypothesis before implementation. iOS's native HTTP parser requires Content-Length and rejects chunking. Android uses OkHttp and already accepts this response form. The screenshot alone does not establish the device's exact exception, but the confirmed incompatibility deterministically blocks this enrollment response.

Work on main. Preserve existing pairing keys, saved credentials, published installers and artifacts. Fix iOS interoperability with the existing Client rather than changing gateway/commercial proxy behavior or requiring the Mac to be reinstalled. No new infrastructure, accounting, trust, public listener or mobile lifecycle behavior.

## Implementation and acceptance

- [x] Reproduce the Go enrollment reply over a real socket with the same Connection: close request and public fixture; retain sanitized evidence.
- [x] Add a failing native Swift regression through the actual HTTP response accumulator, including fragmented enrollment bytes and delayed EOF.
- [x] Accept bounded chunked responses only on clean completion, preserving strict explicit Content-Length validation, duplicate/invalid headers, conflicting Content-Length/Transfer-Encoding rejection, unsupported transfer codings, exact terminators/trailing-byte rejection, 32-KiB header and 256-KiB decoded body limits, cancellation/timeouts and pinned cellular TLS. Bound chunk metadata and encoded bytes while receiving, including before EOF. Keep unsupported close-delimited responses rejected; they are not the demonstrated failure.
- [x] Demonstrate real Go response bytes pass iOS decoding and credential validation; check acknowledgement/config responses and saved same-key retry behavior.
- [ ] Run native Swift suite and warning checks, relevant portable/native iOS build checks, mobile-manifest validation and independent review. Keep the approved foreground-only iOS exception unchanged.
- [ ] Update enrollment evidence in the mobile manifest and synchronize the sibling Inevitable implementation record. Existing Android behavior remains the reference; no Android binary update is required for this native-parser repair.
- [ ] Increment iOS build number, create and verify a signed build through the original native Xcode/manual profile path, upload to TestFlight and make it available to the authorized internal testers. Preserve external build 5 review and public installers; do not publish an App Store version or stable release.
- [ ] Record physical acceptance separately: install the update without deleting the app, retain the pending Mac entry, retry pairing/start sharing, and verify a live session plus HTTP/CONNECT/SOCKS cellular traffic. Never claim remote physical success from source/unit checks.

## Risk and rollback

Chunked framing must never complete on timeout, transport failure or a partial read, and requires the final zero chunk and exact terminator. Length-framed responses remain exact, and all response data and chunk overhead remain bounded. TLS/identity/strict JSON validation is unchanged. Existing one-use invitations retain idempotent same-key recovery after credential issuance; no reset or re-pair should be required for that state. If device evidence differs, investigate the next boundary before claiming the entire issue resolved. Retain prior TestFlight artifacts; a requested rollback changes only the new build's availability, not credentials or existing pairings.

## Validation record

Real TLS 1.3/HTTP/1.1 Go regression passes and the complete nodeservice package passes. The test demonstrates chunked enrollment, identical same-key replay, pending state before acknowledgement, rejection of an unauthenticated acknowledgement, successful authenticated acknowledgement and current configuration. No Go production behavior changed.

New native Swift regressions failed with `HTTP1Error.ambiguousResponse` before repair and all 13 focused codec/interoperability tests pass afterward. Captured real Go TLS response bytes also decode exactly at 1-, 37- and 65,536-byte fragments, complete only at EOF, and pass the actual pinned certificate/serial validation. Incremental chunk decoding has a 32-KiB cumulative metadata bound in addition to the existing header/body bounds; unsupported extensions/trailers and close-delimited bodies remain rejected. TLS/transport errors remain fatal.

The first full native warning-as-error run compiled cleanly but found a stale packaging assertion expecting build 4; that assertion is updated with the new build 6 metadata. The complete rerun passes: 378 tests, zero failures and two existing physical Secure Enclave/entitled-Keychain opt-in skips. Source hashes match the tested native snapshot. Mobile manifest validation and iOS verification-script contracts pass. Independent review found no blocking issues. Native app builds and signed delivery are pending. Private evidence is in `G:/codex-build-cache/mobile-egress-hosted-20261004/ios-pairing-http-20261005/`.

No physical iPhone is connected to this workstation/build Mac; the owner's tester provides the final hardware result.
