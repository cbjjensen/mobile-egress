# Task 1 Report: Swift Package Protocol and Validation Core

## Outcome

Implemented the portable `MobileEgressCore` Swift package under `ios/Sources/MobileEgressCore` and retained the prior XCTest suite under `ios/Tests/MobileEgressCoreTests`.

The core now provides:

- Strict unpadded base64url QR decoding, strict required JSON fields, version 1 pairing and endpoint-migration parsing, HTTPS-origin normalization, future-expiry enforcement, and byte-identical migration CA matching.
- A Security-guarded certificate-authority validator. It requires a single PEM certificate, parses DER basic constraints and key usage for CA/keyCertSign, and on Apple platforms uses `SecTrust` with the supplied CA as the sole anchor at the supplied verification date.
- Version 1 binary WebSocket envelope encoding/parsing with strict fields, valid stream IDs, agent-inbound role enforcement, 1 MiB payload and 2 MiB frame limits, plus the Android finite error-code vocabulary.
- Public IPv4/IPv6 literal validation that rejects non-public, reserved, documentation, multicast, loopback, link-local, and private ranges.
- Bounded session primitives for 32-stream admission, required-control queue saturation signaling, per-stream/global data queue bounds, 32-entry tombstones, and idempotent session close.

## Tests Added or Corrected

The preexisting tests covered every bullet in the Task 1 brief. I added focused coverage for:

- Missing required pairing fields and query-bearing relay origins.
- Unexpected wire-envelope fields.
- Migration recognition rejecting a Boolean version, which Foundation otherwise bridges as integer `1`.

I also corrected a preexisting expiry test fixture: its old timestamp was after the fixed test clock, so it did not describe an expired payload. The test now uses `2026-04-30T18:00:00Z`, before the fixed `2026-04-30T20:26:40Z` clock.

## Verification

Initial red baseline, before production source existed:

```powershell
docker run --rm --mount "type=bind,source=C:\Users\Chad\workspace\mobile-egress-ios-agent,target=/workspace" -w /workspace/ios swift:6.0 swift test
```

Result: exit 1, with `target 'MobileEgressCore' referenced in product 'MobileEgressCore' is empty`.

Final portable verification:

```powershell
docker run --rm --mount "type=bind,source=C:\Users\Chad\workspace\mobile-egress-ios-agent,target=/workspace" -w /workspace/ios swift:6.0 swift test
```

Result: exit 0, 17 XCTest cases passed with 0 failures.

Windows-native attempts:

```powershell
Set-Location 'C:\Users\Chad\workspace\mobile-egress-ios-agent\ios'
swift test
xcodebuild test
```

Result: both commands are unavailable on this Windows host (`swift` and `xcodebuild` are not recognized). Docker Desktop was available and used for the portable suite.

## Self-Review

- Reviewed strict parser behavior against the Android implementations for pairing, migration, wire protocol, public-address policy, stream admission, and queue limits.
- Found and fixed the Boolean-to-`Int` bridge in migration recognition with a failing regression test.
- Reviewed Task 1 files for private-key and credential markers; none were found.
- Ran `git diff --check`; no whitespace errors were reported.

## Remaining Concern

The `#if canImport(Security)` certificate-validation branch and the XCTest guarded by the same condition could not be compiled or run on Windows/Linux. The container result verifies the portable core only; macOS/Xcode verification remains required for the Apple Security path.

## Fix Round 1

Addressed four review findings:

- Zero-extended the IPv6 network prefixes before matching, preserving Android's `2001::/23`, `2001:2::/48`, `3fff::/20`, and other byte-prefix semantics.
- Added a raw top-level JSON lexer for `version` and now require the literal token `1`; `1.0` and `1e0` are rejected before Foundation can coerce them to `Int`.
- Added portable real-PEM DER coverage for a parseable leaf (`CA:FALSE`) and a `CA:TRUE` certificate that lacks `keyCertSign`.
- Changed pairing and migration capability validation from nonempty to nonblank, matching Android `isBlank()` behavior.

### RED

Command:

```powershell
docker run --rm --mount "type=bind,source=C:\Users\Chad\workspace\mobile-egress-ios-agent,target=/workspace" -w /workspace/ios swift:6.0 swift test
```

Output: exit 1; 23 XCTest cases executed with 11 failures. The failures were the expected new regressions: `2001::1`, `2001:2::1`, and `3fff::1` were accepted; pairing, migration, and wire envelopes accepted both `1.0` and `1e0`; and pairing/migration accepted whitespace-only capabilities. The portable parseable non-CA and no-`keyCertSign` certificate test passed, proving the existing DER checks were exercised.

### GREEN

Command:

```powershell
docker run --rm --mount "type=bind,source=C:\Users\Chad\workspace\mobile-egress-ios-agent,target=/workspace" -w /workspace/ios swift:6.0 swift test
```

Output: exit 0; all 23 XCTest cases passed with 0 failures. This includes the new public-address, pairing, migration, wire-protocol, whitespace-capability, and portable certificate-authority regressions.

## Fix Round 2

The raw `version` lexer previously returned on its first matching key, while Foundation accepted duplicate keys. Top-level object validation now rejects duplicate keys before strict parsing or migration recognition; escaped top-level field names are rejected as part of that lexical strictness, so they cannot encode a semantic duplicate key.

### RED

Full duplicate-version regression command:

```powershell
docker run --rm --mount "type=bind,source=C:\Users\Chad\workspace\mobile-egress-ios-agent,target=/workspace" -w /workspace/ios swift:6.0 swift test
```

Output: exit 1; 26 XCTest cases executed with 4 failures. The `1` then `1.0` ordering was accepted by pairing, migration recognition, migration parse, and wire parsing. The reverse ordering was already rejected by the first-token literal check.

Focused all-duplicates regression command:

```powershell
docker run --rm --mount "type=bind,source=C:\Users\Chad\workspace\mobile-egress-ios-agent,target=/workspace" -w /workspace/ios swift:6.0 swift test --filter WireProtocolTests
```

Output: exit 1; 7 selected XCTest cases executed with 2 failures. Both duplicate-version keys and duplicate non-version `type` keys were accepted before the uniqueness gate.

### GREEN

Focused command:

```powershell
docker run --rm --mount "type=bind,source=C:\Users\Chad\workspace\mobile-egress-ios-agent,target=/workspace" -w /workspace/ios swift:6.0 swift test --filter WireProtocolTests
```

Output: exit 0; all 7 selected XCTest cases passed with 0 failures.

Full command:

```powershell
docker run --rm --mount "type=bind,source=C:\Users\Chad\workspace\mobile-egress-ios-agent,target=/workspace" -w /workspace/ios swift:6.0 swift test
```

Output: exit 0; all 27 XCTest cases passed with 0 failures, including pairing, migration recognition/full-parse, wire duplicate-version orderings, and duplicate non-version top-level keys.
