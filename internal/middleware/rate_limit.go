package middleware

import (
	"context"
	"fmt"
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

func (rl *RedisRateLimiter) Allow(ctx context.Context, key string) (bool, error) {
	pipe := rl.client.Pipeline()

	now := time.Now().UnixNano()
	windowStart := now - rl.window.Nanoseconds()

	// Remove old requests
	pipe.ZRemRangeByScore(ctx, key, "0", fmt.Sprint(windowStart))

	// Add current request
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(now), Member: now})

	// Count requests in window
	pipe.ZCard(ctx, key)

	// Set key expiration
	pipe.Expire(ctx, key, rl.window)

	results, err := pipe.Exec(ctx)
	if err != nil {
		return false, err
	}

	// Get count from third command (ZCard)
	count := results[2].(*redis.IntCmd).Val()

	return count <= int64(rl.maxRequests), nil
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
