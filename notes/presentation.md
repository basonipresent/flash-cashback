# Flash Cashback — Project Presentation

Prep notes for talking through this project live — structured as **Context → Approach → Trade-offs → Proof → Caveats/what's next**, which fits a whole-project walkthrough better than STAR (STAR's Situation/Task split blurs together outside a single incident, and has no natural slot for trade-offs). The one place STAR *does* fit — the "tell me about a bug" question — is labeled that way on purpose, down in the Q&A. Full technical detail lives in `spec/` (`requirements.md`, `design.md`, `decisions.md`, `invariants.md`, `risks.md`, `api.yaml`); this is the "explain it out loud" version.

Status: backend and mobile app both implemented and verified — not a mockup, the numbers in the demo are real.

---

## 1. Background — what was actually being asked

The brief, boiled down: users earn 5% cashback on payments over 20,000 IDR, capped at 50,000 IDR per user per day and a 10,000,000 IDR total campaign budget, and can redeem their balance. Go, Postgres, Redis, React Native. Built with AI assistance, but "MVP, production grade" — small scope, but everything shipped ready for production.

The brief is clear on the happy path, but silent in several places that matter. Nowhere does it say what happens when two payments for the same user land in the same millisecond. Nowhere does it say what the minimum redemption amount is, whether the campaign has a time window, or which reason code wins when a payment hits the daily cap and the campaign budget at the exact same instant. None of that is an oversight. A service that "moves real money," graded on production-readiness, that silently guesses at those gaps or doesn't handle concurrent writes correctly isn't a smaller version of a correct system — it's a wrong one that happens to work in a single-threaded demo.

So the real scope of this project was two things, in order: (1) find every place the brief was silent or ambiguous, make a real decision with real reasoning instead of a silent guess, and write it down; (2) prove — not assert — that the money math holds under concurrent access, since that's the one property a live demo can't easily fake its way past.

---

## 2. Approach — how I got from that to a working system

**Spec before code.** Every ambiguous point got resolved in `spec/decisions.md` *before* it got implemented, each with the reasoning and the alternative I rejected, not just the answer — eight of them by the end (minimum redemption amount, campaign time window, the reason-code tie-break, `paid_at` bounds, idempotency-key conflict handling, payment ingestion transport, campaign-budget visibility, user identity). If I made a call without writing down why, that's exactly the "silent guess" the brief was testing for.

**Every gap named in Background got answered directly, not left open:** the same-millisecond concurrency problem is exactly what the locking mechanism below exists to solve; the minimum redemption amount, the campaign time window, and the reason-code tie-break are each resolved in Trade-offs, with the alternative I rejected and why. Proof then shows three of those four aren't just decided on paper.

**Architecture, stateless by design:**

```
Mobile (Expo, RN)  --HTTPS, X-User-Id header-->  Go API (net/http, stateless)
                                                        |
                                                        v
                                              Postgres (source of truth)
                                              Redis (readyz only, so far)
```

No web framework, no ORM — stdlib `net/http` with Go 1.22+ method-pattern routing, `pgx/v5`, `go-redis/v9`. The API holds no state between requests; every cross-request coordination (daily cap, budget, idempotency) happens in Postgres via row locks. That's what makes "run three replicas behind a load balancer" a non-event instead of a redesign.

**The core mechanism:** every payment is processed inside one transaction that locks the campaign-budget row, then the user's daily-cap row, in that fixed order (avoids deadlocks), computes the awardable amount against both remaining caps, and commits everything — the award, the ledger entry, the updated counters — atomically. Idempotency in both directions: a payment's `payment_id` is a claim row inserted with `ON CONFLICT DO NOTHING`, so a concurrent duplicate loses the race at the database level and just reads back the winner's result instead of double-awarding; redemptions work the same way keyed on `(user_id, idempotency_key)`.

**What that produced:** the full cashback lifecycle (ingest → award → cap enforcement → redeem) with all six reason codes, idempotency that actually holds under concurrency, the full API surface (`POST /payments`, `GET /cashback/{balance,daily,history}`, `POST /cashback/redemptions`, `GET /campaign`), and a mobile screen wired to the real API — balance, today's usage, campaign status, redeem, full history — that never computes cashback itself, only displays what the server says (NFR-07, and it's load-bearing: a client that *could* compute its own cashback is a client you can't trust).

---

## 3. Trade-offs — the decisions with a real alternative I seriously considered and rejected

These first four answer the exact gaps named in Background, in the order they were raised there. The rest are trade-offs the project also needed but that Background didn't call out by name.

### 1. Same-millisecond concurrent payments: a single lock on the budget row vs. a lock-free counter
A lock-free `UPDATE ... WHERE remaining >= 0` pattern would scale further, but can't correctly compute a *partial* award — you need the exact remaining amount, not just whether some is left — so you need a second read anyway, and most of the throughput win evaporates. I chose the lock: simple and obviously correct beats a scheme that's harder to verify and doesn't buy much at this budget size (10,000,000 IDR — the campaign is short-lived regardless).

### 2. Minimum redemption amount: 1,000 IDR vs. a higher accumulation floor
The brief left this unspecified. 1,000 IDR is exactly the smallest single cashback award possible under the rules (5% of the 20,000 IDR minimum-eligible payment) — set the floor there and a user is never left holding an unredeemable balance after just one qualifying payment. The real alternative was a higher floor, the more common real-world pattern (forces a little accumulation before the first redemption) — rejected because it serves no purpose here beyond that, and a lower floor is meaningless since no smaller award can ever exist. Enforced as `MinRedemptionIDR` in the redemption handler, asserted by `TestRedeem_BelowMinimumRejected`.

### 3. Campaign time window: a budget-only end condition vs. a time window, or both with a first-to-end rule
The brief specifies exactly one end condition — the campaign ends when the 10,000,000 IDR budget is exhausted — and says nothing about dates. I considered adding a time window anyway, since real flash-sale campaigns almost always run both a clock and a budget, with "whichever hits first ends it" as the obvious combined rule. I rejected it: it's an unrequested requirement, `campaign_budget` would need `start_at`/`end_at` columns, and `CAMPAIGN_ENDED` would need to test two conditions instead of one — two sources of truth that can disagree about which one actually ended the campaign, versus a single boolean check today (`awarded_total_idr >= total_budget_idr`). Not closed off, either: if a real time-bound promotion needed it later, those columns slot onto the same table without touching the ledger or award logic — additive, not a rewrite.

### 4. Reason-code tie-break when both caps bind at once: budget wins
When the daily cap and the campaign budget both cap the same payment at the exact same remaining amount, the awarded total is identical either way — only the reported reason code differs. Two alternatives considered: keep the previous `PARTIAL_DAILY_CAP` default (frames it as "you personally hit your limit," understating that the campaign itself is also on the edge for everyone), or add a new combined `PARTIAL_DAILY_CAP_AND_BUDGET` code (most literally accurate, but expands FR-09's fixed set of six reason codes for a rare exact-tie edge case, forcing every consumer — the mobile app included — to handle a 7th value). Chose `PARTIAL_BUDGET`: campaign-budget exhaustion is a global, one-time event affecting every user, more significant to surface than a personal daily cap that just resets tomorrow. Covered by `TestComputeAward`'s exact-tie case, not just decided on paper.

### Synchronous HTTP vs. a message queue for payment ingestion
A real payment processor would probably want a queue for delivery guarantees during an outage. Not load-bearing at this project's scale — a reviewer running a demo, not a system surviving a real provider's traffic — and it's not a one-way door: the idempotency-on-`payment_id` design is exactly what a queue consumer would need too, so the award logic itself wouldn't change if this moved to async later.

### Materialized counters vs. recomputing from the ledger on every read
Balance, daily total, and budget consumed are running counters kept in sync with the append-only ledger by transaction discipline, not `SUM()`'d on every read. Cheap on the hot path; the cost is a counter could in principle drift if a future change breaks that discipline, with nothing catching it automatically today. Accepted as a documented risk rather than building a reconciliation job that nothing in this scope currently needs.

### A `POST /users` identity endpoint vs. a SQL seed script
I built the API version first — find-or-create a user by name, same race-safe pattern as payments. Then pulled it back out: user creation isn't something the brief asked this service to do ("assume the user is known"), and an endpoint that manufactures identities grows the API surface for a concern this service doesn't own. `users` is now seeded by a plain SQL script with fixed demo ids. Worse standalone UX (paste a UUID instead of typing a name) in exchange for being honest about what this system actually is.

### Redis in the stack vs. Redis actually doing something
Redis is required by the brief and wired up (`/readyz` depends on it), but today it does nothing beyond that health check — every read and write goes straight to Postgres. I considered building its two obvious jobs now: a short-TTL cache for `GET /campaign`/`GET /cashback/balance`, and a token-bucket rate limiter on `POST /payments`/`POST /cashback/redemptions`. I didn't, for the same reason twice over: there's no real traffic in a demo to validate either against, and this project's whole credibility rests on claims backed by a concurrency test, not "should work" — a cache or limiter I can't load-test is exactly the kind of unverified code this project has otherwise refused to ship. Leaving Redis idle costs nothing correctness-wise (NFR-02: it was never allowed to be authoritative for money anyway) and keeps that discipline consistent; the trade-off is real optimization left on the table, priced and ready to build (TTL-only cache first — it can't lie for long — invalidate-on-write only if staleness actually becomes a problem).

---

## 4. Proof — how I know it actually holds, not just that I designed it to

Design intent and working code are different claims, and the gap between them is exactly where "concurrent-safe" systems usually fail quietly. So `spec/invariants.md` states the correctness properties as formal claims — *"the campaign never awards more than its budget, always, including at the moment two concurrent payments both attempt to consume the last remaining budget"* — and each one has a test that tries to violate it under real concurrent load against a real Postgres, not a mock.

Two concrete numbers: 20 simultaneous goroutines each submitting a payment worth 5,000 IDR cashback against a single user's 50,000 IDR daily cap (100,000 requested, 50,000 available) — the test asserts the final total is *exactly* 50,000, not "close to it." 300 simultaneous goroutines across 300 distinct users each requesting 50,000 IDR cashback against the 10,000,000 IDR campaign budget (15,000,000 requested) — same assertion, exact final total, no over-award.

The other three gaps named in Background aren't just written decisions either — they're asserted the same way. `TestRedeem_BelowMinimumRejected` proves the 1,000 IDR floor is actually enforced, not just documented. `TestComputeAward`'s exact-tie case proves the reason-code tie-break resolves to `PARTIAL_BUDGET`, not whatever the code happened to fall through to. The campaign time window is the one exception, and deliberately so: there's no invariant to test because nothing is computed — the decision was to add no date logic at all, so its proof is structural (no `start_at`/`end_at` columns exist to get wrong) rather than a test result.

~30 automated tests total: pure-function unit tests for the award-computation logic, and integration tests that exercise real concurrency, all clean under `-race` — reproducible with `make test-integration`, which runs them against a disposable database so it never touches seeded demo data.

---

## 5. Caveats & what's next

What I'd flag before anyone asks, paired with what I'd actually do about each if this needed to go further than a demo:

- **No authentication.** `X-User-Id` is a plain, unverified header. This is what the brief explicitly asked for, but it's the one item I'd call a hard blocker, not a nice-to-have, before this touches real money — real auth would be step one of any next phase.
- **`paid_at` can be backdated.** Future timestamps are rejected (a payment can't succeed before the event reporting it exists), but the past is deliberately unbounded, since legitimate backfill needs that slack. Next step if it mattered: restrict who can call `POST /payments` at the network layer, since that's the actual protection, not a tighter timestamp rule.
- **Counter drift is possible in theory, undetected in practice.** No automated job reconciles the materialized counters against `SUM(ledger)`. I know exactly what that job would look like; building it wasn't justified by anything this project's scope exercises.
- **The campaign-budget row is a single global write bottleneck.** Fine at this budget size. At real throughput: shard the counter, or revisit the lock-free pattern I rejected earlier, once there's load data to justify the complexity.
- **Redis is idle** beyond a health check — see "Redis in the stack vs. Redis actually doing something" in Trade-offs above for why and what it'd take.
- **No fraud/abuse detection.** The mitigation is entirely "this endpoint shouldn't be reachable by end users," a deployment/network concern, not something the code enforces today.
- **The mobile app is one screen**, there to prove the API works end-to-end from a real client, not to demonstrate product design.

---

## Questions I'd expect, and how I'd answer them

**"How do you actually guarantee the daily cap and budget never get exceeded under concurrent load?"**
Row-level locks on the two shared counters, inside one transaction per payment, in a fixed lock order to avoid deadlocks. Backed by a test that fires 300 concurrent goroutines requesting 1.5x the budget and asserts the final total is exactly the budget.

**"What happens if the same payment_id arrives twice at the same instant?"**
Postgres serializes it for me: the claim-row insert on `payment_id` uses `ON CONFLICT DO NOTHING`, and concurrent inserts on the same key block against each other at the database level. The loser sees 0 rows back, rolls back, and reads the winner's committed result instead of double-processing.

**"Why Postgres as the source of truth instead of Redis, given Redis is in the stack anyway?"**
It's not about atomicity — Redis has `MULTI`/`EXEC` and Lua scripts, so atomic operations aren't the gap. It's durability and recovery: an award has to survive a crash and be reconstructable afterward from an append-only record, and Redis's persistence model doesn't give me that guarantee the way Postgres's WAL and `ledger` table do. Redis is there for future read-optimization, never for correctness — that's stated explicitly in my own spec (NFR-02).

**"What would you change for a real production deployment?"**
In order: real authentication, full stop. Then a reconciliation job for the counters. Then probably a queue for payment ingestion once there's an actual provider with real delivery-guarantee needs on the other end. The core award/redeem logic wouldn't need to change for any of these — that's by design.

**"Tell me about a bug you ran into." (STAR)**
*Situation:* a concurrency test showed a redemption balance that looked wrong after 10 simultaneous identical requests. *Task:* figure out if the redemption logic itself was actually broken before I went fixing anything. *Action:* instead of assuming the bug was where the test pointed, I pulled the raw `redemptions` and `ledger` rows directly from Postgres. *Result:* the redemption idempotency logic was correct the whole time — the bug was in my *test's own* setup helper, which seeded a starting balance via a payment large enough to silently get capped by the daily limit, so the test's assumed starting balance was wrong. I'd rather tell that story than pretend nothing went sideways; it's a decent example of not trusting a failing test's first explanation of itself.

**"Why did you write spec docs before writing any code?"**
Because the brief's real ask was judgment, not typing speed. Every ambiguous point got a written decision with reasoning and rejected alternatives instead of a silent guess baked into the code — eight of them by the end, and nothing shipped without one.

**"How would this evolve to support multiple currencies?"**
Every table that holds money (`payments`, `ledger`, `campaign_budget`, `user_daily_cashback`, `user_balance`, `redemptions`) would need a `currency` column next to the integer amount — right now "10,000,000" is implicitly IDR, and that stops being safe the moment a second currency exists. The real design fork is whether the campaign stays a single budget pot (foreign-currency payments get converted at award time, using an exchange rate fetched once and frozen onto that ledger entry — never recomputed later, same discipline as how `payment_id` idempotency already locks in a result at first insert) or becomes one budget per currency, which is simpler locking but changes what "one campaign" means. Converting to a base currency is the more realistic answer but introduces a failure mode this design has zero of today — a stale or wrong exchange rate — and it's the one place Redis would finally earn its keep, caching rates without touching the award transaction's correctness path.

**"If there were multiple campaigns running at once, which one deducts first?"**
Today there's no multi-campaign concept at all — `campaign_budget` is a hardcoded singleton row, so the question isn't answerable by the current schema, not just unhandled at runtime. My approach: give campaigns an explicit `priority` rather than an implicit rule like "biggest budget" or "earliest end date" — those sound reasonable until two campaigns tie, whereas an admin-set priority is deterministic and auditable, same reasoning already used for the daily-cap/budget tie-break (pick one rule, write down why, don't invent a fuzzy heuristic). Concretely: a `campaigns` table, a `campaign_id` FK added to `payments` and `ledger` so the append-only ledger still says which campaign paid for every award, and at award time, lock only the highest-priority active campaign with budget left — same transaction shape as today, just against a selected row instead of a fixed one. The trade-off I'd bake in for v1: one campaign per payment, no splitting a partial award across two campaigns if the first one runs out mid-payment — splitting leaves less cashback unclaimed, but it turns one reason code and one ledger entry per payment into a variable-length chain, the same kind of complexity this project already rejected once when it picked a single tie-break winner instead of a combined reason code.

**"How would this scale to real traffic?"**
The single-lock design caps global award throughput — that's the known ceiling, and I said so above rather than waiting to be asked. Fix is sharding the counter or the lock-free pattern I already evaluated and set aside, revisited with real load data. Reads already scale horizontally since the API is stateless; that part isn't the bottleneck.

**"What's the one thing you'd point to as evidence this is 'production grade' and not just a demo?"**
The invariants doc and the tests enforcing it. Not "I think this is correct" — "I can show you it's correct, including the two numbers where it matters most."
