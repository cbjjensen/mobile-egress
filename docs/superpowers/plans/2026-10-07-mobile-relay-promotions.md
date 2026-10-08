# Inevitable Mobile Relay promotions

Approved 2026-10-07. Implement on existing main according to Inevitable ai-instructions.txt. Authoritative analysis, phase plan and evidence: [Inevitable implementation record](../../../../inevitable-proxies/technical-requirement-docs/2026-10-07-mobile-relay-promotions/plan.md) in the sibling checkout.

## Contract and decisions

Reuse existing percentage promotions for monthly/yearly Relay subscriptions, with once/repeating-calendar-month/forever durations including 100%. All-products codes, including existing codes, include Relay; scoped codes require explicit Relay selection. Affiliate buyer promotions auto-apply unless an explicit code is selected or removed; no stacking and coupon entry never changes signup attribution. Repeating months describe Stripe's duration from coupon application, not a count of yearly payments. Verified invoice evidence owns application/expiry; the backend does not equate discount/subscription start timestamps or calculate a local expiry.

Affiliate rewards cover only initial subscription invoices. Each link's Relay reward defaults None until configured. Free initial invoices grant service and consume the first-purchase affiliate discount opportunity, but earn no reward for that subscription, including later renewals. Cash/data rewards use existing affiliate paths, not Relay traffic accounting. Store-credit spending, automatic account benefits, proxy products/traffic/accounting and existing app binaries remain unchanged.

## Implementation checklist

- [x] Save plan and Inevitable analysis/implementation/validation records before coding.
- [x] Extend shared targets/contracts and constrained durable records in Inevitable.
- [x] Complete integration qualification of atomic promotion checkout, discount-aware settlement and zero-period eligibility; source is implemented.
- [x] Add durable initial-invoice affiliate decisions and reporting, with focused and isolated PostgreSQL evidence in Inevitable.
- [x] Add existing-pattern customer/Admin controls and focused UI evidence in Inevitable.
- [x] Complete focused/backend/frontend/PostgreSQL/billing-scenario validation and independent review.
- [x] Update both accepted Inevitable architectures, existing operations/help, requirements index and ai-instructions frontend patterns.
- [x] Synchronize final results, commits and explicit external blockers here.

No production deployment, price change, sales opening, app release or data quota is authorized by this work. Rollback must preserve reconciliation of already accepted discounted subscriptions. Existing pairing/credentials, hosted/direct behavior, signing identities and lifecycle rules remain intact.

## Validation and completion

Source implementation and documentation are present in the sibling Inevitable checkout. Its [analysis and interface decisions](../../../../inevitable-proxies/technical-requirement-docs/2026-10-07-mobile-relay-promotions/analysis.md), [implementation summary](../../../../inevitable-proxies/technical-requirement-docs/2026-10-07-mobile-relay-promotions/implementation-summary.md) and [validation report](../../../../inevitable-proxies/technical-requirement-docs/2026-10-07-mobile-relay-promotions/validation-report.md) own the detailed backend schema, transitions, exact commands/results, final commits and remaining external checks. Original local qualification and independent review are complete; the approved timing follow-up below awaits its own results. Ordinary real Stripe smoke remains unperformed without test credentials. The original timestamp-equality blocker is superseded by removal of that guard, so smoke is no longer required to prove that equality. This is not production release readiness. This work changed no production settings or Mobile binaries.

The Mobile quote/checkout retains accepted pending price, interval and promotion instead of starting another checkout after an uncertain result. Only verified invoice settlement grants the term, including explicit promotion-covered zero settlement. Initial affiliate decisions and outbox work commit with that invoice; later renewals never earn rewards. Refund/dispute reviews use stable changed-financial-state identities so replay cannot reopen a resolved review, while a larger cumulative refund creates a new review. These are billing records, not Mobile traffic accounting.

Existing conversion-based first-buyer eligibility is preserved: concurrent cross-product checkouts accepted before either settles can both capture the affiliate buyer code. The shared global promotion-use limit remains atomic. No Mobile store-credit spending, automatic account-benefit expansion or new deployed environment variable is added. Follow the existing [subscription operations guide](../../../../inevitable-proxies/technical-requirement-docs/2026-10-05-mobile-egress-subscriptions/operations.md#promotions-and-affiliate-rewards-2026-10-07) for Admin setup, recovery and rollback. This backend/website extension leaves Android/iOS behavior and parity evidence, phone pairing, hosted/direct transport, loopback listeners and signing/release artifacts unchanged.

### Final evidence (2026-10-07)

Inevitable code commits: `91dc51a2` (shared promotion/affiliate foundation) and `6642022d` (Mobile billing/customer UI). Inevitable qualification/documentation commit: `0e8af681`. Full backend rerun passed 211 suites/2,660 tests, with 12 existing environment-gated suites/128 tests skipped; frontend passed 70 files/701 tests; workspace build passed. The isolated signed-webhook HTTP/PostgreSQL bundle passed 12 cases with cleanup; its 3 runner safety tests passed. The safe local baseline passed before testing and again after the additive local-only migration; existing records were preserved. Mobile manifest validation passed. Documentation links and fixture-only OLED desktop/phone layout checks passed.

Independent review findings were fixed with regression coverage: confirmed checkout rejection releases capacity; ineligible automatic affiliate codes fall back to full price; uncertain checkout failure refreshes saved UI terms; canceled rewards stay canceled; increased refund totals get distinct review markers. See Inevitable validation for full original evidence and the superseded timing blocker. Production settings, traffic, Mobile binaries and signing identities remain unchanged. No release, production deployment or push was performed.

### Approved invoice-timing follow-up (2026-10-07)

Inevitable correction commit: `e68bbf2a` on main.

Implemented the existing ISP pattern by using verified Stripe invoice discounts for repeating application/expiry. Initial promotional and forever invoices still require the accepted discount; once cannot recur. An undiscounted once/repeating renewal must settle the full frozen base. Kept coupon/promotion identities, exact amounts and basic timestamp shapes, while removing equality to subscription start and local month arithmetic. No schema, proxy behavior or UI flow changed; the customer-help wording correction is copy only. Follow-up passed 42 focused terms tests, 211 backend suites/2,675 tests (128 existing environment-gated skips), all 12 isolated HTTP/database scenarios on a separate run, and the full workspace build. Inevitable's validation records the initial parallel HTTP 503 and likely shared fixture-lock contention, successful cleanup, documentation checks and independent review. Ordinary real Stripe smoke remains unperformed. Prior totals above remain historical evidence; no push or deployment was performed.
