package cache

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/jasonnorch/go-service-starter/internal/config"
)

// Redis is a Cache backed by a Redis server, shared by every instance.
type Redis struct {
	client *redis.Client
}

func openRedis(ctx context.Context, cfg config.Cache) (*Redis, error) {
	opts, err := redis.ParseURL(cfg.RedisURL.Reveal())
	if err != nil {
		// go-redis passes net/url's error through, and *url.Error quotes the
		// whole URL — password included — so wrapping it as-is would write the
		// password into the startup log. Keep the reason, drop the URL.
		// redis_test.go holds every parse failure to that.
		if ue, ok := errors.AsType[*url.Error](err); ok {
			err = ue.Err
		}
		return nil, fmt.Errorf("parsing cache.redis_url: %w", err)
	}
	client := redis.NewClient(opts)

	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("reaching redis within %s: %w", cfg.ConnectTimeout, err)
	}
	return &Redis{client: client}, nil
}

// Get implements Cache.
func (r *Redis) Get(ctx context.Context, key string) (value []byte, ok bool, err error) {
	value, err = r.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("cache get: %w", err)
	}
	return value, true, nil
}

// Set implements Cache. The ttl check is here as well as in Memory because
// go-redis reads 0 as "no expiry" and -1 as "keep the old one", and neither is
// what a caller who passed a non-positive ttl by mistake meant.
func (r *Redis) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if ttl <= 0 {
		return ErrInvalidTTL
	}
	if err := r.client.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("cache set: %w", err)
	}
	return nil
}

// Delete implements Cache.
func (r *Redis) Delete(ctx context.Context, key string) error {
	if err := r.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("cache delete: %w", err)
	}
	return nil
}

// Ping implements Cache.
func (r *Redis) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

// Close implements Cache.
func (r *Redis) Close() error {
	return r.client.Close()
}
