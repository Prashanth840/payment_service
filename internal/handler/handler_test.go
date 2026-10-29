package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Prashanth840/payment_service/internal/domain"
	"github.com/Prashanth840/payment_service/internal/handler"
	"github.com/Prashanth840/payment_service/internal/router"
	"github.com/Prashanth840/payment_service/internal/service"
)

func init() {
	gin.SetMode(gin.TestMode)
}

type fakeTransfers struct {
	gotReq domain.TransferRequest
	calls  int
	result service.TransferResult
	err    error
}

func (f *fakeTransfers) CreateTransfer(_ context.Context, req domain.TransferRequest) (service.TransferResult, error) {
	f.calls++
	f.gotReq = req
	return f.result, f.err
}

func (f *fakeTransfers) GetTransfer(context.Context, string) (domain.Transfer, error) {
	return f.result.Transfer, f.err
}

type fakeWallets struct{}

func (fakeWallets) CreateWallet(context.Context, int64) (domain.Wallet, error) {
	return domain.Wallet{}, nil
}
func (fakeWallets) GetWallet(context.Context, int64) (domain.Wallet, error) {
	return domain.Wallet{}, domain.ErrWalletNotFound
}
func (fakeWallets) ListLedgerEntries(context.Context, int64, int) ([]domain.LedgerEntry, error) {
	return nil, nil
}

func newServer(transfers *fakeTransfers) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := handler.New(transfers, fakeWallets{}, func(context.Context) error { return nil }, logger)
	return router.New(h, logger)
}

func post(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body.Error.Code
}

func TestCreateTransfer_RejectsInvalidRequestsWithoutCallingService(t *testing.T) {
	cases := map[string]string{
		"malformed json":    `{"fromWalletId":`,
		"unknown field":     `{"fromWalletId":1,"toWalletId":2,"amount":1,"currency":"USD"}`,
		"fractional":        `{"fromWalletId":1,"toWalletId":2,"amount":1.5}`,
		"amount as text":    `{"fromWalletId":1,"toWalletId":2,"amount":"100"}`,
		"wallet id as text": `{"fromWalletId":"wallet_1","toWalletId":2,"amount":1}`,
		"zero amount":       `{"fromWalletId":1,"toWalletId":2,"amount":0}`,
		"negative amount":   `{"fromWalletId":1,"toWalletId":2,"amount":-1}`,
		"same wallet":       `{"fromWalletId":1,"toWalletId":1,"amount":1}`,
		"missing from":      `{"toWalletId":2,"amount":1}`,
		"trailing data":     `{"fromWalletId":1,"toWalletId":2,"amount":1}{}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			svc := &fakeTransfers{}
			rec := post(t, newServer(svc), "/transfers", body)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body)
			}
			if code := errorCode(t, rec); code != "INVALID_REQUEST" {
				t.Errorf("code = %s", code)
			}
			if svc.calls != 0 {
				t.Error("service must not be called for invalid requests")
			}
		})
	}
}

func TestCreateTransfer_PassesRequestToService(t *testing.T) {
	svc := &fakeTransfers{result: service.TransferResult{Transfer: domain.Transfer{Status: domain.StatusProcessed}}}
	post(t, newServer(svc), "/transfers",
		`{"idempotencyKey":"abc123","fromWalletId":1,"toWalletId":2,"amount":100}`)

	want := domain.TransferRequest{IdempotencyKey: "abc123", FromWalletID: 1, ToWalletID: 2, Amount: 100}
	if svc.gotReq != want {
		t.Errorf("service got %+v, want %+v", svc.gotReq, want)
	}
}

func TestCreateTransfer_StatusCodeFollowsTransferState(t *testing.T) {
	cases := []struct {
		status   domain.TransferStatus
		replayed bool
		wantCode int
	}{
		{domain.StatusProcessed, false, http.StatusCreated},
		{domain.StatusProcessed, true, http.StatusCreated},
		{domain.StatusFailed, false, http.StatusUnprocessableEntity},
		{domain.StatusFailed, true, http.StatusUnprocessableEntity},
		{domain.StatusPending, false, http.StatusAccepted},
	}
	for _, tc := range cases {
		svc := &fakeTransfers{result: service.TransferResult{
			Transfer: domain.Transfer{ID: "t1", Status: tc.status},
			Replayed: tc.replayed,
		}}
		rec := post(t, newServer(svc), "/transfers", `{"idempotencyKey":"k","fromWalletId":1,"toWalletId":2,"amount":1}`)

		if rec.Code != tc.wantCode {
			t.Errorf("%s replayed=%v: status = %d, want %d", tc.status, tc.replayed, rec.Code, tc.wantCode)
		}
		if got := rec.Header().Get("Idempotent-Replayed") == "true"; got != tc.replayed {
			t.Errorf("%s replayed=%v: Idempotent-Replayed header present = %v", tc.status, tc.replayed, got)
		}
		var body struct{ ID, Status string }
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body.ID != "t1" || body.Status != string(tc.status) {
			t.Errorf("unexpected body %s", rec.Body)
		}
	}
}

func TestCreateTransfer_MapsServiceErrors(t *testing.T) {
	cases := []struct {
		err      error
		wantCode int
		wantErr  string
	}{
		{domain.ErrWalletNotFound, http.StatusNotFound, "WALLET_NOT_FOUND"},
		{domain.ErrIdempotencyKeyReused, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED"},
		{domain.ErrIdempotencyUnavailable, http.StatusServiceUnavailable, "IDEMPOTENCY_UNAVAILABLE"},
		{errors.New("connection reset"), http.StatusInternalServerError, "INTERNAL"},
	}
	for _, tc := range cases {
		svc := &fakeTransfers{err: tc.err}
		rec := post(t, newServer(svc), "/transfers", `{"idempotencyKey":"k","fromWalletId":1,"toWalletId":2,"amount":1}`)
		if rec.Code != tc.wantCode || errorCode(t, rec) != tc.wantErr {
			t.Errorf("%v: got %d %s, want %d %s", tc.err, rec.Code, rec.Body, tc.wantCode, tc.wantErr)
		}
		if strings.Contains(rec.Body.String(), "connection reset") {
			t.Error("internal error details must not leak to clients")
		}
	}
}

func TestUnknownRouteReturnsJSONNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	newServer(&fakeTransfers{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNotFound || errorCode(t, rec) != "NOT_FOUND" {
		t.Errorf("got %d %s, want 404 NOT_FOUND", rec.Code, rec.Body)
	}
}

func TestWalletRoutesRejectNonNumericID(t *testing.T) {
	for _, path := range []string{"/wallets/abc", "/wallets/0", "/wallets/abc/ledger"} {
		rec := httptest.NewRecorder()
		newServer(&fakeTransfers{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "INVALID_REQUEST" {
			t.Errorf("%s: got %d %s, want 400 INVALID_REQUEST", path, rec.Code, rec.Body)
		}
	}
}
