package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/go-redis/redis/v8"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const VideoVerificationInterval = 10 * time.Minute
const videoVerificationTimeout = 180 * time.Second
const videoVerificationLease = 300 * time.Second
const videoVerificationStream = "apimaster:video-verification:v1"
const videoVerificationGroup = "verification"
const videoVerificationConsumer = "master"
const videoVerificationLock = "apimaster:video-verification:global-lease"
const videoVerificationChat = "oc_9b0726dbd589af84fcee8544538c95b0"

var videoVerificationStart sync.Once

func StartVideoVerificationWorker() {
	videoVerificationStart.Do(func() {
		if !common.RedisEnabled || common.RDB == nil {
			common.SysError("video verification disabled: Redis unavailable")
			return
		}
		queue := make(chan model.VideoVerificationEvent, 256)
		model.SetVideoVerificationQueue(queue)
		go publishVideoVerificationEvents(queue)
		if common.IsMasterNode {
			go consumeVideoVerificationEvents()
			go deliverVideoVerificationAlerts()
		}
	})
}

func publishVideoVerificationEvents(queue <-chan model.VideoVerificationEvent) {
	for event := range queue {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := common.RDB.XAdd(ctx, &redis.XAddArgs{Stream: videoVerificationStream, MaxLenApprox: 10000, Values: map[string]interface{}{"task_db_id": event.TaskID, "completed_at": event.CompletedAt}}).Err()
		cancel()
		if err != nil {
			common.SysError("video verification event dropped: Redis publish failed")
		}
	}
}

func consumeVideoVerificationEvents() {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := common.RDB.XGroupCreateMkStream(ctx, videoVerificationStream, videoVerificationGroup, "0").Err()
		cancel()
		if err == nil || strings.Contains(err.Error(), "BUSYGROUP") {
			break
		}
		common.SysError("video verification stream initialization failed")
		time.Sleep(5 * time.Second)
	}
	common.SysLog("video verification worker ready: event-driven, interval=10m, concurrency=1")
	for {
		messages, err := readVideoVerificationEvents("0", -1)
		if err == nil && len(messages) == 0 {
			messages, err = readVideoVerificationEvents(">", 30*time.Second)
		}
		if err != nil && !errors.Is(err, redis.Nil) {
			common.SysError("video verification stream read failed")
			time.Sleep(5 * time.Second)
			continue
		}
		for _, message := range messages {
			taskID, parseErr := strconv.ParseInt(fmt.Sprint(message.Values["task_db_id"]), 10, 64)
			completedAt, timeErr := strconv.ParseInt(fmt.Sprint(message.Values["completed_at"]), 10, 64)
			if parseErr != nil || timeErr != nil || taskID <= 0 || completedAt <= 0 || handleVideoVerificationEvent(taskID, completedAt) {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				ackErr := common.RDB.XAck(ctx, videoVerificationStream, videoVerificationGroup, message.ID).Err()
				cancel()
				if ackErr != nil {
					common.SysError("video verification stream acknowledgement failed")
				}
			} else {
				time.Sleep(5 * time.Second)
			}
		}
	}
}

func readVideoVerificationEvents(offset string, block time.Duration) ([]redis.XMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	streams, err := common.RDB.XReadGroup(ctx, &redis.XReadGroupArgs{Group: videoVerificationGroup, Consumer: videoVerificationConsumer, Streams: []string{videoVerificationStream, offset}, Count: 1, Block: block}).Result()
	if err != nil {
		return nil, err
	}
	if len(streams) == 0 {
		return nil, nil
	}
	return streams[0].Messages, nil
}

func claimVideoVerification(ctx context.Context, task *model.Task, normalized string, completedAt, now int64) (*model.VideoVerificationState, bool, error) {
	state := model.VideoVerificationState{ChannelID: task.ChannelId, Model: normalized}
	claimed := false
	err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run model.VideoVerificationRun
		if err := tx.Where("task_id = ?", task.ID).Limit(1).Find(&run).Error; err != nil {
			return err
		}
		if run.ID != 0 {
			if run.Status != "processing" {
				return nil
			}
			if run.LeaseUntil > now {
				return errors.New("video verification run busy")
			}
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error; err != nil {
			return err
		}
		if err := tx.Where("channel_id = ? AND model = ?", task.ChannelId, normalized).First(&state).Error; err != nil {
			return err
		}
		if state.NextAt > completedAt || state.NextAt > now || state.LastTaskID == task.ID {
			if run.ID != 0 {
				return tx.Model(&run).Updates(map[string]interface{}{"status": "skipped", "lease_until": 0}).Error
			}
			return nil
		}
		lease := now + int64(videoVerificationLease/time.Second)
		result := tx.Model(&state).Where("lease_until <= ?", now).Updates(map[string]interface{}{"active_task_id": task.ID, "lease_until": lease})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("video verification route busy")
		}
		if run.ID == 0 {
			run = model.VideoVerificationRun{TaskID: task.ID, ChannelID: task.ChannelId, Model: normalized, CompletedAt: completedAt, Status: "processing", LeaseUntil: lease}
			if err := tx.Create(&run).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Model(&run).Updates(map[string]interface{}{"lease_until": lease}).Error; err != nil {
				return err
			}
		}
		state.ActiveTaskID = task.ID
		state.LeaseUntil = lease
		claimed = true
		return nil
	})
	return &state, claimed && err == nil, err
}

func handleVideoVerificationEvent(taskID, completedAt int64) bool {
	ctx, cancel := context.WithTimeout(context.Background(), videoVerificationTimeout)
	defer cancel()
	var task model.Task
	if err := model.DB.WithContext(ctx).First(&task, taskID).Error; err != nil {
		return errors.Is(err, gorm.ErrRecordNotFound)
	}
	normalized := model.NormalizeVerifiedVideoModel(task.Properties.OriginModelName)
	if task.Status != model.TaskStatusSuccess || normalized == "" || task.ChannelId <= 0 {
		return true
	}
	owner := model.GenerateTaskID()
	locked, err := common.RDB.SetNX(ctx, videoVerificationLock, owner, videoVerificationLease).Result()
	if err != nil || !locked {
		return false
	}
	defer func() {
		releaseCtx, release := context.WithTimeout(context.Background(), 2*time.Second)
		defer release()
		if common.RDB.Eval(releaseCtx, `if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end return 0`, []string{videoVerificationLock}, owner).Err() != nil {
			common.SysError("video verification lease release failed")
		}
	}()
	state, claimed, err := claimVideoVerification(ctx, &task, normalized, completedAt, time.Now().Unix())
	if err != nil {
		common.SysError("video verification state claim failed")
		return false
	}
	if !claimed {
		return true
	}
	status, reason, checks := verifyDeliveredVideo(ctx, &task, normalized)
	persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer persistCancel()
	if err := finishVideoVerification(persistCtx, state, &task, status, reason, checks, time.Now().Unix()); err != nil {
		common.SysError("video verification result persistence failed")
		return false
	}
	common.SysLog(fmt.Sprintf("video verification finished: channel=%d model=%s status=%s reason=%s", task.ChannelId, normalized, status, reason))
	return true
}

func finishVideoVerification(ctx context.Context, state *model.VideoVerificationState, task *model.Task, status, reason string, checks []model.VideoFingerprintCheck, now int64) error {
	return model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(state).Where("active_task_id = ? AND lease_until = ?", task.ID, state.LeaseUntil).Updates(map[string]interface{}{"last_task_id": task.ID, "active_task_id": 0, "lease_until": 0, "next_at": now + int64(VideoVerificationInterval/time.Second), "last_status": status, "last_detected_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("video verification lease lost")
		}
		if err := tx.Model(&model.VideoVerificationRun{}).Where("task_id = ? AND status = ?", task.ID, "processing").Updates(map[string]interface{}{"status": status, "lease_until": 0}).Error; err != nil {
			return err
		}
		encoded, err := common.Marshal(checks)
		if err != nil {
			return err
		}
		entry := model.ChannelDetectLog{ChannelId: task.ChannelId, Source: "video", Status: status, ClaimedModel: state.Model, VideoChecksJSON: string(encoded), DetectTime: now, Note: reason}
		if err := tx.Create(&entry).Error; err != nil {
			return err
		}
		if status == "suspicious" {
			alert := model.VideoVerificationAlert{TaskID: task.ID, ChannelID: task.ChannelId, Model: state.Model, Reason: reason, DetectedAt: now, NextAt: now, Status: "pending"}
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&alert).Error
		}
		return nil
	})
}

func deliverVideoVerificationAlerts() {
	for {
		if err := deliverDueVideoVerificationAlerts(); err != nil {
			common.SysError("video verification alert delivery failed")
		}
		time.Sleep(time.Minute)
	}
}

func deliverDueVideoVerificationAlerts() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := time.Now().Unix()
	var alerts []model.VideoVerificationAlert
	if err := model.DB.WithContext(ctx).Where("status = ? AND next_at <= ? AND lease_until <= ?", "pending", now, now).Order("id ASC").Limit(10).Find(&alerts).Error; err != nil {
		return err
	}
	for _, alert := range alerts {
		lease := time.Now().Add(5 * time.Minute).Unix()
		result := model.DB.Model(&alert).Where("status = ? AND lease_until <= ?", "pending", now).Update("lease_until", lease)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			continue
		}
		err := sendVideoVerificationAlert(&alert)
		attempts := alert.Attempts + 1
		status := "sent"
		nextAt := int64(0)
		if err != nil {
			delays := []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}
			status = "dead"
			if attempts <= len(delays) {
				status = "pending"
				nextAt = time.Now().Add(delays[attempts-1]).Unix()
			}
			common.SysError("video verification Feishu alert failed; retained for retry/audit")
		}
		if err := model.DB.Model(&alert).Where("lease_until = ?", lease).Updates(map[string]interface{}{"attempts": attempts, "status": status, "next_at": nextAt, "lease_until": 0}).Error; err != nil {
			return err
		}
	}
	return nil
}

var sendVideoVerificationAlert = func(alert *model.VideoVerificationAlert) error {
	if common.FeishuAppID() == "" || common.FeishuAppSecret() == "" {
		return errors.New("Feishu credentials unavailable")
	}
	return common.SendFeishuCard(videoVerificationChat, common.FeishuNotificationTitle("Seedance 视频指纹验证失败"), []string{
		fmt.Sprintf("渠道：%d", alert.ChannelID), "声明模型：" + alert.Model, "结果：明确指纹/版本不匹配", "原因：" + alert.Reason,
		"检测时间：" + time.Unix(alert.DetectedAt, 0).UTC().Format(time.RFC3339), "仅内部监测，不影响用户视频交付、计费或渠道路由。",
	})
}
