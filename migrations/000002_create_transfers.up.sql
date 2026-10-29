CREATE TABLE transfers (
    id               UUID        PRIMARY KEY,
    idempotency_key  TEXT        NOT NULL UNIQUE,
    from_wallet_id   BIGINT      NOT NULL REFERENCES wallets (id),
    to_wallet_id     BIGINT      NOT NULL REFERENCES wallets (id),
    amount           BIGINT      NOT NULL CHECK (amount > 0),
    status           TEXT        NOT NULL CHECK (status IN ('PENDING', 'PROCESSED', 'FAILED')),
    failure_reason   TEXT        NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (from_wallet_id <> to_wallet_id),
    CHECK ((status = 'FAILED') = (failure_reason <> ''))
);

CREATE INDEX transfers_from_wallet_idx ON transfers (from_wallet_id, created_at);
CREATE INDEX transfers_to_wallet_idx   ON transfers (to_wallet_id, created_at);
-- Lets a recovery worker find stuck transfers without scanning the table.
CREATE INDEX transfers_pending_idx     ON transfers (created_at) WHERE status = 'PENDING';
