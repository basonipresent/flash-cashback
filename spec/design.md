# Technical Design — Flash Cashback

Status: implemented and synced - backend (`backend/internal/cashback`, `backend/internal/httpapi`, `backend/migrations`), `spec/api.yaml` (all endpoints below), and the mobile app (`mobile/src/screens/Dashboard.tsx`).
Related: [requirements.md](./requirements.md) · [decisions.md](./decisions.md) · [invariants.md](./invariants.md) · [api.yaml](./api.yaml) · [risks.md](./risks.md)

This document answers *how* the requirements in [requirements.md](./requirements.md) get built. It makes technical decisions (schema shape, concurrency control, tool choices). It deliberately does **not** make product/business decisions (e.g. the minimum redemption amount) — those belong in [decisions.md](./decisions.md) and are called out below as open items where this design depends on them.

---

## 1. Architecture overview

```
┌──────────────────┐
│  Mobile (Expo)    │   untrusted client (NFR-07): displays server
│  React Native     │   results only, never computes cashback
└────────┬──────────┘
         │ HTTPS, header: X-User-Id  (no auth in MVP — see requirements.md §4)
         ▼
┌───────────────────┐
│  Go API            │   net/http, stateless, horizontally scalable
│  (internal/httpapi)│
└───┬────────────┬───┘
    │            │
    │ source     │ optional cache / future
    │ of truth   │ rate-limit (NFR-02: never
    ▼            ▼ authoritative for money)
┌──────────┐  ┌────────┐
│ Postgres │  │ Redis  │
└──────────┘  └────────┘
```

The API is stateless so it can run as multiple replicas behind the published port; all cross-request coordination (daily cap, campaign budget, idempotency) happens in Postgres, not in process memory.

---

## 2. Data model

All money columns are `BIGINT` IDR (NFR-01). Every `user_id` column is `UUID REFERENCES users(id)` (decisions.md "User identity") — real referential integrity, not just an opaque string.

### `users`
Minimal identity: no auth (requirements.md §4), and this service doesn't create users either (decisions.md "User identity") - rows are populated directly via SQL (`scripts/seed_users.sql`), not through an API.

| column | type | notes |
|---|---|---|
| `id` | uuid, PK | fixed, well-known values in the seed script (e.g. `11111111-...` for alice) |
| `name` | text | |
| `created_at` | timestamptz | |

### `payments`
One row per ingested payment event, doubling as the idempotency record for FR-08: a second `POST /payments` for the same `payment_id` is a read of this row, not a re-award.

| column | type | notes |
|---|---|---|
| `payment_id` | text, PK | from the incoming event |
| `user_id` | uuid, FK → users | |
| `amount_idr` | bigint | as received |
| `paid_at` | timestamptz | as received |
| `cashback_date` | date | `paid_at` converted to Asia/Jakarta, truncated to date (FR-05) — stored, not computed at query time, so the daily-cap key never changes if the server's default timezone changes later |
| `base_cashback_idr` | bigint | `floor(amount_idr * 5 / 100)` |
| `awarded_cashback_idr` | bigint | `min(base, daily remaining, budget remaining)` |
| `reason_code` | text | `AWARDED` / `PARTIAL_DAILY_CAP` / `PARTIAL_BUDGET` / `BELOW_MINIMUM` / `DAILY_CAP_REACHED` / `CAMPAIGN_ENDED` (FR-09) |
| `processed_at` | timestamptz | when this row was written |

### `campaign_budget`
Singleton row (`id = 1`) tracking total and consumed budget, so the cap is enforceable with a single atomic `UPDATE` rather than a table scan.

| column | type | notes |
|---|---|---|
| `id` | int, PK | always `1` |
| `total_budget_idr` | bigint | 10,000,000 (FR-06) — a row, not a constant, so it's adjustable without a redeploy |
| `awarded_total_idr` | bigint | running total consumed |
| `updated_at` | timestamptz | |

### `user_daily_cashback`
Per-user, per-WIB-day running total, for the 50,000 IDR daily cap (FR-05).

| column | type | notes |
|---|---|---|
| `user_id` | uuid, FK → users | PK (composite) |
| `cashback_date` | date | PK (composite) |
| `awarded_total_idr` | bigint | |

### `ledger`
Append-only, the single source of truth for money movement (NFR-04). Every award and every redemption is one row; balances must always be reconcilable as `SUM(amount_idr)` per user against this table.

| column | type | notes |
|---|---|---|
| `id` | bigserial, PK | |
| `user_id` | uuid, FK → users | |
| `entry_type` | text | `AWARD` (+) or `REDEEM` (−) |
| `amount_idr` | bigint | signed: positive for AWARD, negative for REDEEM |
| `ref_type` | text | `PAYMENT` or `REDEMPTION` |
| `ref_id` | text | `payments.payment_id` or `redemptions.id` — traceability from ledger entry back to its cause |
| `created_at` | timestamptz | |

### `user_balance`
Materialized balance for O(1) reads (FR-10), kept in sync with `ledger` inside the same transaction as every write. Reconciliation (NFR-04) = periodically or on-demand asserting `user_balance.balance_idr == SUM(ledger.amount_idr) WHERE user_id = ...`.

| column | type | notes |
|---|---|---|
| `user_id` | uuid, PK, FK → users | |
| `balance_idr` | bigint | |
| `updated_at` | timestamptz | |

### `redemptions`
| column | type | notes |
|---|---|---|
| `id` | uuid, PK | |
| `user_id` | uuid, FK → users | |
| `amount_idr` | bigint | |
| `idempotency_key` | text | unique per `(user_id, idempotency_key)` — backs FR-16 |
| `created_at` | timestamptz | |

---

## 3. Concurrency & correctness (NFR-03)

This is the part that has to be right: two payments for the same user arriving at the same instant must not jointly award more than the daily cap, and two payments anywhere must not jointly exceed the campaign budget.

**Approach: guard the two shared counters with row-level locks inside a single transaction per payment**, in a fixed lock order to avoid deadlocks:

1. `INSERT INTO payments (...) ON CONFLICT (payment_id) DO NOTHING`. If it conflicted, the payment was already processed — `SELECT` the existing row and return its `awarded_cashback_idr` / `reason_code` as-is (FR-08). Stop here.
2. Otherwise, in the same transaction:
   a. `SELECT ... FROM campaign_budget WHERE id = 1 FOR UPDATE` (always locked first).
   b. `INSERT INTO user_daily_cashback (user_id, cashback_date, awarded_total_idr) VALUES (..., ..., 0) ON CONFLICT DO NOTHING`, then `SELECT ... FOR UPDATE` on that row (always locked second).
3. Compute, in order:
   - `amount_idr < 20,000` → awarded = 0, reason = `BELOW_MINIMUM`. Skip the checks below (FR-02).
   - else `budget_remaining = total_budget_idr - awarded_total_idr`; if `<= 0` → awarded = 0, reason = `CAMPAIGN_ENDED` (FR-07).
   - else `daily_remaining = 50,000 - awarded_total_idr`; if `<= 0` → awarded = 0, reason = `DAILY_CAP_REACHED`.
   - else `base = floor(amount_idr * 5 / 100)`; `awarded = min(base, daily_remaining, budget_remaining)` (FR-03, FR-04).
   - reason: `AWARDED` if `awarded == base`; otherwise whichever of `daily_remaining` / `budget_remaining` was the binding constraint — see the open tie-break question below.
4. Update `campaign_budget.awarded_total_idr += awarded` and `user_daily_cashback.awarded_total_idr += awarded` (only if `awarded > 0`).
5. Insert the `payments` row with the computed outcome; insert a `ledger` row (`AWARD`, `+awarded`) if `awarded > 0`; upsert `user_balance.balance_idr += awarded`.
6. Commit.

Because the budget row is locked first on every award, it is a single global serialization point. At this project's scale (a 10,000,000 IDR budget, MVP demo traffic) that's the right trade-off: it's simple and obviously correct, versus a lock-free atomic-`UPDATE ... WHERE remaining >= 0 RETURNING ...` pattern that would need a second read to compute the *partial* award correctly (partial awards need to know the exact remaining amount, not just whether some is left). If budget contention ever became a real bottleneck, that's a documented, deferred optimization — not a concern at this scope.

**Redemptions (FR-14–16)** follow the same shape, one lock instead of two: `SELECT ... FROM user_balance WHERE user_id = ... FOR UPDATE`, check `amount <= balance` (FR-15) and `amount >= 1,000 IDR` (decisions.md), insert `redemptions` (idempotency key gives FR-16: an identical retry hits the unique constraint and the handler returns the original result instead of erroring — but a *reused* key with a *different* amount is rejected with a conflict rather than returning the stale result, decisions.md "Redemption idempotency-key conflict"), insert `ledger` (`REDEEM`, `-amount`), update `user_balance`. Implemented in `internal/cashback/{award,redeem}.go` using a claim-row pattern (insert a placeholder first, fill it in once locks are held) rather than the single-`INSERT`-with-full-data shorthand described above — needed so the idempotency guard on `payment_id`/`idempotency_key` is in place *before* the outcome is known.

---

## 4. API surface

Implemented and fully documented in `spec/api.yaml`:

| Method & path | FRs | Notes |
|---|---|---|
| `POST /payments` | FR-01–09 | Body: `payment_id`, `user_id`, `amount`, `paid_at`. `user_id` comes from the event body, not `X-User-Id` — this is a system-to-system call from whatever emits payment-succeeded events, not a call the mobile app makes. Must reference a real `users.id` (400 if not - see `cashback.ErrUserNotFound`). Response: `{ awarded_amount, reason_code, balance_after }`. Idempotent on `payment_id`. |
| `GET /cashback/balance` | FR-10 | Identifies the caller via `X-User-Id`. |
| `GET /cashback/daily` | FR-11 | Today's (WIB) earned total and remaining allowance for the calling user. |
| `GET /cashback/history` | FR-12 | Paginated, newest-first. Not a raw `ledger` dump: unions `ledger` entries (`AWARD` with `amount > 0`, and `REDEEM`, left-joined to `payments` for `reason_code`) with zero-award `payments` rows (`BELOW_MINIMUM`/`DAILY_CAP_REACHED`/`CAMPAIGN_ENDED`), which never get a `ledger` row at all since `AwardPayment` only writes one when `awarded > 0`. Without this, FR-12's "including the reason for each payment outcome" wouldn't hold for zero-award payments - they'd simply be invisible. |
| `GET /campaign` | FR-13 | `{ status: "active" \| "ended" }`. Doesn't expose the remaining budget (decisions.md "GET /campaign budget visibility"). |
| `POST /cashback/redemptions` | FR-14–18 | Header: `Idempotency-Key`. Body: `{ amount }`. Must reference a real `users.id` (400 if not). Response: `{ redemption_id, new_balance }`. |

All endpoints identifying a user use `X-User-Id`, consistent with the brief's "assume the user is known; identity passed in a header" (requirements.md §4) - it's now a real `users.id` UUID (seeded via SQL, `scripts/seed_users.sql`), not an arbitrary caller-chosen string, though nothing verifies the caller *is* that user (still no auth).

The API allows any origin (`Access-Control-Allow-Origin: *`, `internal/httpapi/httpapi.go`'s `withCORS`) so the mobile app's web target (a different origin/port during local development) can call it directly from a browser. Doesn't widen the trust boundary - there's no auth or cookie-based session to protect, `X-User-Id` is already an untrusted, caller-supplied header regardless of origin.

---

## 5. Redis's role

Per NFR-02, Redis is never authoritative for money — every check above happens against Postgres inside a transaction. `/readyz` already depends on Redis because it's part of the required stack; its actual job is deliberately **not yet decided** beyond that:

- Most likely use: a short-TTL read-through cache for cheap, frequently-polled reads (`GET /campaign`, `GET /cashback/balance`) to keep DB load down — pure optimization, safe to skip for the MVP demo since traffic is trivial.
- Possible future use: rate-limiting `POST /payments` as a defensive measure (ties to abuse/fraud risk — see [risks.md](./risks.md)).

Nothing in the correctness story depends on Redis; it can be introduced or left unused without changing any invariant.

---

## 6. Migrations

[golang-migrate](https://github.com/golang-migrate/migrate): a single versioned `0001_init.{up,down}.sql` in `backend/migrations/` (embedded into the binary via `go:embed`, see `backend/migrations/embed.go`). Runs automatically on every backend boot (`internal/migrate.Up`, called from `cmd/api/main.go` right after the Postgres ping succeeds) - `make up` alone stands up a fully working schema, no separate migrate step needed (NFR-06). Safe under concurrent replicas: golang-migrate holds a Postgres advisory lock for the duration of the run. `make migrate` still exists for a manual/CI re-run (`docker compose exec backend /app/api -migrate`, reusing the same `-healthcheck`-style flag pattern already in `main.go`).

---

## 7. Open items for `spec/decisions.md`

All resolved — see `decisions.md`: minimum redemption amount (1,000 IDR), campaign time window (none), redemption idempotency-key conflict (reject with 409), `paid_at` bounds (reject future only), payment ingestion transport (synchronous HTTP), tie-break reason code (budget wins), `GET /campaign` budget visibility (status only, no amount), and user identity (real `users` table, seeded via SQL, no API).

---

## 8. Non-goals

Mirrors requirements.md §4: no refunds/clawback, no auth beyond the `X-User-Id` header assumption, no product catalogue, no multi-campaign support, no cashback expiry, no real payout rail. Fraud/abuse detection is tracked as a risk, not designed here.
