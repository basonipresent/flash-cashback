#!/usr/bin/env bash
# Fixed demo user ids seeded by scripts/seed_users.sql (via `make seed`).
ALICE="${ALICE:-11111111-1111-1111-1111-111111111111}"
BOB="${BOB:-22222222-2222-2222-2222-222222222222}"

# AWARDED (5% of 100,000)
curl -s -X POST http://localhost:8080/payments -H "Content-Type: application/json" \
  -d "{\"payment_id\":\"p1\",\"user_id\":\"$ALICE\",\"amount\":100000,\"paid_at\":\"2026-09-22T10:00:00+07:00\"}"

# BELOW_MINIMUM (under 20,000)
curl -s -X POST http://localhost:8080/payments -H "Content-Type: application/json" \
  -d "{\"payment_id\":\"p2\",\"user_id\":\"$ALICE\",\"amount\":10000,\"paid_at\":\"2026-09-22T10:00:00+07:00\"}"

# PARTIAL_DAILY_CAP then DAILY_CAP_REACHED (bob, fresh daily allowance)
curl -s -X POST http://localhost:8080/payments -H "Content-Type: application/json" \
  -d "{\"payment_id\":\"p3\",\"user_id\":\"$BOB\",\"amount\":400000,\"paid_at\":\"2026-09-22T10:00:00+07:00\"}"
curl -s -X POST http://localhost:8080/payments -H "Content-Type: application/json" \
  -d "{\"payment_id\":\"p4\",\"user_id\":\"$BOB\",\"amount\":400000,\"paid_at\":\"2026-09-22T10:00:00+07:00\"}"
curl -s -X POST http://localhost:8080/payments -H "Content-Type: application/json" \
  -d "{\"payment_id\":\"p5\",\"user_id\":\"$BOB\",\"amount\":400000,\"paid_at\":\"2026-09-22T10:00:00+07:00\"}"
curl -s -X POST http://localhost:8080/payments -H "Content-Type: application/json" \
  -d "{\"payment_id\":\"p6\",\"user_id\":\"$BOB\",\"amount\":400000,\"paid_at\":\"2026-09-22T10:00:00+07:00\"}"

# Idempotent retry - same payment_id, identical result, no double-award
curl -s -X POST http://localhost:8080/payments -H "Content-Type: application/json" \
  -d "{\"payment_id\":\"p1\",\"user_id\":\"$ALICE\",\"amount\":100000,\"paid_at\":\"2026-09-22T10:00:00+07:00\"}"

# paid_at too far in the future -> 400
curl -s -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/payments -H "Content-Type: application/json" \
  -d "{\"payment_id\":\"p7\",\"user_id\":\"$ALICE\",\"amount\":100000,\"paid_at\":\"2026-09-22T23:59:59+07:00\"}"

# Unknown user_id (never seeded via scripts/seed_users.sql) -> 400
curl -s -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/payments -H "Content-Type: application/json" \
  -d '{"payment_id":"p8","user_id":"00000000-0000-0000-0000-000000000000","amount":100000,"paid_at":"2026-09-22T10:00:00+07:00"}'

# Missing fields -> 400
curl -s -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/payments -H "Content-Type: application/json" -d '{"amount":100000}'
