#!/usr/bin/env bash
# Fixed demo user ids seeded by scripts/seed_users.sql (via `make seed`).
ALICE="${ALICE:-11111111-1111-1111-1111-111111111111}"
BOB="${BOB:-22222222-2222-2222-2222-222222222222}"

# Happy path
curl -s -X POST http://localhost:8080/cashback/redemptions -H "Content-Type: application/json" \
  -H "X-User-Id: $ALICE" -H "Idempotency-Key: r1" -d '{"amount":2000}'

# Idempotent retry - same key, same amount -> identical result, no double-decrement
curl -s -X POST http://localhost:8080/cashback/redemptions -H "Content-Type: application/json" \
  -H "X-User-Id: $ALICE" -H "Idempotency-Key: r1" -d '{"amount":2000}'

# Idempotency-key conflict - same key, different amount -> 409
curl -s -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/cashback/redemptions -H "Content-Type: application/json" \
  -H "X-User-Id: $ALICE" -H "Idempotency-Key: r1" -d '{"amount":3000}'

# Below minimum (under 1,000) -> 400
curl -s -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/cashback/redemptions -H "Content-Type: application/json" \
  -H "X-User-Id: $ALICE" -H "Idempotency-Key: r2" -d '{"amount":500}'

# Exceeds balance -> 400
curl -s -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/cashback/redemptions -H "Content-Type: application/json" \
  -H "X-User-Id: $ALICE" -H "Idempotency-Key: r3" -d '{"amount":999999}'

# Unknown user -> 400
curl -s -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/cashback/redemptions -H "Content-Type: application/json" \
  -H "X-User-Id: 00000000-0000-0000-0000-000000000000" -H "Idempotency-Key: r4" -d '{"amount":2000}'

# Missing Idempotency-Key -> 400
curl -s -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/cashback/redemptions -H "Content-Type: application/json" \
  -H "X-User-Id: $ALICE" -d '{"amount":2000}'

# BOB: happy path
curl -s -X POST http://localhost:8080/cashback/redemptions -H "Content-Type: application/json" \
  -H "X-User-Id: $BOB" -H "Idempotency-Key: bob-r1" -d '{"amount":5000}'
