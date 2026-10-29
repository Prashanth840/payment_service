package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const MaxIdempotencyKeyLength = 255

type TransferStatus string

const (
	StatusPending   TransferStatus = "PENDING"
	StatusProcessed TransferStatus = "PROCESSED"
	StatusFailed    TransferStatus = "FAILED"
)

func (s TransferStatus) IsTerminal() bool {
	return s == StatusProcessed || s == StatusFailed
}

const FailureInsufficientFunds = "INSUFFICIENT_FUNDS"

type TransferRequest struct {
	IdempotencyKey string
	FromWalletID   int64
	ToWalletID     int64
	Amount         int64
}

func NewTransferRequest(idempotencyKey string, from, to, amount int64) (TransferRequest, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)

	switch {
	case len(idempotencyKey) > MaxIdempotencyKeyLength:
		return TransferRequest{}, fmt.Errorf("%w: idempotencyKey must be at most %d characters", ErrInvalidRequest, MaxIdempotencyKeyLength)
	case from <= 0:
		return TransferRequest{}, fmt.Errorf("%w: fromWalletId must be a positive integer", ErrInvalidRequest)
	case to <= 0:
		return TransferRequest{}, fmt.Errorf("%w: toWalletId must be a positive integer", ErrInvalidRequest)
	case from == to:
		return TransferRequest{}, fmt.Errorf("%w: fromWalletId and toWalletId must differ", ErrInvalidRequest)
	case amount <= 0:
		return TransferRequest{}, fmt.Errorf("%w: amount must be positive", ErrInvalidRequest)
	}
	return TransferRequest{IdempotencyKey: idempotencyKey, FromWalletID: from, ToWalletID: to, Amount: amount}, nil
}

func (r TransferRequest) Hash() string {
	payload := strconv.FormatInt(r.FromWalletID, 10) + "|" + strconv.FormatInt(r.ToWalletID, 10) + "|" + strconv.FormatInt(r.Amount, 10)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

type Transfer struct {
	ID            string
	FromWalletID  int64
	ToWalletID    int64
	Amount        int64
	Status        TransferStatus
	FailureReason string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (t *Transfer) MarkProcessed() error {
	if t.Status != StatusPending {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, t.Status, StatusProcessed)
	}
	t.Status = StatusProcessed
	return nil
}

func (t *Transfer) MarkFailed(reason string) error {
	if t.Status != StatusPending {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, t.Status, StatusFailed)
	}
	t.Status = StatusFailed
	t.FailureReason = reason
	return nil
}

func (t Transfer) LedgerEntries() []LedgerEntry {
	return []LedgerEntry{
		{TransferID: t.ID, WalletID: t.FromWalletID, Type: EntryDebit, Amount: t.Amount},
		{TransferID: t.ID, WalletID: t.ToWalletID, Type: EntryCredit, Amount: t.Amount},
	}
}
