package domain

import "errors"

var (
	ErrInvalidRequest         = errors.New("invalid request")
	ErrWalletNotFound         = errors.New("wallet not found")
	ErrTransferNotFound       = errors.New("transfer not found")
	ErrInsufficientFunds      = errors.New("insufficient funds")
	ErrInvalidTransition      = errors.New("invalid transfer state transition")
	ErrIdempotencyKeyReused   = errors.New("idempotency key reused with a different request")
	ErrIdempotencyUnavailable = errors.New("idempotency store unavailable")
)
