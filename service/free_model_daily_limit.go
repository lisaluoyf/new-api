package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
)

const freeModelDailyLimitLua = `local current = tonumber(redis.call('GET', KEYS[1]) or '0')
local limit = tonumber(ARGV[1])
if current >= limit then return {0, current} end
current = redis.call('INCR', KEYS[1])
if current == 1 then redis.call('EXPIREAT', KEYS[1], ARGV[2]) end
return {1, current}`

var freeModelDailyLimitNow = time.Now

var freeModelDailyMemory = struct {
	sync.Mutex
	counts map[string]int
}{counts: make(map[string]int)}

func freeModelDailyRequestKey(channelID int, now time.Time) string {
	return fmt.Sprintf("free_model:daily:%d:%s", channelID, now.UTC().Format("20060102"))
}

func freeModelNextUTCMidnight(now time.Time) time.Time {
	utc := now.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day()+1, 0, 0, 0, 0, time.UTC)
}

func FreeModelDailyRequestAvailable(channelID, limit int) (bool, int, error) {
	if limit <= 0 {
		return true, 0, nil
	}
	now := freeModelDailyLimitNow()
	key := freeModelDailyRequestKey(channelID, now)
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		value, err := common.RDB.Get(ctx, key).Result()
		cancel()
		if errors.Is(err, redis.Nil) {
			return true, 0, nil
		}
		if err != nil {
			return false, 0, err
		}
		used, err := strconv.Atoi(value)
		if err != nil {
			return false, 0, err
		}
		return used < limit, used, nil
	}

	freeModelDailyMemory.Lock()
	used := freeModelDailyMemory.counts[key]
	freeModelDailyMemory.Unlock()
	return used < limit, used, nil
}

func ReserveFreeModelDailyRequest(channelID, limit int) (bool, int, error) {
	if limit <= 0 {
		return true, 0, nil
	}
	now := freeModelDailyLimitNow()
	key := freeModelDailyRequestKey(channelID, now)
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		result, err := common.RDB.Eval(ctx, freeModelDailyLimitLua, []string{key}, limit, freeModelNextUTCMidnight(now).Unix()).Result()
		cancel()
		if err != nil {
			return false, 0, err
		}
		values, ok := result.([]interface{})
		if !ok || len(values) != 2 {
			return false, 0, fmt.Errorf("invalid FreeModel daily limit response")
		}
		allowed, err := strconv.ParseInt(fmt.Sprint(values[0]), 10, 64)
		if err != nil {
			return false, 0, err
		}
		used, err := strconv.ParseInt(fmt.Sprint(values[1]), 10, 64)
		if err != nil {
			return false, 0, err
		}
		return allowed == 1, int(used), nil
	}

	freeModelDailyMemory.Lock()
	defer freeModelDailyMemory.Unlock()
	used := freeModelDailyMemory.counts[key]
	if used >= limit {
		return false, used, nil
	}
	used++
	freeModelDailyMemory.counts[key] = used
	return true, used, nil
}

func resetFreeModelDailyLimitForTest() {
	freeModelDailyMemory.Lock()
	freeModelDailyMemory.counts = make(map[string]int)
	freeModelDailyMemory.Unlock()
	freeModelDailyLimitNow = time.Now
}
