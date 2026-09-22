#!/usr/bin/env bash
# Seeds demo data: users via direct SQL (scripts/seed_users.sql - this
# service doesn't own user creation, see decisions.md "User identity"),
# then payments/redemptions through the real API, exercising the actual
# award/redeem logic same as a real client would. Safe to re-run: the SQL
# is idempotent (ON CONFLICT DO NOTHING) and fixed payment_id/idempotency-
# key values mean a second API run just replays the original results
# (FR-08, FR-16) - it doesn't create duplicates or double-award.
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
DB_USER="${DB_USER:-postgres}"
DB_NAME="${DB_NAME:-flash_cashback}"
NOW="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Fixed, well-known ids - must match scripts/seed_users.sql.
ALICE_ID="11111111-1111-1111-1111-111111111111"
BOB_ID="22222222-2222-2222-2222-222222222222"

if ! curl -sf "$BASE_URL/healthz" >/dev/null; then
    echo "Backend not reachable at $BASE_URL - is 'make up' running?" >&2
    exit 1
fi

post_payment() {
    local payment_id="$1" user_id="$2" amount="$3"
    local result
    result="$(curl -s -X POST "$BASE_URL/payments" \
        -H "Content-Type: application/json" \
        -d "{\"payment_id\":\"$payment_id\",\"user_id\":\"$user_id\",\"amount\":$amount,\"paid_at\":\"$NOW\"}")"
    echo "  payment $payment_id ($user_id, $amount IDR): $result"
}

redeem() {
    local user_id="$1" amount="$2" idempotency_key="$3"
    local result
    result="$(curl -s -X POST "$BASE_URL/cashback/redemptions" \
        -H "Content-Type: application/json" \
        -H "X-User-Id: $user_id" \
        -H "Idempotency-Key: $idempotency_key" \
        -d "{\"amount\":$amount}")"
    echo "  redemption ($user_id, $amount IDR): $result"
}

echo "Seeding demo data against $BASE_URL ..."

echo "creating users (SQL)"
docker compose exec -T postgres psql -U "$DB_USER" -d "$DB_NAME" < "$SCRIPT_DIR/seed_users.sql"
echo "  alice -> $ALICE_ID"
echo "  bob   -> $BOB_ID"
