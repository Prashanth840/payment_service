package router

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/Prashanth840/payment_service/internal/handler"
)

func New(h *handler.Handler, logger *slog.Logger) *gin.Engine {
	r := gin.New()
	r.NoRoute(handler.NotFound)

	transfers := r.Group("/transfers")
	transfers.POST("", h.CreateTransfer)
	transfers.GET("/:id", h.GetTransfer)

	wallets := r.Group("/wallets")
	wallets.POST("", h.CreateWallet)
	wallets.GET("/:id", h.GetWallet)
	wallets.GET("/:id/ledger", h.ListLedger)

	r.GET("/health", h.Health)

	return r
}
