# Inevitable Mobile Relay promotions

Approved 2026-10-07. Implement on existing main according to Inevitable ai-instructions.txt. Authoritative analysis, phase plan and evidence: [Inevitable implementation record](../../../../inevitable-proxies/technical-requirement-docs/2026-10-07-mobile-relay-promotions/plan.md) in the sibling checkout.

## Contract and decisions

Reuse existing percentage promotions for monthly/yearly Relay subscriptions, with once/repeating-month/forever durations including 100%. All-products codes include Relay; scoped codes require explicit Relay selection. Affiliate buyer promotions auto-apply unless an explicit code is selected; no stacking.

Affiliate rewards cover only initial subscription invoices. Each link's Relay reward defaults None until configured. Free initial invoices grant service and consume the first-purchase affiliate discount opportunity, but earn no reward for that subscription, including later renewals. Cash/data rewards use existing affiliate paths, not Relay traffic accounting. Store-credit spending, automatic account benefits, proxy products/traffic/accounting and existing app binaries remain unchanged.

## Implementation checklist

- [x] Save plan and Inevitable analysis/implementation/validation records before coding.
- [ ] Extend shared targets/contracts and constrained durable records.
- [ ] Add atomic promotion checkout, discount-aware settlement and zero-period eligibility.
- [ ] Add durable initial-invoice affiliate decisions and reporting.
- [ ] Add existing-pattern customer/Admin controls.
- [ ] Complete focused/backend/frontend/PostgreSQL/billing-scenario validation and independent review.
- [ ] Update both accepted Inevitable architectures, existing operations/help, requirements index and ai-instructions frontend patterns.
- [ ] Synchronize final results, commits and explicit external blockers here.

No production deployment, price change, sales opening, app release or data quota is authorized by this work. Rollback must preserve reconciliation of already accepted discounted subscriptions. Existing pairing/credentials, hosted/direct behavior, signing identities and lifecycle rules remain intact.

## Validation and completion

Implementation started; checks not yet complete. See Inevitable validation-report.md and implementation-summary.md for current evidence. No production changes performed.
