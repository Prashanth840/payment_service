package domain

import (
	"fmt"
	"time"
)

type Wallet struct {
	ID        int64
	Balance   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

func NewWallet(initialBalance int64) (Wallet, error) {
	if initialBalance < 0 {
		return Wallet{}, fmt.Errorf("%w: initial balance must not be negative", ErrInvalidRequest)
	}
	return Wallet{Balance: initialBalance}, nil
}

func (w *Wallet) Debit(amount int64) error {
	if w.Balance < amount {
		return ErrInsufficientFunds
	}
	w.Balance -= amount
	return nil
}

func (w *Wallet) Credit(amount int64) {
	w.Balance += amount
}
