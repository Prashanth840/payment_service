package domain

import (
	"errors"
	"testing"
)

func TestWallet_Debit(t *testing.T) {
	w := Wallet{ID: 1, Balance: 100}

	if err := w.Debit(100); err != nil {
		t.Fatalf("debit of full balance should succeed: %v", err)
	}
	if w.Balance != 0 {
		t.Fatalf("balance = %d, want 0", w.Balance)
	}
	if err := w.Debit(1); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("want ErrInsufficientFunds, got %v", err)
	}
	if w.Balance != 0 {
		t.Fatalf("failed debit must not change balance, got %d", w.Balance)
	}
}

func TestWallet_Credit(t *testing.T) {
	w := Wallet{ID: 1, Balance: 10}
	w.Credit(5)
	if w.Balance != 15 {
		t.Fatalf("balance = %d, want 15", w.Balance)
	}
}

func TestNewWallet_Validation(t *testing.T) {
	if _, err := NewWallet(-1); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("negative balance: want ErrInvalidRequest, got %v", err)
	}
	if _, err := NewWallet(0); err != nil {
		t.Errorf("valid wallet: %v", err)
	}
}
