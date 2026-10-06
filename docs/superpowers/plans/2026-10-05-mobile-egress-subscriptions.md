# Mobile Egress subscriptions, R2 downloads and iPhone pilot

Approved 2026-10-05; implement on main, preserving existing work. The corresponding executable plan, analysis and validation record are in sibling Inevitable `technical-requirement-docs/2026-10-05-mobile-egress-subscriptions/`.

## Approved behavior

- One account subscription, monthly/yearly USD. Admin prices start unset; sales stay closed. No new seats, traffic reporting, data quota, proxy allocation or changes to commercial proxy accounting.
- Reuse existing software billing. Verified paid invoices grant hosted access; pilot grants remain independent. Preserve all activation/gateway authorization checks, saved pairing and Advanced direct. Period-end cancellation retains paid access. No billing-period switch until the paid term ends.
- Mirror verified signed Android APK, Windows EXE and Apple Silicon Mac PKG through existing public R2 under mobile-egress/<version>/. Never change historical releases, signers or Order Tracker objects. Missing compatible artifacts stay unavailable; pilot is not stable.
- Admin-approved free pilot accounts supply TestFlight invitation email/consent. Backend automation enrolls only the configured external group, using an explicit compatible approved build. No Apple login/password/team role, paid-pilot coupling, delivery claims or unrelated membership changes.
- Focused website panels expose subscription/billing, platform downloads, iPhone enrollment and computer management. Preserve Android background versus iOS active/open/unlocked lifecycle guidance.

## Checklist

- [x] Save synchronized plan before code.
- [ ] Shared contracts, billing/access and lifecycle tests.
- [ ] Customer/Admin panels, pricing and downloads.
- [ ] Durable TestFlight signup/invitation/recovery.
- [ ] R2 publisher integrity tests and compatible signed Android preparation.
- [ ] Accepted architecture, operations, configuration and frontend-convention documentation.
- [ ] Billing/backend/frontend/release/manifest validation and independent review.

Implementation does not authorize production deployment, opening sales or stable promotion. Record signed artifact, Apple review and physical-device blockers explicitly. Final evidence and rulings must be synchronized here and in the sibling implementation summary.
