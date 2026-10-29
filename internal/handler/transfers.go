package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Prashanth840/payment_service/internal/domain"
	"github.com/Prashanth840/payment_service/internal/dto"
)

const headerIdempotentReplayed = "Idempotent-Replayed"

func transferStatusCode(s domain.TransferStatus) int {
	switch s {
	case domain.StatusProcessed:
		return http.StatusCreated
	case domain.StatusFailed:
		return http.StatusUnprocessableEntity
	default:
		return http.StatusAccepted
	}
}

func (h *Handler) CreateTransfer(c *gin.Context) {
	var body dto.CreateTransferRequest
	if err := decodeJSON(c, &body); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	req, err := domain.NewTransferRequest(body.IdempotencyKey, body.FromWalletID, body.ToWalletID, body.Amount)
	if err != nil {
		h.writeDomainError(c, err)
		return
	}

	res, err := h.transfers.CreateTransfer(c.Request.Context(), req)
	if err != nil {
		h.writeDomainError(c, err)
		return
	}
	if res.Replayed {
		c.Header(headerIdempotentReplayed, "true")
	}
	c.JSON(transferStatusCode(res.Transfer.Status), dto.NewTransferResponse(res.Transfer))
}

func (h *Handler) GetTransfer(c *gin.Context) {
	t, err := h.transfers.GetTransfer(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.writeDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, dto.NewTransferResponse(t))
}
