package service_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/Prashanth840/payment_service/internal/domain"
	"github.com/Prashanth840/payment_service/internal/repository/postgres"
	"github.com/Prashanth840/payment_service/internal/repository/redisstore"
	"github.com/Prashanth840/payment_service/internal/service"
	"github.com/Prashanth840/payment_service/internal/testsupport"
)

type fixture struct {
	pool        *pgxpool.Pool
	store       *postgres.Store
	redis       *redis.Client
	redisPrefix string
	idempotency *redisstore.IdempotencyStore
	transfers   *service.TransferService
	wallets     *service.WalletService
}

var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := testsupport.NewPool(t)
	rdb, prefix := testsupport.NewRedis(t)
	store := postgres.NewStore(pool)
	idempotency := redisstore.New(rdb, prefix, time.Hour)
	return &fixture{
		pool:        pool,
		store:       store,
		redis:       rdb,
		redisPrefix: prefix,
		idempotency: idempotency,
		transfers:   service.NewTransferService(store, idempotency, discardLogger),
		wallets:     service.NewWalletService(store),
	}
}

func (f *fixture) createWallet(t *testing.T, balance int64) int64 {
	t.Helper()
	w, err := f.wallets.CreateWallet(context.Background(), balance)
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}
	return w.ID
}

func (f *fixture) balance(t *testing.T, id int64) int64 {
	t.Helper()
	w, err := f.wallets.GetWallet(context.Background(), id)
	if err != nil {
		t.Fatalf("get wallet %d: %v", id, err)
	}
	return w.Balance
}

func (f *fixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func (f *fixture) assertLedgerInvariants(t *testing.T) {
	t.Helper()
	bad := f.count(t, `
		SELECT count(*) FROM transfers t
		WHERE (t.status = 'PROCESSED' AND (
		         (SELECT count(*) FROM ledger_entries e WHERE e.transfer_id = t.id) <> 2
		      OR NOT EXISTS (SELECT 1 FROM ledger_entries e WHERE e.transfer_id = t.id AND e.entry_type = 'DEBIT'
		                     AND e.wallet_id = t.from_wallet_id AND e.amount = t.amount)
		      OR NOT EXISTS (SELECT 1 FROM ledger_entries e WHERE e.transfer_id = t.id AND e.entry_type = 'CREDIT'
		                     AND e.wallet_id = t.to_wallet_id AND e.amount = t.amount)))
		   OR (t.status <> 'PROCESSED' AND EXISTS (SELECT 1 FROM ledger_entries e WHERE e.transfer_id = t.id))`)
	if bad != 0 {
		t.Errorf("%d transfers violate the double-entry rules", bad)
	}
	var debits, credits int64
	err := f.pool.QueryRow(context.Background(), `
		SELECT COALESCE(SUM(amount) FILTER (WHERE entry_type = 'DEBIT'), 0),
		       COALESCE(SUM(amount) FILTER (WHERE entry_type = 'CREDIT'), 0)
		FROM ledger_entries`).Scan(&debits, &credits)
	if err != nil {
		t.Fatal(err)
	}
	if debits != credits {
		t.Errorf("ledger does not balance: debits=%d credits=%d", debits, credits)
	}
}

func mustRequest(t *testing.T, key string, from, to, amount int64) domain.TransferRequest {
	t.Helper()
	req, err := domain.NewTransferRequest(key, from, to, amount)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestCreateTransfer_MovesMoneyAndWritesTwoLedgerEntries(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	w1 := f.createWallet(t, 1000)
	w2 := f.createWallet(t, 0)

	res, err := f.transfers.CreateTransfer(ctx, mustRequest(t, "abc123", w1, w2, 100))
	if err != nil {
		t.Fatal(err)
	}

	if res.Replayed {
		t.Error("first request must not be marked as replayed")
	}
	if res.Transfer.Status != domain.StatusProcessed {
		t.Fatalf("status = %s, want PROCESSED", res.Transfer.Status)
	}
	if got := f.balance(t, w1); got != 900 {
		t.Errorf("wallet_1 balance = %d, want 900", got)
	}
	if got := f.balance(t, w2); got != 100 {
		t.Errorf("wallet_2 balance = %d, want 100", got)
	}

	debits, err := f.wallets.ListLedgerEntries(ctx, w1, 0)
	if err != nil {
		t.Fatal(err)
	}
	credits, err := f.wallets.ListLedgerEntries(ctx, w2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(debits) != 1 || debits[0].Type != domain.EntryDebit || debits[0].Amount != 100 || debits[0].TransferID != res.Transfer.ID {
		t.Errorf("unexpected wallet_1 ledger: %+v", debits)
	}
	if len(credits) != 1 || credits[0].Type != domain.EntryCredit || credits[0].Amount != 100 || credits[0].TransferID != res.Transfer.ID {
		t.Errorf("unexpected wallet_2 ledger: %+v", credits)
	}
	f.assertLedgerInvariants(t)
}

func TestCreateTransfer_InsufficientFundsFailsWithoutSideEffects(t *testing.T) {
	f := newFixture(t)
	w1 := f.createWallet(t, 50)
	w2 := f.createWallet(t, 0)

	res, err := f.transfers.CreateTransfer(context.Background(), mustRequest(t, "k1", w1, w2, 100))
	if err != nil {
		t.Fatal(err)
	}

	if res.Transfer.Status != domain.StatusFailed || res.Transfer.FailureReason != domain.FailureInsufficientFunds {
		t.Fatalf("got %s/%s, want FAILED/INSUFFICIENT_FUNDS", res.Transfer.Status, res.Transfer.FailureReason)
	}
	if f.balance(t, w1) != 50 || f.balance(t, w2) != 0 {
		t.Error("balances must not change on a failed transfer")
	}
	if n := f.count(t, "SELECT count(*) FROM ledger_entries"); n != 0 {
		t.Errorf("failed transfer wrote %d ledger entries", n)
	}
}

func TestCreateTransfer_UnknownWalletIsRejectedAndKeyNotConsumed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	w1 := f.createWallet(t, 100)
	w2 := f.createWallet(t, 0)
	const missing = 999_999

	_, err := f.transfers.CreateTransfer(ctx, mustRequest(t, "k1", w1, missing, 10))
	if !errors.Is(err, domain.ErrWalletNotFound) {
		t.Fatalf("want ErrWalletNotFound, got %v", err)
	}
	if n := f.count(t, "SELECT count(*) FROM transfers"); n != 0 {
		t.Errorf("rejected request created %d transfers", n)
	}

	res, err := f.transfers.CreateTransfer(ctx, mustRequest(t, "k1", w1, w2, 10))
	if err != nil {
		t.Fatal(err)
	}
	if res.Transfer.Status != domain.StatusProcessed {
		t.Errorf("status = %s, want PROCESSED", res.Transfer.Status)
	}
}

func TestCreateTransfer_SameKeyReturnsOriginalResult(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	w1 := f.createWallet(t, 1000)
	w2 := f.createWallet(t, 0)
	req := mustRequest(t, "abc123", w1, w2, 100)

	first, err := f.transfers.CreateTransfer(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		again, err := f.transfers.CreateTransfer(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if !again.Replayed {
			t.Error("retry must be marked as replayed")
		}
		if again.Transfer != first.Transfer {
			t.Errorf("retry returned %+v, want original %+v", again.Transfer, first.Transfer)
		}
	}

	if f.balance(t, w1) != 900 || f.balance(t, w2) != 100 {
		t.Error("retries must not move money again")
	}
	if n := f.count(t, "SELECT count(*) FROM transfers"); n != 1 {
		t.Errorf("transfers = %d, want 1", n)
	}
	if n := f.count(t, "SELECT count(*) FROM ledger_entries"); n != 2 {
		t.Errorf("ledger entries = %d, want 2", n)
	}
}

func TestCreateTransfer_SameKeyReplaysFailedResult(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	w1 := f.createWallet(t, 50)
	w2 := f.createWallet(t, 0)
	req := mustRequest(t, "k1", w1, w2, 100)

	first, err := f.transfers.CreateTransfer(ctx, req)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.pool.Exec(ctx, "UPDATE wallets SET balance = 1000 WHERE id = $1", w1); err != nil {
		t.Fatal(err)
	}
	again, err := f.transfers.CreateTransfer(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if again.Transfer != first.Transfer || again.Transfer.Status != domain.StatusFailed {
		t.Errorf("retry returned %+v, want original FAILED %+v", again.Transfer, first.Transfer)
	}
	if f.balance(t, w1) != 1000 {
		t.Error("replay of a failed transfer must not move money")
	}
}

func TestCreateTransfer_SameKeyDifferentPayloadIsRejected(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	w1 := f.createWallet(t, 1000)
	w2 := f.createWallet(t, 0)

	if _, err := f.transfers.CreateTransfer(ctx, mustRequest(t, "k1", w1, w2, 100)); err != nil {
		t.Fatal(err)
	}
	_, err := f.transfers.CreateTransfer(ctx, mustRequest(t, "k1", w1, w2, 999))
	if !errors.Is(err, domain.ErrIdempotencyKeyReused) {
		t.Fatalf("want ErrIdempotencyKeyReused, got %v", err)
	}
	if f.balance(t, w1) != 900 {
		t.Error("conflicting request must not move money")
	}
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestCreateTransfer_WithoutKeyIdenticalRequestsInWindowAreDeduplicated(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	w1 := f.createWallet(t, 1000)
	w2 := f.createWallet(t, 0)
	clock := &fakeClock{now: time.Date(2026, 1, 1, 12, 0, 10, 0, time.UTC)}
	svc := service.NewTransferService(f.store, f.idempotency, discardLogger,
		service.WithPayloadDedupWindow(time.Minute), service.WithClock(clock.Now))
	req := mustRequest(t, "", w1, w2, 100)

	first, err := svc.CreateTransfer(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(30 * time.Second)
	again, err := svc.CreateTransfer(ctx, req)
	if err != nil {
		t.Fatal(err)
	}

	if first.Replayed || !again.Replayed || again.Transfer != first.Transfer {
		t.Errorf("first replayed=%v, second replayed=%v, same transfer=%v",
			first.Replayed, again.Replayed, again.Transfer == first.Transfer)
	}
	if f.balance(t, w1) != 900 {
		t.Errorf("wallet balance = %d, want 900 (money moved once)", f.balance(t, w1))
	}
}

func TestCreateTransfer_WithoutKeyDifferentPayloadsAreSeparateTransfers(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	w1 := f.createWallet(t, 1000)
	w2 := f.createWallet(t, 0)

	if _, err := f.transfers.CreateTransfer(ctx, mustRequest(t, "", w1, w2, 100)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.transfers.CreateTransfer(ctx, mustRequest(t, "", w1, w2, 50)); err != nil {
		t.Fatal(err)
	}
	if f.balance(t, w1) != 850 {
		t.Errorf("wallet balance = %d, want 850", f.balance(t, w1))
	}
}

func TestCreateTransfer_WithoutKeyIdenticalRequestAfterWindowIsNewTransfer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	w1 := f.createWallet(t, 1000)
	w2 := f.createWallet(t, 0)
	clock := &fakeClock{now: time.Date(2026, 1, 1, 12, 0, 10, 0, time.UTC)}
	svc := service.NewTransferService(f.store, f.idempotency, discardLogger,
		service.WithPayloadDedupWindow(time.Minute), service.WithClock(clock.Now))
	req := mustRequest(t, "", w1, w2, 100)

	first, err := svc.CreateTransfer(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	second, err := svc.CreateTransfer(ctx, req)
	if err != nil {
		t.Fatal(err)
	}

	if second.Replayed || second.Transfer.ID == first.Transfer.ID {
		t.Error("an identical request in a later window must be a new transfer")
	}
	if f.balance(t, w1) != 800 {
		t.Errorf("wallet balance = %d, want 800", f.balance(t, w1))
	}
}

func TestCreateTransfer_RetryCompletesTransferWhenClaimantCrashed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	w1 := f.createWallet(t, 1000)
	w2 := f.createWallet(t, 0)
	req := mustRequest(t, "k1", w1, w2, 100)

	claim := domain.IdempotencyRecord{Key: "k1", RequestHash: req.Hash(), TransferID: "6f1c1f9e-8f53-4a43-9f4b-6d7c2a0a0b11"}
	if _, created, err := f.idempotency.Claim(ctx, claim); err != nil || !created {
		t.Fatalf("claim: created=%v err=%v", created, err)
	}

	res, err := f.transfers.CreateTransfer(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Transfer.ID != claim.TransferID || res.Transfer.Status != domain.StatusProcessed {
		t.Errorf("got %s/%s, want the claimed transfer %s PROCESSED", res.Transfer.ID, res.Transfer.Status, claim.TransferID)
	}
	if f.balance(t, w1) != 900 {
		t.Error("transfer must complete exactly once")
	}
}

type unavailableIdempotencyStore struct{}

func (unavailableIdempotencyStore) Claim(context.Context, domain.IdempotencyRecord) (domain.IdempotencyRecord, bool, error) {
	return domain.IdempotencyRecord{}, false, errors.New("redis: connection refused")
}

func (unavailableIdempotencyStore) Release(context.Context, domain.IdempotencyRecord) error {
	return errors.New("redis: connection refused")
}

func TestCreateTransfer_FailsClosedWhileRedisIsDown(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	w1 := f.createWallet(t, 1000)
	w2 := f.createWallet(t, 0)
	svc := service.NewTransferService(f.store, unavailableIdempotencyStore{}, discardLogger)

	_, err := svc.CreateTransfer(ctx, mustRequest(t, "k1", w1, w2, 100))
	if !errors.Is(err, domain.ErrIdempotencyUnavailable) {
		t.Fatalf("want ErrIdempotencyUnavailable, got %v", err)
	}
	if n := f.count(t, "SELECT count(*) FROM transfers"); n != 0 {
		t.Errorf("transfers = %d, want 0: nothing may happen without deduplication", n)
	}
	if f.balance(t, w1) != 1000 {
		t.Error("money moved while deduplication was unavailable")
	}
}

func TestCreateTransfer_KeyLostFromRedisIsTreatedAsNew(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	w1 := f.createWallet(t, 1000)
	w2 := f.createWallet(t, 0)
	req := mustRequest(t, "k1", w1, w2, 100)

	first, err := f.transfers.CreateTransfer(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if n := f.redis.Del(ctx, f.redisPrefix+"idempotency:k1").Val(); n != 1 {
		t.Fatalf("expected to delete 1 redis key, deleted %d", n)
	}
	second, err := f.transfers.CreateTransfer(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if second.Replayed || second.Transfer.ID == first.Transfer.ID {
		t.Error("with the Redis record gone, the request must be processed as a new transfer")
	}
}

type failingStore struct {
	*postgres.Store
	mu        sync.Mutex
	txCount   int
	failTxNum int
}

var errInjected = errors.New("injected: connection lost")

func (s *failingStore) InTx(ctx context.Context, fn func(service.Tx) error) error {
	s.mu.Lock()
	s.txCount++
	fail := s.txCount == s.failTxNum
	s.mu.Unlock()
	if fail {
		return s.Store.InTx(ctx, func(tx service.Tx) error {
			_ = fn(tx)
			return errInjected
		})
	}
	return s.Store.InTx(ctx, fn)
}

func TestCreateTransfer_RetryResumesInterruptedTransferExactlyOnce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	w1 := f.createWallet(t, 1000)
	w2 := f.createWallet(t, 0)

	flaky := &failingStore{Store: f.store, failTxNum: 2}
	svc := service.NewTransferService(flaky, f.idempotency, discardLogger)
	req := mustRequest(t, "k1", w1, w2, 100)

	if _, err := svc.CreateTransfer(ctx, req); !errors.Is(err, errInjected) {
		t.Fatalf("want injected failure, got %v", err)
	}
	var status string
	if err := f.pool.QueryRow(ctx, "SELECT status FROM transfers").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "PENDING" {
		t.Fatalf("interrupted transfer status = %s, want PENDING", status)
	}
	if f.balance(t, w1) != 1000 {
		t.Fatal("no money may move before the execution transaction commits")
	}

	res, err := svc.CreateTransfer(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Transfer.Status != domain.StatusProcessed {
		t.Fatalf("status after retry = %s, want PROCESSED", res.Transfer.Status)
	}
	if f.balance(t, w1) != 900 || f.balance(t, w2) != 100 {
		t.Error("retry must complete the transfer exactly once")
	}
	f.assertLedgerInvariants(t)
}

func TestProcessTransfer_IsNoOpOnTerminalTransfer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	w1 := f.createWallet(t, 1000)
	w2 := f.createWallet(t, 0)

	res, err := f.transfers.CreateTransfer(ctx, mustRequest(t, "k1", w1, w2, 100))
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.transfers.ProcessTransfer(ctx, res.Transfer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again != res.Transfer {
		t.Errorf("reprocessing changed the transfer: %+v", again)
	}
	if f.balance(t, w1) != 900 {
		t.Error("reprocessing must not move money")
	}
}

func TestConcurrency_DebitsFromSameWalletNeverOverdraw(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	const (
		initial  = 1000
		amount   = 30
		attempts = 50 // 50 * 30 = 1500 > 1000, so some must fail
	)
	source := f.createWallet(t, initial)
	dests := make([]int64, 5)
	for i := range dests {
		dests[i] = f.createWallet(t, 0)
	}

	reqs := make([]domain.TransferRequest, attempts)
	for i := range reqs {
		reqs[i] = mustRequest(t, fmt.Sprintf("key-%d", i), source, dests[i%len(dests)], amount)
	}

	var wg sync.WaitGroup
	results := make(chan service.TransferResult, attempts)
	errs := make(chan error, attempts)
	for _, req := range reqs {
		wg.Add(1)
		go func(req domain.TransferRequest) {
			defer wg.Done()
			res, err := f.transfers.CreateTransfer(ctx, req)
			if err != nil {
				errs <- err
				return
			}
			results <- res
		}(req)
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Errorf("unexpected error: %v", err)
	}
	processed := 0
	for r := range results {
		if r.Transfer.Status == domain.StatusProcessed {
			processed++
		}
	}

	wantProcessed := initial / amount
	if processed != wantProcessed {
		t.Errorf("processed = %d, want %d", processed, wantProcessed)
	}
	if got := f.balance(t, source); got != initial-int64(processed*amount) {
		t.Errorf("source balance = %d, want %d", got, initial-processed*amount)
	}
	if total := f.count(t, "SELECT SUM(balance)::int FROM wallets"); total != initial {
		t.Errorf("total money = %d, want %d (money created or destroyed)", total, initial)
	}
	f.assertLedgerInvariants(t)
}

func TestConcurrency_SameKeyCreatesExactlyOneTransfer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	w1 := f.createWallet(t, 1000)
	w2 := f.createWallet(t, 0)
	req := mustRequest(t, "dup-key", w1, w2, 100)

	const n = 20
	var wg sync.WaitGroup
	ids := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := f.transfers.CreateTransfer(ctx, req)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if res.Transfer.Status != domain.StatusProcessed {
				t.Errorf("status = %s, want PROCESSED", res.Transfer.Status)
			}
			ids <- res.Transfer.ID
		}()
	}
	wg.Wait()
	close(ids)

	seen := map[string]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Errorf("concurrent duplicates returned %d distinct transfers, want 1", len(seen))
	}
	if f.balance(t, w1) != 900 || f.balance(t, w2) != 100 {
		t.Errorf("balances = %d/%d, want 900/100", f.balance(t, w1), f.balance(t, w2))
	}
	if n := f.count(t, "SELECT count(*) FROM ledger_entries"); n != 2 {
		t.Errorf("ledger entries = %d, want 2", n)
	}
}

func TestConcurrency_OppositeDirectionTransfersDoNotDeadlock(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.createWallet(t, 1000)
	b := f.createWallet(t, 1000)

	const perDirection = 25
	var reqs []domain.TransferRequest
	for i := 0; i < perDirection; i++ {
		reqs = append(reqs,
			mustRequest(t, fmt.Sprintf("a-b-%d", i), a, b, 10),
			mustRequest(t, fmt.Sprintf("b-a-%d", i), b, a, 10))
	}

	var wg sync.WaitGroup
	for _, req := range reqs {
		wg.Add(1)
		go func(req domain.TransferRequest) {
			defer wg.Done()
			res, err := f.transfers.CreateTransfer(ctx, req)
			if err != nil {
				t.Errorf("transfer %d->%d: %v", req.FromWalletID, req.ToWalletID, err)
				return
			}
			if res.Transfer.Status != domain.StatusProcessed {
				t.Errorf("status = %s, want PROCESSED", res.Transfer.Status)
			}
		}(req)
	}
	wg.Wait()

	if f.balance(t, a) != 1000 || f.balance(t, b) != 1000 {
		t.Errorf("balances = %d/%d, want 1000/1000", f.balance(t, a), f.balance(t, b))
	}
	f.assertLedgerInvariants(t)
}
