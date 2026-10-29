package dto

type CreateTransferRequest struct {
	IdempotencyKey string `json:"idempotencyKey"`
	FromWalletID   int64  `json:"fromWalletId"`
	ToWalletID     int64  `json:"toWalletId"`
	Amount         int64  `json:"amount"`
}

type CreateWalletRequest struct {
	InitialBalance int64 `json:"initialBalance"`
}
