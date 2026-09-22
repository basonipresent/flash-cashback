-- Known identities this service can award/redeem cashback for. Minimal by
-- design: no auth (requirements.md §4), and this service doesn't create
-- users either (decisions.md "User identity") - rows are populated
-- directly by scripts/seed_users.sql, not through an API.
CREATE TABLE users (
    id         UUID PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per ingested payment event; also the idempotency record for FR-08.
CREATE TABLE payments (
    payment_id           TEXT PRIMARY KEY,
    user_id              UUID NOT NULL REFERENCES users (id),
    amount_idr           BIGINT NOT NULL,
    paid_at              TIMESTAMPTZ NOT NULL,
    cashback_date        DATE NOT NULL,
    base_cashback_idr    BIGINT NOT NULL DEFAULT 0,
    awarded_cashback_idr BIGINT NOT NULL DEFAULT 0,
    reason_code          TEXT NOT NULL DEFAULT '',
    processed_at         TIMESTAMPTZ
);

CREATE INDEX idx_payments_user_id ON payments (user_id);

-- Singleton row tracking total and consumed campaign budget.
CREATE TABLE campaign_budget (
    id                 INT PRIMARY KEY,
    total_budget_idr   BIGINT NOT NULL,
    awarded_total_idr  BIGINT NOT NULL DEFAULT 0,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO campaign_budget (id, total_budget_idr, awarded_total_idr)
VALUES (1, 10000000, 0);

-- Per-user, per-WIB-day running total, for the daily cap.
CREATE TABLE user_daily_cashback (
    user_id           UUID NOT NULL REFERENCES users (id),
    cashback_date     DATE NOT NULL,
    awarded_total_idr BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, cashback_date)
);

-- Append-only source of truth for money movement.
CREATE TABLE ledger (
    id          BIGSERIAL PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users (id),
    entry_type  TEXT NOT NULL CHECK (entry_type IN ('AWARD', 'REDEEM')),
    amount_idr  BIGINT NOT NULL,
    ref_type    TEXT NOT NULL CHECK (ref_type IN ('PAYMENT', 'REDEMPTION')),
    ref_id      TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_ledger_user_id_created_at ON ledger (user_id, created_at);

-- Materialized balance for O(1) reads.
CREATE TABLE user_balance (
    user_id     UUID PRIMARY KEY REFERENCES users (id),
    balance_idr BIGINT NOT NULL DEFAULT 0,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE redemptions (
    id               UUID PRIMARY KEY,
    user_id          UUID NOT NULL REFERENCES users (id),
    amount_idr       BIGINT NOT NULL,
    idempotency_key  TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, idempotency_key)
);
