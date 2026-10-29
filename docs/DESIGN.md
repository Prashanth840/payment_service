# Wallet Transfer Service — Design Note

## 1. Problem statement

Move money between two wallets such that:

- a request carrying an `idempotencyKey` takes effect **at most once**, and every retry
  returns the **original result**;
- every successful transfer writes exactly **two ledger entries** (one DEBIT, one CREDIT)
  of equal amount;
- wallet balances are **always correct**, including under concurrent requests that
  debit the same wallet, and a wallet can never go negative;
- every transfer has an explicit lifecycle `PENDING -> PROCESSED | FAILED` whose
  transitions are safe under retries and duplicate delivery.

Amounts are integers in **minor units** (e.g. cents). A single currency is assumed.
Wallet ids are database-generated integers (`BIGSERIAL`).

## 2. API contract

### `POST /transfers`

Request:

```json
{
  "idempotencyKey": "abc123",
  "fromWalletId": 1,
  "toWalletId": 2,
  "amount": 100
}
```

Validation (rejected with `400`, nothing persisted):

| rule | error code |
|---|---|
| body is valid JSON with no unknown fields | `INVALID_REQUEST` |
| `fromWalletId`, `toWalletId` are positive integers | `INVALID_REQUEST` |
| `fromWalletId != toWalletId` | `INVALID_REQUEST` |
| `amount` is an integer `> 0` | `INVALID_REQUEST` |
| `idempotencyKey` (optional) is ≤ 255 chars | `INVALID_REQUEST` |

Responses (body is always the transfer resource, except for errors):

| outcome | status | body |
|---|---|---|
| transfer processed | `201 Created` | transfer, `status: PROCESSED` |
| business rejection (insufficient funds) | `422 Unprocessable Entity` | transfer, `status: FAILED`, `failureReason` |
| accepted but not yet completed (see §5) | `202 Accepted` | transfer, `status: PENDING` |
| replay of an earlier request | same status and body as the original | + header `Idempotent-Replayed: true` |
| key reused with a different payload | `409 Conflict` | `IDEMPOTENCY_KEY_REUSED` |
| a wallet does not exist | `404 Not Found` | `WALLET_NOT_FOUND` |
| idempotency store (Redis) unreachable | `503 Service Unavailable` | `IDEMPOTENCY_UNAVAILABLE` — nothing written, retry later |
| infrastructure error | `500` | `INTERNAL` — safe to retry with the same key |

Transfer resource:

```json
{
  "id": "8d0c…",
  "fromWalletId": 1,
  "toWalletId": 2,
  "amount": 100,
  "status": "PROCESSED",
  "failureReason": "",
  "createdAt": "…",
  "updatedAt": "…"
}
```

### Supporting endpoints

- `POST /wallets` `{ "initialBalance": 1000 }` — create a wallet; the id is generated (dev/test
  convenience; a real system would fund wallets through transfers from a funding account).
- `GET /wallets/{id}` — current balance.
- `GET /wallets/{id}/ledger` — ledger entries for a wallet, newest first.
- `GET /transfers/{id}` — a single transfer.
- `GET /health` — pings PostgreSQL and Redis.

## 3. Data model

```
wallets              (id BIGSERIAL PK, balance >= 0, created_at, updated_at)
transfers            (id UUID PK, from_wallet_id FK, to_wallet_id FK,
                      amount > 0, status ∈ {PENDING, PROCESSED, FAILED}, failure_reason,
                      CHECK from <> to, created_at, updated_at)
ledger_entries       (id PK, transfer_id FK, wallet_id FK, entry_type ∈ {DEBIT, CREDIT},
                      amount > 0, UNIQUE (transfer_id, entry_type), created_at)
```

Redis (the only idempotency store, see §6):

```
<prefix>idempotency:<idempotencyKey>  →  {"key", "requestHash", "transferId"}   TTL 24h (configurable)
```

Integrity is enforced by the database, not only by code:

- `CHECK (balance >= 0)` — the last line of defence against overdraft.
- `UNIQUE (transfer_id, entry_type)` — a transfer can never have more than one debit or more
  than one credit, so a buggy retry cannot double-post.
- `CHECK` constraints on status/entry type/amount, FKs on every wallet/transfer reference.

Indexes: `ledger_entries (wallet_id, id DESC)` for statement queries,
`ledger_entries (transfer_id)` via the unique constraint, `transfers (from_wallet_id, created_at)`
and `transfers (to_wallet_id, created_at)` for history, and a partial index
`transfers (created_at) WHERE status = 'PENDING'` for finding stuck transfers.

**Balance strategy:** stored balance on `wallets`, updated in the same transaction that writes
the ledger entries. Reads are O(1) and the ledger remains the audit trail; the invariant
`Σ balances` is conserved and `Δbalance(wallet) = Σ credits − Σ debits` is checked in tests.

## 4. Transfer workflow and transaction boundaries

The workflow is split into two short database transactions so that a crash between them
leaves a **recoverable** `PENDING` record instead of an ambiguous outcome.

**Phase 1 — accept (Redis claim + tx #1)**

1. Mint a transfer id and `SET NX` `{requestHash, transferId}` under the key in Redis (with TTL).
   If the key already exists, read the stored record instead; a different `requestHash` → `409`.
   If Redis cannot be reached → `503`; nothing is written.
2. In tx #1: check both wallets exist, then insert the transfer as `PENDING` **under the stored
   transfer id** with `INSERT … ON CONFLICT (id) DO NOTHING`, and read the row back. Every
   request carrying the key does this, so it does not matter which duplicate gets there first
   or whether the request that claimed the key crashed before inserting.
3. If the request was rejected (unknown wallet), release the claim with a compare-and-delete so
   the key is not bound to a transfer that does not exist.

**Phase 2 — execute (tx #2)**

1. `SELECT … FROM transfers WHERE id = $1 FOR NO KEY UPDATE`. If the status is terminal, return it
   unchanged (**guarded transition** — this is what makes retries and concurrent duplicates
   safe).
2. Lock both wallets with a single `SELECT … WHERE id IN (a, b) ORDER BY id FOR NO KEY UPDATE`.
   Locking in a deterministic order prevents deadlocks between `A→B` and `B→A`.
3. If the source balance is insufficient → transition to `FAILED` (`INSUFFICIENT_FUNDS`).
4. Otherwise: debit + credit wallet balances, insert the two ledger entries, transition to
   `PROCESSED` with `UPDATE … WHERE id = $1 AND status = 'PENDING'` (0 rows → error, rollback).
5. Commit. Balance changes, ledger entries and the status change are atomic.

## 5. Concurrency strategy

- **Isolation level:** `READ COMMITTED` plus explicit row locks (`SELECT … FOR NO KEY UPDATE`).
  Pessimistic locking was chosen over optimistic/serializable because transfers on a hot
  wallet are expected to contend; locking serialises them without retry storms, and the
  lock is held only for a few indexed single-row statements.
- **Same wallet debited concurrently:** both transactions block on the wallet row lock; the
  second one re-reads the balance after the first commits, so double spending is impossible.
  The `balance >= 0` constraint backs this up.
- **Opposite-direction transfers:** deterministic lock ordering (by wallet id) prevents
  deadlocks.
- **Lock mode:** `FOR NO KEY UPDATE` rather than `FOR UPDATE`. Inserting a transfer or ledger
  entry takes a `FOR KEY SHARE` lock on the referenced wallets (FK check). `FOR UPDATE` would
  conflict with it, so a phase-1 insert for `B→A` holding `B` could deadlock against a
  phase-2 transaction holding `A` and waiting for `B`. We never change a wallet's key, so
  `NO KEY UPDATE` is the correct (and weaker) mode; it still serialises balance updates.
- **Same idempotency key concurrently:** Redis `SET NX` lets exactly one request mint the
  transfer id; the others read it. All of them insert that same transfer with
  `ON CONFLICT (id) DO NOTHING`, which blocks on the primary key until the first insert commits.
  All requests then reach phase 2; the transfer row lock means exactly one executes and the
  others observe the terminal state.

## 6. Idempotency behaviour

Redis is the **only** record of idempotency keys; the `transfers` table does not store them.

- **Storage:** Redis key `idempotency:<key>` holding `{requestHash, transferId}`, written with
  `SET NX` and a TTL (24h).
- **Detection:** `SET NX` fails if the key exists → read the stored record.
- **Payload check:** `requestHash = sha256(from|to|amount)`; a different payload under the same
  key → `409`.
- **Returning the original result:** the stored transfer id leads to the transfer row, and the
  response is derived from it. Because terminal states never change, the replayed status code
  and body are identical to the original. Replays carry `Idempotent-Replayed: true`.
- **Preventing duplicate side effects:** money only moves inside phase 2, guarded by the
  transfer row lock and the `status = 'PENDING'` predicate, and backed by the
  `UNIQUE (transfer_id, entry_type)` ledger constraint.
- **Redis unavailable:** `POST /transfers` fails closed with `503`. Without Redis a duplicate
  could not be detected, so no transfer is accepted.
- **Key scope:** keys are global. Requests rejected by validation or `404` release their claim
  (no side effects happened), so the client may fix the request and reuse the key.
- **No key supplied:** the server derives one from the payload and a fixed time window:
  `auto:<sha256(from|to|amount)>:<window start>` (window 1 minute). Identical requests within
  the same window return the original transfer. Trade-offs: a legitimate repeat payment within
  the window is treated as a duplicate (send distinct keys to avoid that), and two identical
  requests either side of a window boundary are *not* deduplicated. Clients that need exact
  retry safety should send an `idempotencyKey`.

**Limitation (accepted trade-off).** Redis and PostgreSQL cannot commit atomically, and with
no durable copy of the key, guarantees last only as long as the Redis record does. If the
record is lost — TTL expiry (24h), eviction under memory pressure, or a failover that drops
recent writes — a retry with that key is treated as a new request and **moves money again**.
Mitigations if this matters: run Redis with `maxmemory-policy noeviction` and AOF persistence
(`appendfsync everysec` or `always`), keep the TTL longer than any client's retry horizon, or
add a durable key table in PostgreSQL written in tx #1.

## 7. Failure modes and retry behaviour

| failure | state afterwards | client action |
|---|---|---|
| validation / wallet not found | nothing written, Redis claim released | fix and retry (key not consumed) |
| crash after Redis claim, before tx #1 commits | Redis record only, no transfer row | retry with same key → inserts the claimed transfer |
| crash/DB error during tx #1 | tx rolled back, no transfer row | retry with same key |
| Redis down | nothing written (`503`) | retry later with the same key |
| Redis record lost (TTL, eviction, failover) | key unknown | a retry is processed as a **new** transfer (see §6) |
| crash/DB error during phase 2 | transfer `PENDING`, no money moved | retry with same key → resumes |
| response lost on the network | transfer terminal | retry with same key → original result |
| insufficient funds | transfer `FAILED` (terminal) | new key for a new attempt |

A production deployment would add a background sweeper that picks up `PENDING` transfers
older than N seconds (using the partial index) and runs phase 2 for them, so transfers
complete even if the client never retries. `TransferService.ProcessTransfer` is the
entry point such a worker would call.

## 8. Observability

- Structured JSON logs (`log/slog`). Every transfer outcome is logged with `transfer_id`,
  `idempotency_key`, `status`, `replayed`, and failure reason; unexpected errors are logged
  in full while clients get a generic `INTERNAL` response.
- `/health` pings PostgreSQL and Redis; transfers cannot be accepted without either.
- Not implemented (would add next): request ids and access logs, panic-recovery middleware,
  a request body size limit, Prometheus counters for transfers by outcome, a histogram for
  phase-2 latency/lock wait, and an alert on PENDING transfers older than N s.

## 9. Testing strategy

- **Domain unit tests** — state transitions (including illegal ones), request validation,
  debit/credit rules, ledger entry generation.
- **Handler tests** — request validation and error/status mapping against a fake service.
- **Redis store tests** — first claim wins, TTL is set, release only deletes the caller's record.
- **Integration tests (PostgreSQL + Redis)** — run the real service + repositories against an
  isolated schema and key prefix:
  - successful transfer, balances and the two ledger entries;
  - insufficient funds → `FAILED`, no ledger entries, no balance change;
  - idempotent replay returns the original result; key reuse with different payload → conflict;
  - a transfer interrupted after phase 1 is resumed exactly once by a retry;
  - Redis lost the record: the request is processed as new (pins the documented limitation);
  - the request that claimed the key crashed before inserting: a retry completes that transfer;
  - Redis down: the request is refused and nothing is written;
  - no key: identical requests in one window are deduplicated, different payloads are not,
    and the same payload in a later window is a new transfer;
  - **concurrency:** N concurrent debits from one wallet never overdraw it and conserve
    total money; N concurrent requests with the same key create exactly one transfer;
    opposite-direction transfers complete without deadlock;
  - ledger invariant: Σ debits = Σ credits, and every transfer has exactly 2 or 0 entries.
