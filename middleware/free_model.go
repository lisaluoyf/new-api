package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type freeModelMemoryBucket struct {
	StartedAt time.Time
	Count     int
}

var freeModelMemoryLimiter = struct {
	sync.Mutex
	buckets map[string]freeModelMemoryBucket
}{buckets: make(map[string]freeModelMemoryBucket)}

const freeModelRateLimitLua = `local count = redis.call('INCR', KEYS[1])
if count == 1 then redis.call('EXPIRE', KEYS[1], ARGV[1]) end
local ttl = redis.call('TTL', KEYS[1])
return {count, ttl}`

// CheckFreeModelRateLimit consumes one request from the account bucket.
// It is called after authentication and eligibility checks, before the relay
// retry loop, so fallback attempts are not counted.
func CheckFreeModelRateLimit(c *gin.Context) bool {
	userID := c.GetInt("id")
	settings := service.GetFreeModelSettings()
	limit := settings.AccountRequestsPerMinute
	allowed, remaining, reset, err := consumeFreeModelRateLimit(userID, limit)
	if err != nil {
		abortWithOpenAiMessage(c, http.StatusInternalServerError, "free_model_rate_limit_unavailable")
		return false
	}
	c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
	c.Header("X-RateLimit-Remaining", strconv.Itoa(remaining))
	c.Header("X-RateLimit-Reset", strconv.FormatInt(reset, 10))
	if !allowed {
		retryAfter := reset - time.Now().Unix()
		if retryAfter < 1 {
			retryAfter = 1
		}
		c.Header("Retry-After", strconv.FormatInt(retryAfter, 10))
		abortWithOpenAiMessage(c, http.StatusTooManyRequests, "FreeModel rate limit exceeded")
		return false
	}
	return true
}

func consumeFreeModelRateLimit(userID, limit int) (bool, int, int64, error) {
	if userID <= 0 || limit <= 0 {
		return false, 0, time.Now().Add(time.Minute).Unix(), fmt.Errorf("invalid FreeModel rate limit arguments")
	}
	window := service.FreeModelRateLimitWindow()
	if common.RedisEnabled && common.RDB != nil {
		result, err := common.RDB.Eval(context.Background(), freeModelRateLimitLua, []string{service.FreeModelRateLimitKey(userID)}, int(window.Seconds())).Result()
		if err != nil {
			return false, 0, 0, err
		}
		values, ok := result.([]interface{})
		if !ok || len(values) != 2 {
			return false, 0, 0, fmt.Errorf("invalid Redis rate limit response")
		}
		count, _ := strconv.ParseInt(fmt.Sprint(values[0]), 10, 64)
		ttl, _ := strconv.ParseInt(fmt.Sprint(values[1]), 10, 64)
		if ttl < 1 {
			ttl = int64(window.Seconds())
		}
		remaining := limit - int(count)
		if remaining < 0 {
			remaining = 0
		}
		return count <= int64(limit), remaining, time.Now().Unix() + ttl, nil
	}

	now := time.Now()
	freeModelMemoryLimiter.Lock()
	defer freeModelMemoryLimiter.Unlock()
	if len(freeModelMemoryLimiter.buckets) > 4096 {
		for key, existing := range freeModelMemoryLimiter.buckets {
			if now.Sub(existing.StartedAt) >= window {
				delete(freeModelMemoryLimiter.buckets, key)
			}
		}
	}
	bucket := freeModelMemoryLimiter.buckets[service.FreeModelRateLimitKey(userID)]
	if bucket.StartedAt.IsZero() || now.Sub(bucket.StartedAt) >= window {
		bucket = freeModelMemoryBucket{StartedAt: now}
	}
	bucket.Count++
	freeModelMemoryLimiter.buckets[service.FreeModelRateLimitKey(userID)] = bucket
	reset := bucket.StartedAt.Add(window).Unix()
	remaining := limit - bucket.Count
	if remaining < 0 {
		remaining = 0
	}
	return bucket.Count <= limit, remaining, reset, nil
}
