# Invariants

Status: proposed, not yet implemented.
Related: [requirements.md](./requirements.md) · [design.md](./design.md) · [decisions.md](./decisions.md)

Correctness properties the system must hold **at every point a transaction commits**, including under concurrent payments and concurrent redemptions (NFR-03) — not just "eventually consistent." Each is derived from a specific FR/NFR, and [design.md](./design.md) §3 describes the locking scheme intended to guarantee them. These are the properties tests should assert, especially under concurrency (e.g. many goroutines hammering the same user or the same campaign at once).

## Money invariants

| ID | Invariant |
|---|---|
| INV-01 | A user's redeemable balance is never negative: `user_balance.balance_idr >= 0`, always. (FR-15) |
| INV-02 | A user's balance always equals the sum of their ledger entries: `user_balance.balance_idr == SUM(ledger.amount_idr WHERE user_id = X)`, always. (NFR-04) |
| INV-03 | Every unit of cashback ever awarded or redeemed corresponds to exactly one `ledger` row. `user_balance`, `campaign_budget`, and `user_daily_cashback` are never mutated outside the same transaction as the `ledger` insert that explains the mutation. (NFR-04) |

## Budget invariants

| ID | Invariant |
|---|---|
| INV-04 | The campaign never awards more than its budget: `campaign_budget.awarded_total_idr <= campaign_budget.total_budget_idr`, always — including at the moment two concurrent payments both attempt to consume the last remaining budget. (FR-04, FR-06, FR-07) |
| INV-05 | `campaign_budget.awarded_total_idr` equals the sum of every `AWARD` ledger entry across all users: `campaign_budget.awarded_total_idr == SUM(ledger.amount_idr WHERE entry_type = 'AWARD')`, always. |

## Daily cap invariants

| ID | Invariant |
|---|---|
| INV-06 | A user's awarded total for any single calendar day (Asia/Jakarta) never exceeds the daily cap: `user_daily_cashback.awarded_total_idr <= 50,000` for every `(user_id, cashback_date)` row, always — including under concurrent payments for the same user on the same day. (FR-04, FR-05) |
| INV-07 | `user_daily_cashback.awarded_total_idr` for a given `(user_id, cashback_date)` equals the sum of that user's `AWARD` ledger entries whose payment's `cashback_date` matches, always. |

## Award correctness invariants

| ID | Invariant |
|---|---|
| INV-08 | A payment with `amount_idr < 20,000` always awards exactly 0. (FR-02) |
| INV-09 | A payment's awarded amount never exceeds its base cashback: `awarded_cashback_idr <= floor(amount_idr * 5 / 100)`. (FR-03, FR-04) |
| INV-10 | A payment's awarded amount never exceeds what was actually available at the moment it was processed: `awarded_cashback_idr <= daily_remaining` and `awarded_cashback_idr <= budget_remaining`, both evaluated pre-award within the same transaction. (FR-04) |

## Idempotency invariants

| ID | Invariant |
|---|---|
| INV-11 | A given `payment_id` is processed at most once: its `awarded_cashback_idr` and `reason_code` are fixed at first insert and never recomputed or overwritten by a later request for the same `payment_id`. (FR-08) |
| INV-12 | A given `(user_id, idempotency_key)` redemption pair produces at most one `redemptions` row and at most one `REDEEM` ledger entry, regardless of how many times the request is retried. (FR-16) |

## Availability invariant

| ID | Invariant |
|---|---|
| INV-13 | Redemption remains possible after `campaign_budget.awarded_total_idr == campaign_budget.total_budget_idr` (campaign ended) — only *awarding* stops, never *redeeming*. (FR-17) |

## Identity invariant

| ID | Invariant |
|---|---|
| INV-14 | Every `user_id` in `payments`, `ledger`, `user_balance`, `user_daily_cashback`, and `redemptions` refers to an existing row in `users`. Mechanically guaranteed by `FOREIGN KEY` constraints (decisions.md "User identity"), not just asserted — an insert/update that would violate this fails outright (`cashback.ErrUserNotFound`). |
