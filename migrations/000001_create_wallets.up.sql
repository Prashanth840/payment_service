-- Amounts are BIGINT minor units (e.g. cents); a single currency is assumed.
CREATE TABLE wallets (
    id          BIGSERIAL   PRIMARY KEY,
    balance     BIGINT      NOT NULL CHECK (balance >= 0),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
