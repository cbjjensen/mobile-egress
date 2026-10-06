# Inevitable Mobile Relay branding

Approved 2026-10-05. Implement on main in Mobile Egress and sibling Inevitable Proxies. Rename all current customer-facing product names, including ZFNF Mobile Egress, to **Inevitable Mobile Relay**. This is a branding change, not a return to the retired personal-computer relay architecture.

## Implementation checklist

- [x] Record approved plan and sibling analysis/implementation ledger before code.
- [ ] Website and billing: rename customer/Admin navigation, titles, activation, download/pilot/consent/help/error text. New purchase snapshots and Checkout product names use the new name. Accept both exact old/new billing names without rewriting historical snapshots, pending/idempotent requests, prices or invoices. Advance only the managed portal configuration branding revision; preserve billing behavior and message sanitization.
- [ ] Desktop: rename UI, installer/service display text and Windows shortcuts. Mac GUI becomes `/Applications/Inevitable Mobile Relay.app`; safely migrate only verified old application/shortcut objects during upgrade, with conflict handling and recovery. Preserve service ownership, protected state, keys and pairing.
- [ ] Mobile: rename Android/iOS app display names, screens, notification and permission/help/support text together. Preserve the approved lifecycle exception. Update tracked parity evidence/tests; retain legacy names only where recovery identifies an old VPN/profile.
- [ ] Release/download contract: future branded assets are `InevitableMobileRelaySetup.exe`, `inevitable-mobile-relay-macos-<version>-arm64.pkg`, and `inevitable-mobile-relay-android-<version>.apk`. Preserve published/frozen assets, including Android 2.0.1, and old verification contracts. Use 2.0.2 as the first new branding contract version; historical versions remain immutable. This task does not publish builds or deploy.
- [ ] Rename the existing App Store Connect/TestFlight app in place if its editable metadata and credentials allow; retain app identity, testers and groups. Record Apple availability/review blockers accurately.
- [ ] Current documentation, accepted architecture terminology and frontend instructions; remaining-old-name audit; relevant tests/builds, independent review, commit/push main.

## Compatibility and defaults

Keep package/bundle IDs, protocol identifiers, server/API/dashboard URLs, configuration keys, database names, service IDs, executable/internal storage paths, signing identities, pricing/access controls and pilot group membership unchanged. Existing icon artwork remains unchanged. The desktop/mobile primary display name is exactly **Inevitable Mobile Relay**. Historical releases/plans/benchmarks retain their original labels. Exact old names in verified recovery instructions and signer subjects are intentional compatibility evidence.

New visible Mac bundle/Windows shortcut paths require targeted upgrade handling; do not rename protected state directories or introduce a second service. Unknown or conflicting filesystem objects must not be overwritten. New artifact names start at 2.0.2, not by renaming frozen 2.0.0/2.0.1 artifacts. No binary rebuilding, signing, publication, stable promotion or production deployment is implied by source changes.

## Acceptance

Verify brand text across website and mobile/desktop surfaces; old/new billing terms and pending retries; new Checkout name and portal revision; customer error handling; old/new installer upgrade/repair and failure recovery preserving pairing; release/R2 historical and branded contracts; native tests where available; manifest and local documentation links. Record unavailable signing, Apple or physical checks rather than claiming acceptance.

## Progress and validation

Canonical implementation ledger: sibling `inevitable-proxies/technical-requirement-docs/2026-10-05-inevitable-mobile-relay-branding/implementation-summary.md`. Source baseline: Mobile `85708ac`, Inevitable `79a76dbf`, both clean main. Current progress: documentation saved; implementation pending.
