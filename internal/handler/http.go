package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Prashanth840/payment_service/internal/domain"
	"github.com/Prashanth840/payment_service/internal/dto"
)

func decodeJSON(c *gin.Context, dst any) error {
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("malformed JSON body: %v", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("body must contain a single JSON object")
	}
	return nil
}

func writeError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, dto.NewErrorResponse(code, message))
}

func NotFound(c *gin.Context) {
	writeError(c, http.StatusNotFound, "NOT_FOUND", "route not found")
}

func (h *Handler) writeDomainError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidRequest):
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
	case errors.Is(err, domain.ErrWalletNotFound):
		writeError(c, http.StatusNotFound, "WALLET_NOT_FOUND", err.Error())
	case errors.Is(err, domain.ErrTransferNotFound):
		writeError(c, http.StatusNotFound, "TRANSFER_NOT_FOUND", err.Error())
	case errors.Is(err, domain.ErrIdempotencyKeyReused):
		writeError(c, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED", err.Error())
	case errors.Is(err, domain.ErrIdempotencyUnavailable):
		h.logger.ErrorContext(c.Request.Context(), "idempotency store unavailable", "error", err)
		writeError(c, http.StatusServiceUnavailable, "IDEMPOTENCY_UNAVAILABLE",
			"transfers are temporarily unavailable; retry with the same idempotencyKey")
	default:
		h.logger.ErrorContext(c.Request.Context(), "request failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL",
			"internal error; safe to retry")
	}
}
