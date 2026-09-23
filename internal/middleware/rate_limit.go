package middleware

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	"backend/internal/config"
	"backend/internal/database"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type RedisRateLimiter struct {
	client      *redis.Client
	maxRequests int
	window      time.Duration
}

func NewRedisRateLimiter(cfg *config.Config, maxRequests int, window time.Duration) (*RedisRateLimiter, error) {
	client, err := database.GetRedisClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to get Redis client: %v", err)
	}

	// Test connection
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %v", err)
	}

	return &RedisRateLimiter{
		client:      client,
		maxRequests: maxRequests,
		window:      window,
	}, nil
}

// allowScript trims the window, then records the request only if it fits
// under the limit. Rejected requests are not recorded, so a client that keeps
// retrying while blocked doesn't push its own lockout further out. Running it
// as one script keeps check-then-add atomic across concurrent requests.
//
// KEYS[1] = bucket key; ARGV = now (ns), window start (ns), max requests,
// window (ms), member. The member carries a random suffix so two requests
// landing on the same timestamp are still recorded as two entries.
var allowScript = redis.NewScript(`
redis.call("ZREMRANGEBYSCORE", KEYS[1], "-inf", ARGV[2])
if redis.call("ZCARD", KEYS[1]) >= tonumber(ARGV[3]) then
	return 0
end
redis.call("ZADD", KEYS[1], ARGV[1], ARGV[5])
redis.call("PEXPIRE", KEYS[1], ARGV[4])
return 1
`)

func (rl *RedisRateLimiter) Allow(ctx context.Context, key string) (bool, error) {
	now := time.Now().UnixNano()
	windowStart := now - rl.window.Nanoseconds()
	member := fmt.Sprintf("%d-%d", now, rand.Uint64())

	allowed, err := allowScript.Run(ctx, rl.client, []string{key},
		now, windowStart, rl.maxRequests, rl.window.Milliseconds(), member).Int()
	if err != nil {
		return false, err
	}

	return allowed == 1, nil
}

func RateLimit() gin.HandlerFunc {
	cfg, err := config.LoadConfig()
	if err != nil {
		panic(fmt.Sprintf("Failed to load config: %v", err))
	}

	limiter, err := NewRedisRateLimiter(cfg, 5, time.Minute)
	if err != nil {
		panic(fmt.Sprintf("Failed to create rate limiter: %v", err))
	}

	return func(c *gin.Context) {
		ip := c.ClientIP()
		key := fmt.Sprintf("rate_limit:%s", ip)

		allowed, err := limiter.Allow(c.Request.Context(), key)
		if err != nil {
			c.JSON(500, gin.H{"error": "Rate limiter error"})
			c.Abort()
			return
		}

		if !allowed {
			c.JSON(429, gin.H{"error": "Too many requests"})
			c.Abort()
			return
		}

		c.Next()
	}
}

// ClickRateLimit uses its own Redis key prefix and a more generous threshold than
// RateLimit, so low-stakes click pings (e.g. job "Apply" clicks) don't share a quota
// bucket with form submissions (general application, newsletter) from the same IP.
func ClickRateLimit() gin.HandlerFunc {
	cfg, err := config.LoadConfig()
	if err != nil {
		panic(fmt.Sprintf("Failed to load config: %v", err))
	}

	limiter, err := NewRedisRateLimiter(cfg, 30, time.Minute)
	if err != nil {
		panic(fmt.Sprintf("Failed to create rate limiter: %v", err))
	}

	return func(c *gin.Context) {
		ip := c.ClientIP()
		key := fmt.Sprintf("click_rate_limit:%s", ip)

		allowed, err := limiter.Allow(c.Request.Context(), key)
		if err != nil {
			c.JSON(500, gin.H{"error": "Rate limiter error"})
			c.Abort()
			return
		}

		if !allowed {
			c.JSON(429, gin.H{"error": "Too many requests"})
			c.Abort()
			return
		}

		c.Next()
	}
}
