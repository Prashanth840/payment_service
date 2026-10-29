package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Prashanth840/payment_service/internal/domain"
	"github.com/Prashanth840/payment_service/internal/dto"
	"github.com/Prashanth840/payment_service/internal/service"
)

type TransferService interface {
	CreateTransfer(ctx context.Context, req domain.TransferRequest) (service.TransferResult, error)
	GetTransfer(ctx context.Context, id string) (domain.Transfer, error)
}

type WalletService interface {
	CreateWallet(ctx context.Context, initialBalance int64) (domain.Wallet, error)
	GetWallet(ctx context.Context, id int64) (domain.Wallet, error)
	ListLedgerEntries(ctx context.Context, walletID int64, limit int) ([]domain.LedgerEntry, error)
}

type HealthCheck func(ctx context.Context) error

type Handler struct {
	transfers TransferService
	wallets   WalletService
	health    HealthCheck
	logger    *slog.Logger
}

func New(transfers TransferService, wallets WalletService, health HealthCheck, logger *slog.Logger) *Handler {
	return &Handler{transfers: transfers, wallets: wallets, health: health, logger: logger}
}

func (h *Handler) Health(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	if err := h.health(ctx); err != nil {
		h.logger.ErrorContext(ctx, "health check failed", "error", err)
		writeError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "dependency check failed")
		return
	}
	c.JSON(http.StatusOK, dto.HealthResponse{Status: "ok"})
}
