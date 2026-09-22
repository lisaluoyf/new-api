package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/bytedance/gopkg/util/gopool"
)

const (
	dailyStatsInterval = 5 * time.Minute
	dailyStatsLookback = 8 * 24 * time.Hour
)

var dailyStatsOnce sync.Once
var dailyStatsNow = time.Now

func StartDailyStatsTask() {
	dailyStatsOnce.Do(func() {
		if !common.IsMasterNode || model.APIMASTER_PG_DB == nil {
			return
		}
		gopool.Go(func() {
			logger.LogInfo(context.Background(), "daily-stats task started")
			runDailyStatsOnce(true)
			ticker := time.NewTicker(dailyStatsInterval)
			defer ticker.Stop()
			for range ticker.C {
				runDailyStatsOnce(false)
			}
		})
	})
}

func runDailyStatsOnce(fullBackfill bool) {
	ctx := context.Background()
	now := dailyStatsNow().Unix()
	endDay := billingDayStart(now)
	startDay := billingDayStart(dailyStatsNow().Add(-dailyStatsLookback).Unix())
	if fullBackfill {
		oldestDay, err := model.GetDailyStatsOldestRegistrationDay()
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("daily-stats: oldest registration query failed: %v", err))
			return
		}
		startDay = oldestDay
	}
	rows, err := model.BuildDailyStatsSummaries(startDay, endDay, now)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("daily-stats: aggregate failed: %v", err))
		return
	}
	if err := model.UpsertDailyStatsSummaries(rows); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("daily-stats: upsert failed: %v", err))
		return
	}
	logger.LogInfo(ctx, fmt.Sprintf("daily-stats: refreshed %d daily rows since %d", len(rows), startDay))
}
