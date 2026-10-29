package service

import (
	"context"

	"github.com/Prashanth840/payment_service/internal/domain"
)

type Store interface {
	InTx(ctx context.Context, fn func(tx Tx) error) error

	GetTransfer(ctx context.Context, id string) (domain.Transfer, error)
	CreateWallet(ctx context.Context, w domain.Wallet) (domain.Wallet, error)
	GetWallet(ctx context.Context, id int64) (domain.Wallet, error)
	ListLedgerEntries(ctx context.Context, walletID int64, limit int) ([]domain.LedgerEntry, error)
}

type Tx interface {
	EnsureWalletsExist(ctx context.Context, ids ...int64) error
	LockWallets(ctx context.Context, ids ...int64) (map[int64]domain.Wallet, error)
	UpdateWalletBalance(ctx context.Context, w domain.Wallet) error

	CreateTransferIfAbsent(ctx context.Context, t domain.Transfer) (domain.Transfer, error)
	GetTransferForUpdate(ctx context.Context, id string) (domain.Transfer, error)
	UpdateTransferStatus(ctx context.Context, t domain.Transfer, expectedCurrent domain.TransferStatus) (domain.Transfer, error)

	InsertLedgerEntries(ctx context.Context, entries []domain.LedgerEntry) error
}

type IdempotencyStore interface {
	Claim(ctx context.Context, rec domain.IdempotencyRecord) (stored domain.IdempotencyRecord, created bool, err error)
	Release(ctx context.Context, rec domain.IdempotencyRecord) error
}
