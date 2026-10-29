CREATE TABLE ledger_entries (
    id           BIGSERIAL   PRIMARY KEY,
    transfer_id  UUID        NOT NULL REFERENCES transfers (id),
    wallet_id    BIGINT      NOT NULL REFERENCES wallets (id),
    entry_type   TEXT        NOT NULL CHECK (entry_type IN ('DEBIT', 'CREDIT')),
    amount       BIGINT      NOT NULL CHECK (amount > 0),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- At most one debit and one credit per transfer: a buggy retry cannot double-post.
    UNIQUE (transfer_id, entry_type)
);

CREATE INDEX ledger_entries_wallet_idx ON ledger_entries (wallet_id, id DESC);
