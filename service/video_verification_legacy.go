package service

import (
	"context"
	"errors"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/go-redis/redis/v8"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strconv"
	"time"
)

// Drain pre-upgrade Redis events into the durable outbox. New completions never
// rely on Redis publishing; this only preserves jobs during a rolling release.
func importLegacyVideoVerificationEvents() {
	const stream = "apimaster:video-verification:v1"
	const group = "verification"
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		common.RDB.XGroupCreateMkStream(ctx, stream, group, "0")
		streams, err := common.RDB.XReadGroup(ctx, &redis.XReadGroupArgs{Group: group, Consumer: "master", Streams: []string{stream, "0"}, Count: 10, Block: -1}).Result()
		if err == nil && (len(streams) == 0 || len(streams[0].Messages) == 0) {
			streams, err = common.RDB.XReadGroup(ctx, &redis.XReadGroupArgs{Group: group, Consumer: "master", Streams: []string{stream, ">"}, Count: 10, Block: 5 * time.Second}).Result()
		}
		if err == nil {
			for _, batch := range streams {
				for _, message := range batch.Messages {
					id, idErr := strconv.ParseInt(fmt.Sprint(message.Values["task_db_id"]), 10, 64)
					at, timeErr := strconv.ParseInt(fmt.Sprint(message.Values["completed_at"]), 10, 64)
					if idErr == nil && timeErr == nil && id > 0 && at > 0 {
						var task model.Task
						err := model.DB.WithContext(ctx).First(&task, id).Error
						if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
							continue
						}
						name := model.NormalizeAutomaticVerifiedVideoModel(task.Properties.OriginModelName)
						if err == nil && task.Status == model.TaskStatusSuccess && name != "" {
							run := model.VideoVerificationRun{TaskID: id, ChannelID: task.ChannelId, Model: name, CompletedAt: at, Status: "pending"}
							if model.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&run).Error != nil {
								continue
							}
						}
					}
					common.RDB.XAck(ctx, stream, group, message.ID)
				}
			}
		}
		cancel()
		if err != nil && !errors.Is(err, redis.Nil) {
			common.SysError("legacy video verification import failed")
			time.Sleep(5 * time.Second)
		}
	}
}
