package service

import (
	"context"

	"github.com/Prashanth840/payment_service/internal/domain"
)

const (
	DefaultLedgerPageSize = 50
)

type WalletService struct {
	store Store
}

func NewWalletService(store Store) *WalletService {
	return &WalletService{store: store}
}

func (s *WalletService) CreateWallet(ctx context.Context, initialBalance int64) (domain.Wallet, error) {
	w, err := domain.NewWallet(initialBalance)
	if err != nil {
		return domain.Wallet{}, err
	}
	return s.store.CreateWallet(ctx, w)
}

func (s *WalletService) GetWallet(ctx context.Context, id int64) (domain.Wallet, error) {
	return s.store.GetWallet(ctx, id)
}

func (s *WalletService) ListLedgerEntries(ctx context.Context, walletID int64, limit int) ([]domain.LedgerEntry, error) {
	if limit <= 0 {
		limit = DefaultLedgerPageSize
	}

	if _, err := s.store.GetWallet(ctx, walletID); err != nil {
		return nil, err
	}
	return s.store.ListLedgerEntries(ctx, walletID, limit)
}
