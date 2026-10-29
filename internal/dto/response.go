package dto

import (
	"time"

	"github.com/Prashanth840/payment_service/internal/domain"
)

type TransferResponse struct {
	ID            string    `json:"id"`
	FromWalletID  int64     `json:"fromWalletId"`
	ToWalletID    int64     `json:"toWalletId"`
	Amount        int64     `json:"amount"`
	Status        string    `json:"status"`
	FailureReason string    `json:"failureReason,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

func NewTransferResponse(t domain.Transfer) TransferResponse {
	return TransferResponse{
		ID:            t.ID,
		FromWalletID:  t.FromWalletID,
		ToWalletID:    t.ToWalletID,
		Amount:        t.Amount,
		Status:        string(t.Status),
		FailureReason: t.FailureReason,
		CreatedAt:     t.CreatedAt.UTC(),
		UpdatedAt:     t.UpdatedAt.UTC(),
	}
}

type WalletResponse struct {
	ID        int64     `json:"id"`
	Balance   int64     `json:"balance"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func NewWalletResponse(w domain.Wallet) WalletResponse {
	return WalletResponse{ID: w.ID, Balance: w.Balance, CreatedAt: w.CreatedAt.UTC(), UpdatedAt: w.UpdatedAt.UTC()}
}

type LedgerEntryResponse struct {
	ID         int64     `json:"id"`
	TransferID string    `json:"transferId"`
	WalletID   int64     `json:"walletId"`
	Type       string    `json:"type"`
	Amount     int64     `json:"amount"`
	CreatedAt  time.Time `json:"createdAt"`
}

type LedgerResponse struct {
	Entries []LedgerEntryResponse `json:"entries"`
}

func NewLedgerResponse(entries []domain.LedgerEntry) LedgerResponse {
	out := make([]LedgerEntryResponse, 0, len(entries))
	for _, e := range entries {
		out = append(out, LedgerEntryResponse{
			ID: e.ID, TransferID: e.TransferID, WalletID: e.WalletID,
			Type: string(e.Type), Amount: e.Amount, CreatedAt: e.CreatedAt.UTC(),
		})
	}
	return LedgerResponse{Entries: out}
}

type HealthResponse struct {
	Status string `json:"status"`
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NewErrorResponse(code, message string) ErrorResponse {
	return ErrorResponse{Error: ErrorBody{Code: code, Message: message}}
}
