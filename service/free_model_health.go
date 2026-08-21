package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	freeModelHealthTTL       = 24 * time.Hour
	freeModelCooldown        = 15 * time.Second
	freeModelCircuitDuration = 60 * time.Second
	freeModelCircuitFailures = 3
)

type FreeModelHealth struct {
	ChannelID          int     `json:"channel_id"`
	ConsecutiveFailure int     `json:"consecutive_failures"`
	CooldownUntil      int64   `json:"cooldown_until"`
	CircuitOpenUntil   int64   `json:"circuit_open_until"`
	Successes          int64   `json:"successes"`
	Failures           int64   `json:"failures"`
	EWLatencyMS        float64 `json:"latency_ms"`
	UpdatedAt          int64   `json:"updated_at"`
}

func (h FreeModelHealth) AvoidUntil() int64 {
	if h.CircuitOpenUntil > h.CooldownUntil {
		return h.CircuitOpenUntil
	}
	return h.CooldownUntil
}

func (h FreeModelHealth) IsAvoided(now time.Time) bool { return h.AvoidUntil() > now.UnixMilli() }

func (h FreeModelHealth) SuccessRate() float64 {
	total := h.Successes + h.Failures
	if total == 0 {
		return 0
	}
	return float64(h.Successes) / float64(total)
}

var freeModelHealthStore = struct {
	sync.RWMutex
	values map[int]FreeModelHealth
}{values: make(map[int]FreeModelHealth)}

var freeModelHealthNow = time.Now

func freeModelHealthKey(channelID int) string { return fmt.Sprintf("free_model:health:%d", channelID) }

func GetFreeModelHealth(channelID int) FreeModelHealth {
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		raw, err := common.RDB.Get(ctx, freeModelHealthKey(channelID)).Bytes()
		cancel()
		if err == nil {
			var health FreeModelHealth
			if common.Unmarshal(raw, &health) == nil {
				return health
			}
		}
	}
	freeModelHealthStore.RLock()
	health := freeModelHealthStore.values[channelID]
	freeModelHealthStore.RUnlock()
	health.ChannelID = channelID
	return health
}

func saveFreeModelHealth(health FreeModelHealth) {
	freeModelHealthStore.Lock()
	freeModelHealthStore.values[health.ChannelID] = health
	freeModelHealthStore.Unlock()
	if common.RedisEnabled && common.RDB != nil {
		if raw, err := common.Marshal(health); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			_ = common.RDB.Set(ctx, freeModelHealthKey(health.ChannelID), raw, freeModelHealthTTL).Err()
			cancel()
		}
	}
}

func RecordFreeModelFailure(channelID, statusCode int, transient bool) FreeModelHealth {
	now := freeModelHealthNow()
	health := GetFreeModelHealth(channelID)
	health.ChannelID = channelID
	health.Failures++
	health.UpdatedAt = now.UnixMilli()
	if statusCode == 429 {
		health.CooldownUntil = now.Add(freeModelCooldown).UnixMilli()
	} else if transient {
		health.ConsecutiveFailure++
		if health.ConsecutiveFailure >= freeModelCircuitFailures {
			health.CircuitOpenUntil = now.Add(freeModelCircuitDuration).UnixMilli()
		}
	}
	saveFreeModelHealth(health)
	return health
}

func RecordFreeModelSuccess(channelID int, duration time.Duration) FreeModelHealth {
	now := freeModelHealthNow()
	health := GetFreeModelHealth(channelID)
	health.ChannelID = channelID
	health.Successes++
	if health.ConsecutiveFailure > 0 {
		health.ConsecutiveFailure--
	}
	if health.ConsecutiveFailure == 0 {
		health.CooldownUntil = 0
		health.CircuitOpenUntil = 0
	}
	latency := float64(duration.Milliseconds())
	if health.EWLatencyMS == 0 {
		health.EWLatencyMS = latency
	} else {
		health.EWLatencyMS = health.EWLatencyMS*0.8 + latency*0.2
	}
	health.UpdatedAt = now.UnixMilli()
	saveFreeModelHealth(health)
	return health
}

func resetFreeModelHealthForTest() {
	freeModelHealthStore.Lock()
	freeModelHealthStore.values = make(map[int]FreeModelHealth)
	freeModelHealthStore.Unlock()
}
