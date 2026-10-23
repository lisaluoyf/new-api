package service

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const evidenceFirst = "incomplete_evidence_first_seen"
const evidenceSurge = "incomplete_evidence_surge"
const evidenceDigest = "incomplete_evidence_daily_digest"

// The caller holds the route lease. Keep ordinary results for audit, and only
// enqueue first-seen or a doubled hourly volume (at least ten), at most once/6h.
func prepareIncompleteEvidenceAlert(tx *gorm.DB, alert *model.VideoVerificationAlert) error {
	alert.Status, alert.NextAt = "skipped", 0
	route := func() *gorm.DB {
		return tx.Model(&model.VideoVerificationAlert{}).Where("channel_id = ? AND model = ? AND reason IN ?", alert.ChannelID, alert.Model, []string{"incomplete_evidence", evidenceFirst, evidenceSurge})
	}
	var total int64
	if err := route().Count(&total).Error; err != nil {
		return err
	}
	if total == 0 {
		alert.Reason, alert.Status, alert.NextAt = evidenceFirst, "pending", alert.DetectedAt
		return nil
	}
	var recentNotice, current, previous int64
	if err := route().Where("reason IN ? AND detected_at >= ?", []string{evidenceFirst, evidenceSurge}, alert.DetectedAt-6*3600).Count(&recentNotice).Error; err != nil {
		return err
	}
	if recentNotice != 0 {
		return nil
	}
	if err := route().Where("detected_at > ? AND detected_at <= ?", alert.DetectedAt-3600, alert.DetectedAt).Count(&current).Error; err != nil {
		return err
	}
	if err := route().Where("detected_at > ? AND detected_at <= ?", alert.DetectedAt-7200, alert.DetectedAt-3600).Count(&previous).Error; err != nil {
		return err
	}
	if current+1 >= 10 && current+1 >= 2*previous {
		alert.Reason, alert.Status, alert.NextAt = evidenceSurge, "pending", alert.DetectedAt
	}
	return nil
}

func isEvidenceDigest(alert *model.VideoVerificationAlert) bool {
	return alert.Reason == evidenceDigest && alert.TaskID < 0 && alert.ChannelID == 0
}

func evidenceGap(checksJSON string) string {
	var checks []model.VideoFingerprintCheck
	if common.Unmarshal([]byte(checksJSON), &checks) != nil {
		return "证据格式异常"
	}
	byID := make(map[string]model.VideoFingerprintCheck)
	for _, check := range checks {
		byID[check.ID] = check
	}
	var gaps []string
	for _, id := range model.VideoFingerprintCheckIDs {
		check, ok := byID[id]
		if !ok || check.Status != "pass" || strings.TrimSpace(check.Value) == "" {
			gaps = append(gaps, id)
		}
	}
	if len(gaps) == 0 {
		return "证据结构待核查"
	}
	return strings.Join(gaps, ", ")
}

// One durable outbox entry per UTC day. Run after 01:00 UTC (09:00 Beijing),
// including after restarts; failed sends use the normal outbox retry policy.
func enqueueEvidenceDailyDigest(tx *gorm.DB, now time.Time) error {
	now = now.UTC()
	if now.Hour() < 1 {
		return nil
	}
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	taskID := -end.Unix()
	var exists int64
	if err := tx.Model(&model.VideoVerificationAlert{}).Where("task_id = ?", taskID).Count(&exists).Error; err != nil || exists != 0 {
		return err
	}
	type group struct {
		ChannelID  int
		Model, Gap string
	}
	counts := make(map[group]int)
	var batch []model.VideoVerificationAlert
	total := 0
	err := tx.Model(&model.VideoVerificationAlert{}).Where("detected_at >= ? AND detected_at < ? AND reason IN ?", end.Add(-24*time.Hour).Unix(), end.Unix(), []string{"incomplete_evidence", evidenceFirst, evidenceSurge}).FindInBatches(&batch, 500, func(_ *gorm.DB, _ int) error {
		for _, alert := range batch {
			if model.NormalizeAutomaticVerifiedVideoModel(alert.Model) == "" {
				continue
			}
			counts[group{alert.ChannelID, alert.Model, evidenceGap(alert.VideoChecksJSON)}]++
			total++
		}
		return nil
	}).Error
	if err != nil {
		return err
	}
	keys := make([]group, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		if keys[i].ChannelID != keys[j].ChannelID {
			return keys[i].ChannelID < keys[j].ChannelID
		}
		if keys[i].Model != keys[j].Model {
			return keys[i].Model < keys[j].Model
		}
		return keys[i].Gap < keys[j].Gap
	})
	body := fmt.Sprintf("**统计日（UTC）** %s\n共 %d 个视频、%d 类缺口（按渠道、模型和缺失项去重）。\n仍为未完成，不代表已确认模型真实。\n", end.Add(-24*time.Hour).Format("2006-01-02"), total, len(keys))
	for i, key := range keys {
		if i == 40 {
			body += fmt.Sprintf("\n其余 %d 类请查看后台检测记录。", len(keys)-40)
			break
		}
		body += fmt.Sprintf("\n渠道 #%d · %s · 缺失 %s：%d 个", key.ChannelID, key.Model, key.Gap, counts[key])
	}
	alert := model.VideoVerificationAlert{TaskID: taskID, Model: "seedance-evidence-digest", Reason: evidenceDigest, VideoChecksJSON: body, DetectedAt: now.Unix(), NextAt: now.Unix(), Status: "pending"}
	if total == 0 {
		alert.Status, alert.NextAt = "skipped", 0
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&alert).Error
}

func evidenceDailyDigestCard(alert *model.VideoVerificationAlert) map[string]any {
	return map[string]any{
		"schema": "2.0",
		"header": map[string]any{"template": "orange", "title": map[string]any{"tag": "plain_text", "content": common.FeishuNotificationTitle("Seedance · 证据缺口日报")}},
		"body":   map[string]any{"elements": []any{map[string]any{"tag": "markdown", "content": alert.VideoChecksJSON}}},
	}
}
