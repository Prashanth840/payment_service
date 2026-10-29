package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/Prashanth840/payment_service/internal/domain"
)

type TransferResult struct {
	Transfer domain.Transfer
	Replayed bool
}

const DefaultPayloadDedupWindow = time.Minute

type TransferService struct {
	store              Store
	idempotency        IdempotencyStore
	logger             *slog.Logger
	payloadDedupWindow time.Duration
	now                func() time.Time
}

type Option func(*TransferService)

func WithPayloadDedupWindow(d time.Duration) Option {
	return func(s *TransferService) { s.payloadDedupWindow = d }
}

func WithClock(now func() time.Time) Option {
	return func(s *TransferService) { s.now = now }
}

func NewTransferService(store Store, idempotency IdempotencyStore, logger *slog.Logger, opts ...Option) *TransferService {
	s := &TransferService{
		store:              store,
		idempotency:        idempotency,
		logger:             logger,
		payloadDedupWindow: DefaultPayloadDedupWindow,
		now:                time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *TransferService) CreateTransfer(ctx context.Context, req domain.TransferRequest) (TransferResult, error) {
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = s.derivedKey(req)
	}

	transferID, replayed, err := s.accept(ctx, req)
	if err != nil {
		return TransferResult{}, err
	}

	transfer, err := s.ProcessTransfer(ctx, transferID)
	if err != nil {
		return TransferResult{}, err
	}

	s.logger.InfoContext(ctx, "transfer completed",
		"transfer_id", transfer.ID,
		"idempotency_key", req.IdempotencyKey,
		"status", transfer.Status,
		"failure_reason", transfer.FailureReason,
		"replayed", replayed,
	)
	return TransferResult{Transfer: transfer, Replayed: replayed}, nil
}

func (s *TransferService) derivedKey(req domain.TransferRequest) string {
	window := s.now().UTC().Truncate(s.payloadDedupWindow).Unix()
	return fmt.Sprintf("auto:%s:%d", req.Hash(), window)
}

func (s *TransferService) accept(ctx context.Context, req domain.TransferRequest) (transferID string, replayed bool, err error) {
	rec := domain.IdempotencyRecord{
		Key:         req.IdempotencyKey,
		RequestHash: req.Hash(),
		TransferID:  uuid.NewString(),
	}

	stored, created, err := s.idempotency.Claim(ctx, rec)
	if err != nil {
		return "", false, fmt.Errorf("%w: %v", domain.ErrIdempotencyUnavailable, err)
	}
	if stored.RequestHash != rec.RequestHash {
		return "", false, domain.ErrIdempotencyKeyReused
	}

	var transfer domain.Transfer
	err = s.store.InTx(ctx, func(tx Tx) error {
		if err := tx.EnsureWalletsExist(ctx, req.FromWalletID, req.ToWalletID); err != nil {
			return err
		}
		transfer, err = tx.CreateTransferIfAbsent(ctx, domain.Transfer{
			ID:           stored.TransferID,
			FromWalletID: req.FromWalletID,
			ToWalletID:   req.ToWalletID,
			Amount:       req.Amount,
			Status:       domain.StatusPending,
		})
		return err
	})

	if created && errors.Is(err, domain.ErrWalletNotFound) {
		if relErr := s.idempotency.Release(ctx, rec); relErr != nil {
			s.logger.WarnContext(ctx, "release idempotency claim", "idempotency_key", rec.Key, "error", relErr)
		}
	}
	if err != nil {
		return "", false, err
	}
	return transfer.ID, !created, nil
}

func (s *TransferService) ProcessTransfer(ctx context.Context, transferID string) (domain.Transfer, error) {
	var result domain.Transfer
	err := s.store.InTx(ctx, func(tx Tx) error {
		t, err := tx.GetTransferForUpdate(ctx, transferID)
		if err != nil {
			return err
		}
		if t.Status.IsTerminal() {
			result = t
			return nil
		}

		wallets, err := tx.LockWallets(ctx, t.FromWalletID, t.ToWalletID)
		if err != nil {
			return err
		}
		from, to := wallets[t.FromWalletID], wallets[t.ToWalletID]

		if err := from.Debit(t.Amount); err != nil {
			if !errors.Is(err, domain.ErrInsufficientFunds) {
				return err
			}
			if err := t.MarkFailed(domain.FailureInsufficientFunds); err != nil {
				return err
			}
			result, err = tx.UpdateTransferStatus(ctx, t, domain.StatusPending)
			return err
		}
		to.Credit(t.Amount)

		if err := tx.UpdateWalletBalance(ctx, from); err != nil {
			return err
		}
		if err := tx.UpdateWalletBalance(ctx, to); err != nil {
			return err
		}
		if err := tx.InsertLedgerEntries(ctx, t.LedgerEntries()); err != nil {
			return err
		}
		if err := t.MarkProcessed(); err != nil {
			return err
		}
		result, err = tx.UpdateTransferStatus(ctx, t, domain.StatusPending)
		return err
	})
	if err != nil {
		return domain.Transfer{}, fmt.Errorf("process transfer %s: %w", transferID, err)
	}
	return result, nil
}

func (s *TransferService) GetTransfer(ctx context.Context, id string) (domain.Transfer, error) {
	return s.store.GetTransfer(ctx, id)
}
