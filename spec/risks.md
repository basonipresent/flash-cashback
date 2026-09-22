# Risks

Status: proposed, not yet implemented.
Related: [requirements.md](./requirements.md) · [design.md](./design.md) · [invariants.md](./invariants.md) · [decisions.md](./decisions.md)

Known risks to this system, what mitigates each, and which are deliberately accepted or deferred rather than solved.

## No authentication — accepted for MVP, blocking for production

Any caller can set `X-User-Id` to any value and act as that user: read their balance, redeem their cashback, or (for `POST /payments`) claim any `user_id` earned a payment. This is real money. The FK constraint on `user_id` (decisions.md "User identity") doesn't change this — it only requires the value to be one of the ids seeded by `scripts/seed_users.sql`, not that the caller is verified as that user.

- **Status:** accepted, per the brief ("authentication... assume the user is known"). Explicitly out of scope for this exercise (requirements.md §4).
- **Mitigation if this ever left MVP/demo status:** real authentication (verify the caller *is* the user in `X-User-Id`, e.g. a signed session/JWT) is a hard prerequisite before any real deployment — not a nice-to-have. Worth stating plainly rather than leaving implicit, given the brief's own framing ("production grade").

## Fraud / abuse via fabricated payment events

Nothing stops a caller from submitting `POST /payments` events for payments that never happened, farming cashback. Related: with no auth (above), an attacker can also farm cashback under *someone else's* `user_id`.

- **Status:** out of scope per the brief (requirements.md §4), but the risk exists regardless of scope.
- **Mitigation, if addressed later:** payment events should come from a trusted internal source (the actual payments system), not be directly callable by end users — i.e., `POST /payments` is a system-to-system endpoint, not client-facing (see design.md §4). Network/service-level trust (private network, mTLS, service auth) substitutes for application-level fraud detection at MVP scale.

## Untrusted `paid_at` manipulating the daily cap

`cashback_date` (design.md §2) is derived from the caller-supplied `paid_at`, not server-observed time. A caller could submit payments with deliberately shifted `paid_at` values to make cashback land on whichever calendar day still has daily-cap headroom, effectively bypassing FR-05.

- **Status:** partially mitigated — see [decisions.md](./decisions.md) "`paid_at` bounds": future timestamps are now rejected, closing the "claim tomorrow's cashback today" variant of this. Backdating into the past is still possible and deliberately not bounded (that decision's trade-off) — the residual protection is that `POST /payments` is meant to be a trusted, system-to-system endpoint (see "Fraud / abuse via fabricated payment events" above), not that `paid_at` itself is validated against the past.

## Campaign-budget row as a single point of write contention

Every award transaction locks the same `campaign_budget` singleton row (design.md §3), serializing all payment processing globally.

- **Status:** accepted trade-off at this project's scale — correctness over throughput, and the budget is small (10,000,000 IDR) so the campaign is short-lived regardless.
- **Would matter if:** traffic or budget size grew by orders of magnitude. Deferred rather than solved now; not a concern for a take-home demo.

## Counter drift between `campaign_budget` / `user_daily_cashback` and the ledger

`campaign_budget.awarded_total_idr` and `user_daily_cashback.awarded_total_idr` are materialized counters (design.md §2), kept in sync with `ledger` by convention (same transaction), not by a database constraint. A bug, a missed code path, or manual DB intervention could make a counter drift from the true sum of `ledger` entries — silently causing over- or under-awarding, since nothing else would notice until reconciled (INV-05, INV-07 in invariants.md).

- **Status:** the transaction-boundary discipline in design.md §3 is the primary mitigation; there is no automatic enforcement beyond code review/tests.
- **Mitigation, if hardened further:** a periodic reconciliation job or admin query comparing each counter to `SUM(ledger...)` and alerting on mismatch (NFR-04 already requires balances be "reconcilable against" the ledger — this is that check, just not automated by default).

## Retried redemption with a reused idempotency key but a different amount

FR-16 guarantees a retried *identical* redemption request applies at most once. It's undefined what happens if a client reuses the same `(user_id, idempotency_key)` with a *different* `amount` — a client bug, not a retry.

- **Status:** resolved — see [decisions.md](./decisions.md) "Redemption idempotency-key conflict": rejected with `409 Conflict` rather than silently returning the stale result, so this no longer masks a client bug.

## Timezone assumption

Asia/Jakarta (WIB) has no daylight saving time, so "calendar day in WIB" is unambiguous year-round — this specific risk (DST-related double-counting or skipped hours at day boundaries) does not apply here. Noted only so it isn't re-litigated later: the choice of a fixed UTC+7 offset for `cashback_date` (design.md §2) is safe as long as the campaign stays scoped to this timezone.
