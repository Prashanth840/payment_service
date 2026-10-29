package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Prashanth840/payment_service/internal/domain"
	"github.com/Prashanth840/payment_service/internal/service"
)

type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Store struct {
	pool *pgxpool.Pool
}

var _ service.Store = (*Store)(nil)

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) InTx(ctx context.Context, fn func(tx service.Tx) error) error {
	return pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		return fn(&txStore{q: tx})
	})
}

func (s *Store) GetTransfer(ctx context.Context, id string) (domain.Transfer, error) {
	return getTransfer(ctx, s.pool, id, false)
}

func (s *Store) CreateWallet(ctx context.Context, w domain.Wallet) (domain.Wallet, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO wallets (balance) VALUES ($1)
		RETURNING id, balance, created_at, updated_at`, w.Balance)
	return scanWallet(row)
}

func (s *Store) GetWallet(ctx context.Context, id int64) (domain.Wallet, error) {
	row := s.pool.QueryRow(ctx, `SELECT id, balance, created_at, updated_at FROM wallets WHERE id = $1`, id)
	w, err := scanWallet(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Wallet{}, domain.ErrWalletNotFound
	}
	return w, err
}

func (s *Store) ListLedgerEntries(ctx context.Context, walletID int64, limit int) ([]domain.LedgerEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, transfer_id::text, wallet_id, entry_type, amount, created_at
		FROM ledger_entries
		WHERE wallet_id = $1
		ORDER BY id DESC
		LIMIT $2`, walletID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.LedgerEntry, error) {
		var e domain.LedgerEntry
		err := row.Scan(&e.ID, &e.TransferID, &e.WalletID, &e.Type, &e.Amount, &e.CreatedAt)
		return e, err
	})
}

type txStore struct {
	q querier
}

func (t *txStore) EnsureWalletsExist(ctx context.Context, ids ...int64) error {
	var found int
	if err := t.q.QueryRow(ctx, `SELECT count(*) FROM wallets WHERE id = ANY($1)`, ids).Scan(&found); err != nil {
		return err
	}
	if found != len(ids) {
		return domain.ErrWalletNotFound
	}
	return nil
}

func (t *txStore) LockWallets(ctx context.Context, ids ...int64) (map[int64]domain.Wallet, error) {
	rows, err := t.q.Query(ctx, `
		SELECT id, balance, created_at, updated_at
		FROM wallets WHERE id = ANY($1)
		ORDER BY id
		FOR NO KEY UPDATE`, ids)
	if err != nil {
		return nil, err
	}
	wallets, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Wallet, error) {
		return scanWallet(row)
	})
	if err != nil {
		return nil, err
	}
	if len(wallets) != len(ids) {
		return nil, domain.ErrWalletNotFound
	}
	byID := make(map[int64]domain.Wallet, len(wallets))
	for _, w := range wallets {
		byID[w.ID] = w
	}
	return byID, nil
}

func (t *txStore) UpdateWalletBalance(ctx context.Context, w domain.Wallet) error {
	tag, err := t.q.Exec(ctx, `UPDATE wallets SET balance = $2, updated_at = now() WHERE id = $1`, w.ID, w.Balance)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrWalletNotFound
	}
	return nil
}

func (t *txStore) CreateTransferIfAbsent(ctx context.Context, tr domain.Transfer) (domain.Transfer, error) {
	_, err := t.q.Exec(ctx, `
		INSERT INTO transfers (id, from_wallet_id, to_wallet_id, amount, status, failure_reason)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO NOTHING`,
		tr.ID, tr.FromWalletID, tr.ToWalletID, tr.Amount, tr.Status, tr.FailureReason)
	if err != nil {
		return domain.Transfer{}, err
	}
	return scanTransfer(t.q.QueryRow(ctx, `SELECT `+transferColumns+` FROM transfers WHERE id = $1`, tr.ID))
}

func (t *txStore) GetTransferForUpdate(ctx context.Context, id string) (domain.Transfer, error) {
	return getTransfer(ctx, t.q, id, true)
}

func (t *txStore) UpdateTransferStatus(ctx context.Context, tr domain.Transfer, expectedCurrent domain.TransferStatus) (domain.Transfer, error) {
	row := t.q.QueryRow(ctx, `
		UPDATE transfers SET status = $2, failure_reason = $3, updated_at = now()
		WHERE id = $1 AND status = $4
		RETURNING `+transferColumns,
		tr.ID, tr.Status, tr.FailureReason, expectedCurrent)
	updated, err := scanTransfer(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Transfer{}, fmt.Errorf("%w: transfer %s is no longer %s", domain.ErrInvalidTransition, tr.ID, expectedCurrent)
	}
	return updated, err
}

func (t *txStore) InsertLedgerEntries(ctx context.Context, entries []domain.LedgerEntry) error {
	for _, e := range entries {
		_, err := t.q.Exec(ctx, `
			INSERT INTO ledger_entries (transfer_id, wallet_id, entry_type, amount)
			VALUES ($1, $2, $3, $4)`, e.TransferID, e.WalletID, e.Type, e.Amount)
		if err != nil {
			return err
		}
	}
	return nil
}

const transferColumns = `id::text, from_wallet_id, to_wallet_id, amount, status, failure_reason, created_at, updated_at`

func getTransfer(ctx context.Context, q querier, id string, forUpdate bool) (domain.Transfer, error) {
	if _, err := uuid.Parse(id); err != nil {
		return domain.Transfer{}, domain.ErrTransferNotFound
	}
	query := `SELECT ` + transferColumns + ` FROM transfers WHERE id = $1`
	if forUpdate {
		query += ` FOR NO KEY UPDATE`
	}
	tr, err := scanTransfer(q.QueryRow(ctx, query, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Transfer{}, domain.ErrTransferNotFound
	}
	return tr, err
}

func scanTransfer(row pgx.Row) (domain.Transfer, error) {
	var tr domain.Transfer
	err := row.Scan(&tr.ID, &tr.FromWalletID, &tr.ToWalletID, &tr.Amount,
		&tr.Status, &tr.FailureReason, &tr.CreatedAt, &tr.UpdatedAt)
	return tr, err
}

func scanWallet(row pgx.Row) (domain.Wallet, error) {
	var w domain.Wallet
	err := row.Scan(&w.ID, &w.Balance, &w.CreatedAt, &w.UpdatedAt)
	return w, err
}
