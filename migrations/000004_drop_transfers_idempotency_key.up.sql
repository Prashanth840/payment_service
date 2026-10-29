-- Idempotency keys live only in Redis; transfers no longer record them.
ALTER TABLE transfers DROP COLUMN idempotency_key;
