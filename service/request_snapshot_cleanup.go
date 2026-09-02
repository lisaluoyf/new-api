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
	defaultSnapshotRetentionDays = 7
	snapshotCleanupInterval      = time.Minute
	snapshotCleanupBatchSize     = 100
)

var snapshotCleanupOnce sync.Once
var snapshotCleanupNow = time.Now

func StartFailedRequestSnapshotCleanupTask() {
	snapshotCleanupOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			logger.LogInfo(context.Background(), fmt.Sprintf("failed-request-snapshot cleanup started: interval=%s batch=%d", snapshotCleanupInterval, snapshotCleanupBatchSize))
			ticker := time.NewTicker(snapshotCleanupInterval)
			defer ticker.Stop()
			runFailedRequestSnapshotCleanupOnce()
			for range ticker.C {
				runFailedRequestSnapshotCleanupOnce()
			}
		})
	})
}

func runFailedRequestSnapshotCleanupOnce() {
	retentionDays := common.GetEnvOrDefault("FAILED_REQUEST_SNAPSHOT_RETENTION_DAYS", defaultSnapshotRetentionDays)
	if retentionDays <= 0 {
		return
	}
	cutoff := snapshotCleanupNow().Add(-time.Duration(retentionDays) * 24 * time.Hour).Unix()
	deleted, err := model.DeleteFailedRequestSnapshotsBefore(cutoff, snapshotCleanupBatchSize)
	if err != nil {
		logger.LogWarn(context.Background(), fmt.Sprintf("failed-request-snapshot cleanup failed: %v", err))
		return
	}
	if common.DebugEnabled && deleted > 0 {
		logger.LogDebug(context.Background(), "failed-request-snapshot cleanup: deleted=%d cutoff=%d", deleted, cutoff)
	}
}
