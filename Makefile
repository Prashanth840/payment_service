DATABASE_URL ?= postgres://postgres:postgres@localhost:5432/payment_service?sslmode=disable

REDIS_URL ?= redis://localhost:6379/0
MIGRATE ?= migrate

.PHONY: db-up db-down migrate-up migrate-down migrate-create run test test-unit lint

db-up:
	docker compose up -d --wait postgres redis

db-down:
	docker compose down -v

# Migrations use golang-migrate (https://github.com/golang-migrate/migrate):
#   go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.18.3
# The server does not migrate on start-up.
migrate-up:
	$(MIGRATE) -path migrations -database "$(DATABASE_URL)" up

# Roll back the most recent migration.
migrate-down:
	$(MIGRATE) -path migrations -database "$(DATABASE_URL)" down 1

# make migrate-create name=add_wallet_currency
migrate-create:
	$(MIGRATE) create -ext sql -dir migrations -seq $(name)

run:
	go run .

# Full suite, including PostgreSQL/Redis integration and concurrency tests.
test:
	TEST_DATABASE_URL="$(DATABASE_URL)" TEST_REDIS_URL="$(REDIS_URL)" go test -race -count=1 ./...

# Unit tests only (integration tests skip without TEST_DATABASE_URL/TEST_REDIS_URL).
test-unit:
	go test -race ./...

lint:
	gofmt -l . && go vet ./...
