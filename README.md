# payment_service

A wallet-to-wallet transfer service in Go with idempotent requests, a double-entry
ledger, and concurrency-safe balances on PostgreSQL.

The design (API contract, schema, transaction boundaries, idempotency, failure modes,
testing strategy) is in [docs/DESIGN.md](docs/DESIGN.md).

## Layout

```
main.go                      wiring, config, graceful shutdown
internal/router              route table and middleware wiring
internal/dto                 HTTP request/response bodies and their mapping from domain types
internal/handler             HTTP transport (gin handlers): decoding, validation, error/status mapping
internal/service             business workflow: idempotency, two-phase transfer, state transitions
                             (also defines the Store/Tx ports it needs)
internal/repository/postgres Store implementation on pgx
internal/repository/redisstore idempotency store on Redis (SET NX + TTL)
migrations                   versioned schema migrations (golang-migrate up/down SQL files)
internal/domain              entities, validation rules, state machine, ledger entry generation
internal/testsupport         per-test isolated PostgreSQL schema for integration tests
```

## Running

Requires Go 1.22+, PostgreSQL (13+), Redis and the
[golang-migrate](https://github.com/golang-migrate/migrate) CLI:

```sh
go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.18.3
```

```sh
make migrate-up   # create/upgrade the tables in DATABASE_URL
make run          # serves on :8080 (it does not run migrations itself)
make migrate-down # roll back the latest migration
```

The server's settings are constants at the top of [main.go](main.go): PostgreSQL
`postgres://postgres:postgres@localhost:5432/payment_service?sslmode=disable`, Redis
`redis://localhost:6379/0`, listen address `:8080`, idempotency TTL `24h`. Edit them there to
point elsewhere. The Makefile's `DATABASE_URL`/`REDIS_URL` are only used by `make migrate-*`
and `make test`.

```sh
curl -s -XPOST localhost:8080/wallets -d '{"initialBalance":1000}'   # -> {"id":1,...}
curl -s -XPOST localhost:8080/wallets -d '{"initialBalance":0}'      # -> {"id":2,...}

curl -si -XPOST localhost:8080/transfers \
  -d '{"idempotencyKey":"abc123","fromWalletId":1,"toWalletId":2,"amount":100}'
# 201 Created; repeating the call returns the same body plus "Idempotent-Replayed: true"

curl -s localhost:8080/wallets/1
curl -s localhost:8080/wallets/1/ledger
```

## Tests

```sh
make test         # everything, including PostgreSQL integration + concurrency tests
make test-unit    # domain and handler tests only; integration tests skip
```

Integration tests read `TEST_DATABASE_URL` and `TEST_REDIS_URL`. Each test creates its own
schema and Redis key prefix and removes them afterwards, so they can run against shared
development instances.
