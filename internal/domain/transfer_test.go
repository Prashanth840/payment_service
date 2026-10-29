package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestNewTransferRequest_Validation(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		from    int64
		to      int64
		amount  int64
		wantErr bool
	}{
		{"valid", "k1", 1, 2, 100, false},
		{"valid without key", "", 1, 2, 1, false},
		{"zero amount", "k1", 1, 2, 0, true},
		{"negative amount", "k1", 1, 2, -5, true},
		{"missing source", "k1", 0, 2, 100, true},
		{"missing destination", "k1", 1, 0, 100, true},
		{"negative wallet id", "k1", -1, 2, 100, true},
		{"same wallet", "k1", 1, 1, 100, true},
		{"key too long", strings.Repeat("k", MaxIdempotencyKeyLength+1), 1, 2, 100, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewTransferRequest(tc.key, tc.from, tc.to, tc.amount)
			if tc.wantErr {
				if !errors.Is(err, ErrInvalidRequest) {
					t.Fatalf("want ErrInvalidRequest, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestTransferRequest_HashIsStableAndPayloadSensitive(t *testing.T) {
	a, _ := NewTransferRequest("k", 1, 2, 100)
	b, _ := NewTransferRequest("other-key", 1, 2, 100)
	c, _ := NewTransferRequest("k", 1, 2, 101)
	d, _ := NewTransferRequest("k", 2, 1, 100)

	if a.Hash() != b.Hash() {
		t.Error("hash must depend only on the payload, not the key")
	}
	if a.Hash() == c.Hash() || a.Hash() == d.Hash() {
		t.Error("hash must change when the payload changes")
	}
}

func TestTransfer_StateTransitions(t *testing.T) {
	t.Run("pending to processed", func(t *testing.T) {
		tr := Transfer{Status: StatusPending}
		if err := tr.MarkProcessed(); err != nil {
			t.Fatal(err)
		}
		if tr.Status != StatusProcessed {
			t.Fatalf("status = %s", tr.Status)
		}
	})

	t.Run("pending to failed records reason", func(t *testing.T) {
		tr := Transfer{Status: StatusPending}
		if err := tr.MarkFailed(FailureInsufficientFunds); err != nil {
			t.Fatal(err)
		}
		if tr.Status != StatusFailed || tr.FailureReason != FailureInsufficientFunds {
			t.Fatalf("got %s / %s", tr.Status, tr.FailureReason)
		}
	})

	for _, terminal := range []TransferStatus{StatusProcessed, StatusFailed} {
		t.Run("terminal "+string(terminal)+" cannot transition", func(t *testing.T) {
			tr := Transfer{Status: terminal}
			if err := tr.MarkProcessed(); !errors.Is(err, ErrInvalidTransition) {
				t.Errorf("MarkProcessed: want ErrInvalidTransition, got %v", err)
			}
			if err := tr.MarkFailed(FailureInsufficientFunds); !errors.Is(err, ErrInvalidTransition) {
				t.Errorf("MarkFailed: want ErrInvalidTransition, got %v", err)
			}
			if tr.Status != terminal {
				t.Errorf("status changed to %s", tr.Status)
			}
		})
	}
}

func TestTransfer_LedgerEntriesBalance(t *testing.T) {
	tr := Transfer{ID: "t1", FromWalletID: 1, ToWalletID: 2, Amount: 250}
	entries := tr.LedgerEntries()

	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	debit, credit := entries[0], entries[1]
	if debit.Type != EntryDebit || debit.WalletID != 1 || debit.Amount != 250 || debit.TransferID != "t1" {
		t.Errorf("bad debit entry: %+v", debit)
	}
	if credit.Type != EntryCredit || credit.WalletID != 2 || credit.Amount != 250 || credit.TransferID != "t1" {
		t.Errorf("bad credit entry: %+v", credit)
	}
}
