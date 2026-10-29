package redisstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Prashanth840/payment_service/internal/domain"
	"github.com/Prashanth840/payment_service/internal/service"
)

var releaseScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0`)

type IdempotencyStore struct {
	client *redis.Client
	prefix string
	ttl    time.Duration
}

var _ service.IdempotencyStore = (*IdempotencyStore)(nil)

func New(client *redis.Client, prefix string, ttl time.Duration) *IdempotencyStore {
	return &IdempotencyStore{client: client, prefix: prefix, ttl: ttl}
}

func (s *IdempotencyStore) Claim(ctx context.Context, rec domain.IdempotencyRecord) (domain.IdempotencyRecord, bool, error) {
	value, err := json.Marshal(rec)
	if err != nil {
		return domain.IdempotencyRecord{}, false, err
	}
	key := s.key(rec.Key)

	for attempt := 0; attempt < 2; attempt++ {
		created, err := s.client.SetNX(ctx, key, value, s.ttl).Result()
		if err != nil {
			return domain.IdempotencyRecord{}, false, fmt.Errorf("redis claim: %w", err)
		}
		if created {
			return rec, true, nil
		}

		raw, err := s.client.Get(ctx, key).Bytes()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			return domain.IdempotencyRecord{}, false, fmt.Errorf("redis get: %w", err)
		}
		var stored domain.IdempotencyRecord
		if err := json.Unmarshal(raw, &stored); err != nil {
			return domain.IdempotencyRecord{}, false, fmt.Errorf("decode idempotency record: %w", err)
		}
		return stored, false, nil
	}
	return domain.IdempotencyRecord{}, false, errors.New("redis claim: key kept expiring")
}

func (s *IdempotencyStore) Release(ctx context.Context, rec domain.IdempotencyRecord) error {
	value, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return releaseScript.Run(ctx, s.client, []string{s.key(rec.Key)}, value).Err()
}

func (s *IdempotencyStore) key(idempotencyKey string) string {
	return s.prefix + "idempotency:" + idempotencyKey
}
