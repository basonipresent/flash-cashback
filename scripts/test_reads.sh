#!/usr/bin/env bash
# Fixed demo user ids seeded by scripts/seed_users.sql (via `make seed`).
ALICE="${ALICE:-11111111-1111-1111-1111-111111111111}"
BOB="${BOB:-22222222-2222-2222-2222-222222222222}"

curl -s http://localhost:8080/cashback/balance -H "X-User-Id: $ALICE"
curl -s http://localhost:8080/cashback/daily   -H "X-User-Id: $ALICE"
curl -s http://localhost:8080/cashback/history -H "X-User-Id: $ALICE"

curl -s http://localhost:8080/cashback/balance -H "X-User-Id: $BOB"
curl -s http://localhost:8080/cashback/daily   -H "X-User-Id: $BOB"
curl -s http://localhost:8080/cashback/history -H "X-User-Id: $BOB"

curl -s http://localhost:8080/campaign

# Missing X-User-Id -> 400
curl -s -w "\nHTTP %{http_code}\n" http://localhost:8080/cashback/balance
