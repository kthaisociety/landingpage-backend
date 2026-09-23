package middleware

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// newTestLimiter talks to a real Redis (REDIS_HOST/REDIS_PORT, default
// localhost:6379) since the limiter's logic lives in a Lua script. Skips if
// none is reachable.
func newTestLimiter(t *testing.T, maxRequests int, window time.Duration) (*RedisRateLimiter, string) {
	t.Helper()

	host, port := os.Getenv("REDIS_HOST"), os.Getenv("REDIS_PORT")
	if host == "" {
		host = "localhost"
	}
	if port == "" {
		port = "6379"
	}
	client := redis.NewClient(&redis.Options{Addr: host + ":" + port, Password: os.Getenv("REDIS_PASSWORD")})
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("skipping: no Redis at %s:%s: %v", host, port, err)
	}

	key := "rate_limit_test:" + t.Name()
	client.Del(ctx, key)
	t.Cleanup(func() {
		client.Del(ctx, key)
		client.Close()
	})

	return &RedisRateLimiter{client: client, maxRequests: maxRequests, window: window}, key
}

func TestAllowRejectsOverLimit(t *testing.T) {
	rl, key := newTestLimiter(t, 3, time.Minute)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		allowed, err := rl.Allow(ctx, key)
		if err != nil {
			t.Fatalf("request %d: Allow() error = %v", i, err)
		}
		if !allowed {
			t.Fatalf("request %d: allowed = false, want true", i)
		}
	}

	allowed, err := rl.Allow(ctx, key)
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if allowed {
		t.Fatal("request 4: allowed = true, want false")
	}
}

func TestAllowDoesNotCountRejectedRequests(t *testing.T) {
	window := 500 * time.Millisecond
	rl, key := newTestLimiter(t, 2, window)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if allowed, err := rl.Allow(ctx, key); err != nil || !allowed {
			t.Fatalf("setup request %d: allowed = %v, err = %v", i+1, allowed, err)
		}
	}

	// Hammer the limiter while blocked. Before the fix each of these was
	// recorded, so the lockout kept sliding forward.
	for i := 0; i < 10; i++ {
		allowed, err := rl.Allow(ctx, key)
		if err != nil {
			t.Fatalf("blocked request %d: Allow() error = %v", i+1, err)
		}
		if allowed {
			t.Fatalf("blocked request %d: allowed = true, want false", i+1)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if n := rl.client.ZCard(ctx, key).Val(); n != 2 {
		t.Fatalf("recorded requests = %d, want 2 (rejected ones must not be recorded)", n)
	}

	// Once the original two requests age out, the client is let back in.
	time.Sleep(window)
	allowed, err := rl.Allow(ctx, key)
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if !allowed {
		t.Fatal("after window: allowed = false, want true")
	}
}

// A burst of concurrent requests must let exactly maxRequests through, i.e.
// the check-then-add is atomic.
func TestAllowConcurrentBurstHonorsLimit(t *testing.T) {
	const limit, burst = 5, 50
	rl, key := newTestLimiter(t, limit, time.Minute)
	ctx := context.Background()

	var allowedCount atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, burst)
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed, err := rl.Allow(ctx, key)
			if err != nil {
				errs <- err
				return
			}
			if allowed {
				allowedCount.Add(1)
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("Allow() error = %v", err)
	}
	if got := allowedCount.Load(); got != limit {
		t.Fatalf("allowed = %d, want %d", got, limit)
	}
	if n := rl.client.ZCard(ctx, key).Val(); n != limit {
		t.Fatalf("recorded requests = %d, want %d", n, limit)
	}
}
