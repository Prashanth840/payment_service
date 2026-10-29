package redisstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/Prashanth840/payment_service/internal/domain"
	"github.com/Prashanth840/payment_service/internal/repository/redisstore"
	"github.com/Prashanth840/payment_service/internal/testsupport"
)

func TestClaim_FirstClaimWinsAndExpires(t *testing.T) {
	rdb, prefix := testsupport.NewRedis(t)
	store := redisstore.New(rdb, prefix, time.Minute)
	ctx := context.Background()

	first := domain.IdempotencyRecord{Key: "k1", RequestHash: "h1", TransferID: "t1"}
	second := domain.IdempotencyRecord{Key: "k1", RequestHash: "h2", TransferID: "t2"}

	stored, created, err := store.Claim(ctx, first)
	if err != nil || !created || stored != first {
		t.Fatalf("first claim: stored=%+v created=%v err=%v", stored, created, err)
	}
	stored, created, err = store.Claim(ctx, second)
	if err != nil || created || stored != first {
		t.Fatalf("second claim must return the original record: stored=%+v created=%v err=%v", stored, created, err)
	}

	ttl := rdb.TTL(ctx, prefix+"idempotency:k1").Val()
	if ttl <= 0 || ttl > time.Minute {
		t.Errorf("TTL = %v, want (0, 1m]", ttl)
	}
}

func TestRelease_OnlyDeletesOwnRecord(t *testing.T) {
	rdb, prefix := testsupport.NewRedis(t)
	store := redisstore.New(rdb, prefix, time.Minute)
	ctx := context.Background()

	owner := domain.IdempotencyRecord{Key: "k1", RequestHash: "h1", TransferID: "t1"}
	other := domain.IdempotencyRecord{Key: "k1", RequestHash: "h1", TransferID: "t2"}
	if _, _, err := store.Claim(ctx, owner); err != nil {
		t.Fatal(err)
	}

	if err := store.Release(ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, created, _ := store.Claim(ctx, other); created {
		t.Fatal("release by a non-owner must not delete the record")
	}

	if err := store.Release(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if _, created, _ := store.Claim(ctx, other); !created {
		t.Fatal("release by the owner must delete the record")
	}
}
