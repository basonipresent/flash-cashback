# Decisions

Resolutions to the open questions and ambiguities in [requirements.md](./requirements.md), with the reasoning behind each one.

## Minimum redemption amount: 1,000 IDR

FR-14 requires a minimum redemption amount; requirements.md proposed 1,000 IDR without stated reasoning. Confirmed as final.

**Why:** 1,000 IDR is exactly the smallest single cashback award possible under the rules (5% of the 20,000 IDR minimum-eligible payment, FR-02/FR-03). Setting the redemption floor there means a user is never left with an unredeemable balance after a single qualifying payment — one payment is always enough to redeem. A higher floor would force users to accumulate across multiple payments before their first redemption; a lower one serves no purpose since no smaller award can ever exist.

**How to apply:** Enforce in the redemption handler (FR-14, FR-15) as a hardcoded constant for MVP — see [design.md](./design.md) §3. Revisit only if the 5% rate or 20,000 IDR eligibility floor changes.

## Campaign time window: none

The campaign has no start/end date. It begins accepting payments immediately and ends only when the 10,000,000 IDR budget is exhausted (FR-06/FR-07).

**Why:** the brief states exactly one end condition — "The campaign has a total budget... When it's gone, the campaign is over" — and nothing about dates. Adding a time window would be inventing a requirement the brief doesn't ask for. `campaign_budget` (design.md §2) needs no date columns; `CAMPAIGN_ENDED` (FR-07, FR-09) is determined solely by `awarded_total_idr >= total_budget_idr`.

**How to apply:** `GET /campaign` (FR-13) reports `active` vs `ended` from budget state alone. If a real time-bound promotion is wanted later, `start_at`/`end_at` can be added to `campaign_budget` without touching the ledger or award logic — see design.md §3 for where that check would slot in.

## Redemption idempotency-key conflict: reject with 409

If `POST /cashback/redemptions` is called with a `(user_id, idempotency_key)` pair that was already used, but this request's `amount` differs from the original, the request is rejected (`cashback.ErrIdempotencyKeyConflict`, HTTP 409) rather than silently returning the original cached result.

**Why:** FR-16 only specifies that a *retried* (identical) request applies at most once — it says nothing about a *reused* key with a different amount, which is a different situation: either a client bug (accidentally reusing a key) or a genuine second redemption sent with a stale key. Silently returning the old result in that case would mask the bug and give the client a response that doesn't match what they asked for. Matches how production idempotency-key APIs (e.g. Stripe) typically behave. The alternative (idempotency key always wins) was considered and rejected — it optimizes for simplicity over catching a real failure mode, on a system that moves real money.

**How to apply:** Implemented in `backend/internal/cashback/redeem.go`'s `lookupRedemption` (compares the stored `amount_idr` against the request before returning a cached result) and mapped to `409 Conflict` in `backend/internal/httpapi/cashback.go`. Covered by `TestRedeem_IdempotencyKeyConflict` (domain) and `TestPostRedemption_IdempotencyKeyConflict` (HTTP). Supersedes the note in [risks.md](./risks.md) "Retried redemption with a reused idempotency key but a different amount."

## `paid_at` bounds: reject future timestamps only, no lower bound

`POST /payments` rejects a payment whose `paid_at` is more than 5 minutes in the future (`cashback.MaxFutureClockSkew`, `cashback.ErrFuturePaymentTimestamp`, HTTP 400). There is no bound on how far in the past `paid_at` may be.

**Why:** a payment can't have succeeded before the event reporting it was created — that's a logical invariant of what "payment succeeded" means, not an arbitrary business rule, so it's safe to enforce without more input (unlike a "too old" threshold, which depends on the real payment system's event-processing/backfill SLAs that aren't specified anywhere). The 5-minute tolerance absorbs ordinary clock skew between the payment source and this service without being loose enough to matter. No lower bound because backfill/replay with delay is normal system behavior, not something to reject.

**Trade-off, left open:** this only closes half of the risk in [risks.md](./risks.md) "Untrusted `paid_at` manipulating the daily cap" — a caller can still backdate `paid_at` into the past to land cashback on a day with more daily-cap headroom. Not addressed here because `POST /payments` is a system-to-system endpoint (design.md §4), and the actual protection against that risk is restricting who can call it, not a `paid_at` bound.

**How to apply:** `internal/cashback/award.go`'s `validatePaidAt` (pure, unit-tested in `TestValidatePaidAt`) runs after the FR-08 idempotency check — a retry of an already-processed payment returns its cached result regardless of its `paid_at`, since the original request already passed validation once. Mapped to `400` in `internal/httpapi/payments.go`. Covered end-to-end by `TestPostPayment_Validation`'s "paid_at too far in the future" case and `TestPostPayment_NearFutureWithinClockSkewToleranceAccepted`.

## Payment ingestion transport: synchronous HTTP endpoint

Payment-succeeded events reach the service via `POST /payments`, a synchronous HTTP call — not a message queue consumer. This was already the de facto choice (requirements.md §5 proposed it, and it's what got built); this entry makes the trade-off explicit rather than just deferring to "the brief said so."

**The trade-off:**

| | Sync HTTP (chosen) | Async MQ |
|---|---|---|
| Delivery guarantee | Caller retries on failure — its problem, not ours | Broker retries automatically, at-least-once, survives this service being down |
| Coupling | Caller blocked on this service's latency/uptime | Decoupled — broker buffers during an outage or traffic spike |
| Ops footprint | None beyond the API itself | A broker (Kafka/SQS/RabbitMQ), consumer group, dead-letter handling |
| Demo-ability | `curl`-able, one `docker compose up` | Needs the broker running too — harder to review in one command (NFR-06) |
| Idempotency requirement | Needed anyway (FR-08) | Needed anyway (at-least-once redelivery) — no extra cost either way |
| Ordering | Not guaranteed; not needed (no cross-payment ordering requirement exists) | Can offer per-key ordering, unused here |

**Decision: sync HTTP.** Async's real advantages — surviving this service being down, absorbing a traffic spike, ordering — aren't load-bearing at this project's scope: it's a demo a reviewer runs and pokes at, not a system that has to stay up under a real payment provider's traffic. Standing up a broker would add real complexity (another container, retry/DLQ policy, a consumer lifecycle in `main.go`) to buy guarantees nothing here actually exercises. This isn't a one-way door: FR-08's idempotency-on-`payment_id` design is exactly what a queue consumer would need too, so swapping a consumer loop in later doesn't touch `AwardPayment` at all — only `cmd/api/main.go` changes. If this project's scope ever included "survive the payment provider retrying into us during an outage" as a real requirement, that's the point where the trade-off flips and async becomes the right call, not before.

**How to apply:** No code change; this decision just confirms `internal/httpapi/payments.go`'s `POST /payments` as the answer to requirements.md §5's open question.

## Tie-break reason code: budget wins

When `daily_remaining == budget_remaining < base` — both caps bind at the exact same value — the reported `reason_code` (FR-09) is `PARTIAL_BUDGET`, not `PARTIAL_DAILY_CAP`. The awarded amount is identical either way; only the displayed reason differs.

**Why:** campaign-budget exhaustion is a global, one-time event that affects every user and ends the whole program — more significant to surface than a personal daily cap that simply resets the next calendar day. Two alternatives were considered and rejected: keeping the previous `PARTIAL_DAILY_CAP` default (frames it as "you personally hit your limit," but understates that the *campaign itself* is also right at its edge for everyone), and adding a new combined `PARTIAL_DAILY_CAP_AND_BUDGET` code (most literally accurate, but expands FR-09's originally-specified fixed set of six reason codes for a rare exact-tie edge case, forcing every consumer — mobile UI included — to handle a 7th value).

**How to apply:** `internal/cashback/award.go`'s `computeAward`, in the "both caps bind" branch: `dailyRemainingIDR < budgetRemainingIDR` (strict) returns `PARTIAL_DAILY_CAP`; anything else (including the tie) returns `PARTIAL_BUDGET`. Covered by `TestComputeAward`'s "both caps bind at exactly the same value" and "daily cap strictly smaller than budget remaining" cases.

## GET /campaign budget visibility: status only, no amount

`GET /campaign` returns only `{status: "active" | "ended"}`. It does not expose the remaining budget, spent-so-far total, or any other number. Confirms the behavior already shipped.

**Why:** FR-13 only requires a user be able to see whether the campaign is active or over — nothing in the brief asks for the underlying number. Exposing it would let any caller (there's no auth in this MVP, so effectively anyone) infer aggregate business metrics — total campaign spend, spend velocity, how close to exhaustion the whole program is across every user — from an endpoint whose stated purpose is a single user checking their own cashback status. That's a bigger leak than the endpoint needs to justify, for a feature nobody asked for.

**How to apply:** No code change — `internal/cashback/queries.go`'s `GetCampaignStatus`/`CampaignStatus` already only return a bool, and `internal/httpapi/campaign.go` already only serializes `status`. If a real "spend before it's gone" UX is wanted later, a coarser signal (e.g. a `low` boolean once budget crosses some threshold) would leak less than the raw number.

## User identity: real `users` table, seeded via SQL, no API

`user_id` was plain `TEXT` on every table (`payments`, `ledger`, `user_balance`, `user_daily_cashback`, `redemptions`) - an opaque, caller-chosen string, since this project doesn't own identity (requirements.md §4). It's now `UUID REFERENCES users(id)`, backed by a real `users` table (`id`, `name`, `created_at`). Rows are populated directly by `scripts/seed_users.sql` with fixed, well-known ids (e.g. `11111111-...` for alice) - there is no `POST /users` or any other user-management endpoint. The API surface stays exactly what the brief asked for: payments, redemptions, and reads.

**Why:** a bare type change (`user_id` as `uuid`-typed `TEXT`) was considered and rejected first - it would only validate string *format*, not add any real uniqueness guarantee, since nothing generated actual UUIDs. The real improvement is referential integrity: every `user_id` now provably refers to something, mechanically enforced by the FK constraints (see `invariants.md`), not just asserted. An API-based "find or create by name" endpoint was built first, then deliberately pulled back out: user creation/lookup isn't something the brief asked this service to do (FR-01–18 are entirely payments/redemptions/reads), and adding it grew the API surface for a concern this project doesn't own - identity is assumed to already exist (requirements.md §4), so a SQL seed script is the more honest match for "assume the user is known" than an endpoint that manufactures identities on demand.

**Trade-off, accepted:** the mobile app went back to a plain "enter a user id" text box - the caller now has to already know/paste the UUID, rather than typing a friendly name that resolves itself. Worse standalone UX, but consistent with the rest of the system: nothing here is meant to be a consumer-facing identity flow.

**How to apply:** `scripts/seed_users.sql` (run by `scripts/seed.sh` via `docker compose exec postgres psql`) is the only way `users` rows get created outside a test. `POST /payments` and `POST /cashback/redemptions` still return `cashback.ErrUserNotFound` (400) if `user_id` doesn't reference a real user - that check is independent of *how* users get created, so it stayed. `GET /cashback/{balance,daily,history}` still don't add an existence check (pure reads, harmless zero/empty defaults for an unknown id).
