# Requirements — Flash Cashback

Status: draft v0.1
Related: [decisions.md](./decisions.md) · [invariants.md](./invariants.md) · [api.yaml](./api.yaml) · [risks.md](./risks.md)

---

## 1. Original brief

### Objective
This exercise is about judgment, not typing speed. The rules below describe the happy path; the goal is to decide what has to be true before this feature is allowed near real money.

### Business rules (as given)
1. Users earn 5% cashback on every payment they make.
2. Payments under 20,000 IDR earn nothing.
3. Each user can earn at most 50,000 IDR of cashback per day.
4. The campaign has a total budget of 10,000,000 IDR. When it's gone, the campaign is over.
5. Users can redeem their cashback balance.

### Scope (as given)
- **In scope:** payments, which earn cashback or don't according to the rules above, and what a user needs to see and do around their cashback.
- **Out of scope:** refunds and clawback of cashback already awarded; authentication (assume the user is known); products, catalogue, stock (a payment is just an amount).

### Expectations (as given)
- Built with AI assistance.
- MVP, but production grade: small scope, but everything shipped is ready for production.
- Identify what's missing from the rules and decide what makes the cut.
- Stack: Go, PostgreSQL, Redis, React Native.
- A working demo reviewers can run.
- Published on GitHub.

---

## 2. Functional requirements

Each requirement has an ID so it can be traced to decisions, invariants and tests.

### Earning cashback

| ID | Requirement |
|---|---|
| FR-01 | The system receives a *payment succeeded* event containing `payment_id`, `user_id`, `amount` (IDR, integer) and `paid_at` (timestamp). Only successful payments are processed. |
| FR-02 | A payment with `amount < 20,000` earns 0. A payment of exactly 20,000 is eligible. |
| FR-03 | Base cashback = `floor(amount × 5 / 100)` in whole rupiah. Integer arithmetic only; no floating point. |
| FR-04 | Awarded cashback = `min(base cashback, user's remaining daily allowance, campaign's remaining budget)`. Partial awards are allowed. |
| FR-05 | The daily limit of 50,000 IDR per user applies per calendar day in **Asia/Jakarta (WIB)**, determined by the payment's `paid_at`, not by processing time. |
| FR-06 | The campaign budget of 10,000,000 IDR is consumed when cashback is **awarded**, not when redeemed. |
| FR-07 | When remaining budget reaches 0, the campaign is over: subsequent payments are recorded with 0 cashback and a reason, and are not errors. |
| FR-08 | Processing the same `payment_id` more than once awards cashback at most once and returns the original result. |
| FR-09 | Every payment outcome is recorded with the awarded amount and a reason code (e.g. `AWARDED`, `PARTIAL_DAILY_CAP`, `PARTIAL_BUDGET`, `BELOW_MINIMUM`, `DAILY_CAP_REACHED`, `CAMPAIGN_ENDED`). |

### Viewing cashback

| ID | Requirement |
|---|---|
| FR-10 | A user can see their current redeemable cashback balance. |
| FR-11 | A user can see how much cashback they have earned today and how much of the daily limit remains. |
| FR-12 | A user can see a history of cashback earned and redeemed, including the reason for each payment outcome. |
| FR-13 | A user can see whether the campaign is active or over. |

### Redeeming cashback

| ID | Requirement |
|---|---|
| FR-14 | A user can redeem any amount between the minimum redemption amount and their current balance. |
| FR-15 | A redemption can never make the balance negative. |
| FR-16 | A retried redemption request (same idempotency key) is applied at most once. |
| FR-17 | Redemption remains available after the campaign has ended. Earned cashback belongs to the user. |
| FR-18 | Redemption credits a simulated wallet; no real payout rail is integrated. |

---

## 3. Non-functional requirements

| ID | Requirement |
|---|---|
| NFR-01 | All money amounts are stored and computed as integer IDR (`BIGINT`). |
| NFR-02 | PostgreSQL is the single source of truth for balances, budget and daily usage. Redis is never authoritative for money. |
| NFR-03 | Correctness holds under concurrent payments and concurrent redemptions (see invariants.md). |
| NFR-04 | Every movement of money is recorded in an append-only ledger; balances are reconcilable against it. |
| NFR-05 | The service exposes a health check and structured logs; failures are observable. |
| NFR-06 | The whole backend runs with a single command; the mobile app runs with documented steps. |
| NFR-07 | The client is untrusted: the mobile app never computes cashback; it only displays server results. |

---

## 4. Explicitly out of scope

- Refunds and clawback of awarded cashback (per brief).
- Authentication and authorization (per brief; user identity is passed in a header).
- Products, catalogue, stock (per brief).
- Multiple concurrent campaigns.
- Cashback expiry.
- Fraud and abuse detection (noted as a risk).
- Real payout integration for redemptions.

---

## 5. Open questions

To be resolved in decisions.md:

- Minimum redemption amount (proposed: 1,000 IDR).
- Campaign start and end time beyond budget exhaustion — is there a time window?
- Behaviour for payments whose `paid_at` is far in the past or in the future.
- How payment events reach the service (synchronous API call vs. message consumer) — MVP simulates with an HTTP endpoint.
