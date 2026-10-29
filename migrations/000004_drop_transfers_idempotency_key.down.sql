-- Keys of existing transfers are not recoverable, so the column comes back nullable.
ALTER TABLE transfers ADD COLUMN idempotency_key TEXT UNIQUE;
