package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/Prashanth840/payment_service/internal/handler"
	"github.com/Prashanth840/payment_service/internal/repository/postgres"
	"github.com/Prashanth840/payment_service/internal/repository/redisstore"
	"github.com/Prashanth840/payment_service/internal/router"
	"github.com/Prashanth840/payment_service/internal/service"
)

const (
	databaseURL        = "postgres://postgres:postgres@localhost:5432/payment_service?sslmode=disable"
	redisURL           = "redis://localhost:6379/0"
	httpAddr           = ":8080"
	idempotencyTTL     = 24 * time.Hour
	payloadDedupWindow = time.Minute
)

func main() {
	gin.SetMode(gin.ReleaseMode)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	redisOpts, err := redis.ParseURL(redisURL)
	if err != nil {
		return fmt.Errorf("parse redis url: %w", err)
	}
	rdb := redis.NewClient(redisOpts)
	defer rdb.Close()

	store := postgres.NewStore(pool)
	idempotency := redisstore.New(rdb, "", idempotencyTTL)
	h := handler.New(
		service.NewTransferService(store, idempotency, logger, service.WithPayloadDedupWindow(payloadDedupWindow)),
		service.NewWalletService(store),
		func(ctx context.Context) error {
			if err := pool.Ping(ctx); err != nil {
				return fmt.Errorf("postgres: %w", err)
			}
			if err := rdb.Ping(ctx).Err(); err != nil {
				return fmt.Errorf("redis: %w", err)
			}
			return nil
		},
		logger,
	)
	routes := router.New(h, logger)

	srv := &http.Server{
		Addr:              httpAddr,
		Handler:           routes,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", httpAddr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
