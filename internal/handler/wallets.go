package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Prashanth840/payment_service/internal/dto"
)

func (h *Handler) CreateWallet(c *gin.Context) {
	var body dto.CreateWalletRequest
	if err := decodeJSON(c, &body); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	wallet, err := h.wallets.CreateWallet(c.Request.Context(), body.InitialBalance)
	if err != nil {
		h.writeDomainError(c, err)
		return
	}
	c.JSON(http.StatusCreated, dto.NewWalletResponse(wallet))
}

func (h *Handler) GetWallet(c *gin.Context) {
	id, ok := walletIDParam(c)
	if !ok {
		return
	}
	wallet, err := h.wallets.GetWallet(c.Request.Context(), id)
	if err != nil {
		h.writeDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, dto.NewWalletResponse(wallet))
}

func (h *Handler) ListLedger(c *gin.Context) {
	id, ok := walletIDParam(c)
	if !ok {
		return
	}
	limit := 0
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "limit must be a positive integer")
			return
		}
		limit = n
	}
	entries, err := h.wallets.ListLedgerEntries(c.Request.Context(), id, limit)
	if err != nil {
		h.writeDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, dto.NewLedgerResponse(entries))
}

func walletIDParam(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "wallet id must be a positive integer")
		return 0, false
	}
	return id, true
}
