package domain

import "time"

type EntryType string

const (
	EntryDebit  EntryType = "DEBIT"
	EntryCredit EntryType = "CREDIT"
)

type LedgerEntry struct {
	ID         int64
	TransferID string
	WalletID   int64
	Type       EntryType
	Amount     int64
	CreatedAt  time.Time
}
