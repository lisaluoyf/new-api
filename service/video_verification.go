package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const videoVerificationTimeout = 180 * time.Second
const videoVerificationLease = 300 * time.Second
const videoVerificationLock = "apimaster:video-verification:global-lease"
const videoVerificationChat = "oc_9b0726dbd589af84fcee8544538c95b0"

var videoVerificationStart sync.Once

func StartVideoVerificationWorker() {
	videoVerificationStart.Do(func() {
		if common.IsMasterNode {
			if common.RedisEnabled && common.RDB != nil {
				go superviseVideoVerification(importLegacyVideoVerificationEvents)
			}
			go superviseVideoVerification(consumeVideoVerificationEvents)
			go superviseVideoVerification(deliverVideoVerificationAlerts)
		}
	})
}

func superviseVideoVerification(worker func()) {
	for {
		runVideoVerificationSafely(worker)
		time.Sleep(5 * time.Second)
	}
}

func runVideoVerificationSafely(worker func()) {
	defer func() {
		if recover() != nil {
			common.SysError("video verification worker panic contained; restarting")
		}
	}()
	worker()
}

// Poll only the indexed durable outbox, never the customer task table.
func consumeVideoVerificationEvents() {
	common.SysLog("video verification worker ready: every completed Seedance 2.0/2.5 video, durable outbox")
	for {
		var runs []model.VideoVerificationRun
		now := time.Now().Unix()
		err := model.DB.Where("status IN ? AND lease_until <= ?", []string{"pending", "processing"}, now).Order("id ASC").Limit(10).Find(&runs).Error
		if err != nil {
			common.SysError("video verification outbox read failed")
		}
		for _, run := range runs {
			if !handleVideoVerificationEvent(run.TaskID, run.CompletedAt) {
				break
			}
		}
		time.Sleep(2 * time.Second)
	}
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
			if run.Status != "processing" && run.Status != "pending" {
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
			if err := tx.Model(&run).Updates(map[string]interface{}{"lease_until": lease, "status": "processing"}).Error; err != nil {
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
		if errors.Is(err, gorm.ErrRecordNotFound) {
			model.DB.Model(&model.VideoVerificationRun{}).Where("task_id = ?", taskID).Updates(map[string]any{"status": "skipped", "lease_until": 0})
			return true
		}
		return false
	}
	normalized := model.NormalizeAutomaticVerifiedVideoModel(task.Properties.OriginModelName)
	if task.Status != model.TaskStatusSuccess || normalized == "" || task.ChannelId <= 0 {
		model.DB.Model(&model.VideoVerificationRun{}).Where("task_id = ?", taskID).Updates(map[string]any{"status": "skipped", "lease_until": 0})
		return true
	}
	if common.RedisEnabled && common.RDB != nil {
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
	}
	state, claimed, err := claimVideoVerification(ctx, &task, normalized, completedAt, time.Now().Unix())
	if err != nil {
		common.SysError("video verification state claim failed")
		return false
	}
	if !claimed {
		return true
	}
	status, reason, checks, provenance := verifyDeliveredVideoWithProvenance(ctx, &task, normalized)
	persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer persistCancel()
	if err := finishVideoVerification(persistCtx, state, &task, status, reason, checks, time.Now().Unix(), provenance); err != nil {
		common.SysError("video verification result persistence failed")
		return false
	}
	common.SysLog(fmt.Sprintf("video verification finished: channel=%d model=%s status=%s reason=%s", task.ChannelId, normalized, status, reason))
	return true
}

func finishVideoVerification(ctx context.Context, state *model.VideoVerificationState, task *model.Task, status, reason string, checks []model.VideoFingerprintCheck, now int64, versions ...videoVerificationProvenance) error {
	provenance := videoVerificationProvenance{}
	if len(versions) > 0 {
		provenance = versions[0]
	}
	return model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(state).Where("active_task_id = ? AND lease_until = ?", task.ID, state.LeaseUntil).Updates(map[string]interface{}{"last_task_id": task.ID, "active_task_id": 0, "lease_until": 0, "next_at": 0, "last_status": status, "last_detected_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("video verification lease lost")
		}
		if err := tx.Model(&model.VideoVerificationRun{}).Where("task_id = ? AND status = ?", task.ID, "processing").Updates(map[string]interface{}{"status": status, "lease_until": 0, "fingerprint_model_version": provenance.FingerprintModelVersion, "detector_version": provenance.DetectorVersion, "baseline_sha256": provenance.BaselineSHA256}).Error; err != nil {
			return err
		}
		encoded, err := common.Marshal(checks)
		if err != nil {
			return err
		}
		entry := model.ChannelDetectLog{ChannelId: task.ChannelId, Source: "video", Status: status, ClaimedModel: state.Model, VideoChecksJSON: string(encoded), FingerprintModelVersion: provenance.FingerprintModelVersion, DetectorVersion: provenance.DetectorVersion, BaselineSHA256: provenance.BaselineSHA256, DetectTime: now, Note: reason}
		if err := tx.Create(&entry).Error; err != nil {
			return err
		}
		if status != "pass" && model.NormalizeAutomaticVerifiedVideoModel(state.Model) != "" {
			alert := model.VideoVerificationAlert{TaskID: task.ID, ChannelID: task.ChannelId, Model: state.Model, Reason: reason, VideoChecksJSON: string(encoded), FingerprintModelVersion: provenance.FingerprintModelVersion, DetectorVersion: provenance.DetectorVersion, BaselineSHA256: provenance.BaselineSHA256, DetectedAt: now, NextAt: now, Status: "pending"}
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
		if model.NormalizeAutomaticVerifiedVideoModel(alert.Model) == "" {
			if err := model.DB.WithContext(ctx).Model(&alert).Where("status = ? AND lease_until <= ?", "pending", now).
				Updates(map[string]any{"status": "skipped", "next_at": 0, "lease_until": 0}).Error; err != nil {
				return err
			}
			continue
		}
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
	channelName := fmt.Sprintf("#%d", alert.ChannelID)
	if channel, err := model.GetChannelById(alert.ChannelID, false); err == nil {
		channelName = channel.Name
	}
	email, err := videoVerificationAlertUserEmail(alert.TaskID)
	if err != nil {
		return errors.New("video verification account lookup failed")
	}
	return common.SendFeishuInteractiveCard(videoVerificationChat, videoVerificationAlertCard(alert, channelName, email))
}

func videoVerificationAlertUserEmail(taskID int64) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var email string
	err := model.DB.WithContext(ctx).Model(&model.User{}).Select("email").Where("id = (?)",
		model.DB.Model(&model.Task{}).Select("user_id").Where("id = ?", taskID),
	).Find(&email).Error
	return strings.TrimSpace(email), err
}

func videoVerificationAlertCard(alert *model.VideoVerificationAlert, channelName, email string) map[string]any {
	title, color := "Seedance · 检测未完成", "orange"
	reason := "检测未能得出完整结论，请检查检测记录。"
	switch alert.Reason {
	case "version_mismatch":
		title, color = "Seedance · 指纹不匹配", "red"
		reason = "识别出的编码版本与声明模型不一致。"
	case "known_fingerprint_failed":
		title, color = "Seedance · 指纹不匹配", "red"
		reason = "已知指纹的关键检测项未通过。"
	case "incomplete_evidence":
		reason = "必要检测项尚未全部通过，暂不能确认完整验真结果。"
	case "channel_unavailable":
		reason = "无法读取渠道配置，检测未执行。"
	case "baseline_unavailable_or_expired":
		reason = "检测基线不可用或已过期。"
	case "unexpected_claim":
		reason = "检测器返回的声明模型与请求不一致。"
	case "unknown_fingerprint":
		reason = "未识别出受支持的编码指纹。"
	case "inconclusive_verdict":
		reason = "检测器未能给出明确的匹配结论。"
	case "invalid_evidence":
		reason = "检测证据格式异常。"
	case "proxy_download_not_supported":
		reason = "当前检测下载流程不支持该渠道的代理配置。"
	case "video_download_failed":
		reason = "无法下载视频，检测未完成。"
	case "detector_unavailable":
		reason = "检测服务不可用或响应异常。"
	}
	channelLabel := fmt.Sprintf("#%d", alert.ChannelID)
	if channelName != channelLabel {
		channelLabel = fmt.Sprintf("%s · #%d", channelName, alert.ChannelID)
	}
	detectedAt := time.Unix(alert.DetectedAt, 0).In(time.FixedZone("UTC+8", 8*60*60)).Format("2006-01-02 15:04:05")
	email = strings.TrimSpace(email)
	if email == "" {
		email = "未获取"
	}
	elements := []any{
		map[string]any{"tag": "markdown", "content": fmt.Sprintf("**用户邮箱**　%s\n**渠道**　%s\n**声明模型**　%s", email, channelLabel, alert.Model)},
		map[string]any{"tag": "hr"},
		map[string]any{"tag": "markdown", "content": "**原因**\n" + reason + "\n`" + alert.Reason + "`"},
	}
	if alert.FingerprintModelVersion != "" {
		elements = append(elements, map[string]any{"tag": "markdown", "content": "**指纹版本**　" + alert.FingerprintModelVersion + "\n**检测程序**　" + alert.DetectorVersion})
	}
	if evidence := videoVerificationAlertEvidence(alert.VideoChecksJSON); evidence != "" {
		elements = append(elements, map[string]any{"tag": "markdown", "content": evidence})
	}
	elements = append(elements, map[string]any{"tag": "markdown", "content": "检测时间　" + detectedAt + "（北京时间）"})
	return map[string]any{
		"schema": "2.0",
		"header": map[string]any{
			"template": color,
			"title":    map[string]any{"tag": "plain_text", "content": common.FeishuNotificationTitle(title)},
		},
		"body": map[string]any{
			"elements": elements,
		},
	}
}

func videoVerificationAlertEvidence(raw string) string {
	var checks []model.VideoFingerprintCheck
	if common.Unmarshal([]byte(raw), &checks) != nil || len(checks) == 0 {
		return ""
	}
	byID := make(map[string]model.VideoFingerprintCheck, len(checks))
	for _, check := range checks {
		byID[check.ID] = check
	}
	labels := map[string]string{"claim": "声明版本", "dimensions": "视频尺寸", "family": "容器指纹", "x264": "编码指纹", "frames": "视频帧数"}
	statuses := map[string]string{"pass": "✅ 通过", "fail": "❌ 不通过", "warn": "⚠️ 待确认", "skip": "⏸ 未判定"}
	lines := []string{"**逐项检测**"}
	passed := 0
	for _, checkID := range model.VideoFingerprintCheckIDs {
		check, exists := byID[checkID]
		if !exists {
			lines = append(lines, "⏸ 未判定 · **"+labels[checkID]+"**：未返回检测结果")
			continue
		}
		status := statuses[check.Status]
		if check.Status == "pass" {
			passed++
		}
		if status == "" {
			status = "⏸ 未判定"
		}
		line := fmt.Sprintf("%s · **%s**：%s", status, labels[checkID], check.Value)
		if check.Status != "pass" && check.Expected != "" {
			line += "（基线：" + check.Expected + "）"
		}
		lines = append(lines, line)
	}
	lines[0] = fmt.Sprintf("**逐项检测 · %d/%d 通过**", passed, len(model.VideoFingerprintCheckIDs))
	return strings.Join(lines, "\n")
}
