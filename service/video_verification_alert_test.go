package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestVideoVerificationAlertCard(t *testing.T) {
	previous := common.NodeName
	common.NodeName = "apimaster-new-api-green"
	t.Cleanup(func() { common.NodeName = previous })
	detectedAt := time.Date(2026, 10, 9, 7, 10, 8, 0, time.UTC).Unix()
	cases := []struct {
		reason string
		title  string
		color  string
	}{
		{"incomplete_evidence", "检测未完成", "orange"},
		{"channel_unavailable", "检测未完成", "orange"},
		{"baseline_unavailable_or_expired", "检测未完成", "orange"},
		{"unexpected_claim", "检测未完成", "orange"},
		{"unknown_fingerprint", "检测未完成", "orange"},
		{"inconclusive_verdict", "检测未完成", "orange"},
		{"invalid_evidence", "检测未完成", "orange"},
		{"proxy_download_not_supported", "检测未完成", "orange"},
		{"video_download_failed", "检测未完成", "orange"},
		{"detector_unavailable", "检测未完成", "orange"},
		{"version_mismatch", "指纹不匹配", "red"},
		{"known_fingerprint_failed", "指纹不匹配", "red"},
		{"future_reason", "检测未完成", "orange"},
	}
	for _, testCase := range cases {
		t.Run(testCase.reason, func(t *testing.T) {
			alert := &model.VideoVerificationAlert{ChannelID: 153, Model: "seedance-2.0", Reason: testCase.reason, DetectedAt: detectedAt}
			card := videoVerificationAlertCard(alert, "Apimart_seedance", "owner@example.com")
			require.Equal(t, "2.0", card["schema"])
			header := card["header"].(map[string]any)
			require.Equal(t, testCase.color, header["template"])
			require.Equal(t, "[apimaster] Seedance · "+testCase.title, header["title"].(map[string]any)["content"])
			encoded, err := common.Marshal(card)
			require.NoError(t, err)
			content := string(encoded)
			require.Contains(t, content, "Apimart_seedance · #153")
			require.Contains(t, content, "**用户邮箱**　owner@example.com")
			require.Contains(t, content, "seedance-2.0")
			require.Contains(t, content, "2026-10-09 15:10:08（北京时间）")
			require.Contains(t, content, testCase.reason)
			require.NotContains(t, content, "仅内部监测")
			require.NotContains(t, content, "不影响用户")
			require.NotContains(t, content, "验证失败")
			require.NotContains(t, content, "指纹不匹配或检测未完成")
		})
	}
}

func TestVideoVerificationAlertCardMissingChannel(t *testing.T) {
	alert := &model.VideoVerificationAlert{ChannelID: 288, Reason: "channel_unavailable"}
	card := videoVerificationAlertCard(alert, "#288", "")
	content := fmt.Sprint(card)
	require.Contains(t, content, "#288")
	require.NotContains(t, content, "#288 · #288")
	require.Contains(t, content, "**用户邮箱**　未获取")
}

func TestVideoVerificationIncompleteEvidenceIsPreservedInAlert(t *testing.T) {
	db := videoVerificationTestDB(t)
	fresh := false
	now := time.Date(2026, 10, 9, 7, 10, 8, 0, time.UTC)
	result := videoVerificationResult{Claimed: "SD2.0", Pipeline: "SD2.0", Family: "ark", Verdict: "match"}
	result.Baseline.Expires, result.Baseline.Stale = "2026-11-01", &fresh
	result.Checks = []model.VideoFingerprintCheck{
		{ID: "claim", Status: "pass", Value: "SD2.0"},
		{ID: "dimensions", Status: "pass", Value: "864x496 · 480p · 16:9"},
		{ID: "family", Status: "pass", Value: "Lavf58.76.100 · tb 1/12288 · B 2 · moov-last"},
		{ID: "x264", Status: "pass", Value: "superfast · crf 20.0"},
		{ID: "frames", Status: "skip", Value: "193", Expected: "97 / 121"},
	}
	status, reason, checks := classifyVerifiedVideo(result, "seedance-2.0", now)
	require.Equal(t, "notcomplete", status)
	require.Equal(t, "incomplete_evidence", reason)
	require.Equal(t, result.Checks, checks)
	task := &model.Task{ID: 1, ChannelId: 153, Properties: model.Properties{OriginModelName: "seedance-2.0"}}
	state, claimed, err := claimVideoVerification(context.Background(), task, "seedance-2.0", now.Unix(), now.Unix())
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, finishVideoVerification(context.Background(), state, task, status, reason, checks, now.Unix()))
	var alert model.VideoVerificationAlert
	require.NoError(t, db.First(&alert).Error)
	var persisted []model.VideoFingerprintCheck
	require.NoError(t, common.Unmarshal([]byte(alert.VideoChecksJSON), &persisted))
	require.Equal(t, result.Checks, persisted)
	require.Nil(t, model.PublicVideoFingerprintChecks(alert.VideoChecksJSON))
	card := videoVerificationAlertCard(&alert, "Apimart_seedance", "owner@example.com")
	content := fmt.Sprint(card)
	require.Contains(t, content, "4/5 通过")
	for _, expected := range []string{"声明版本", "视频尺寸", "容器指纹", "x264 编码", "SD2.0", "864x496", "Lavf58.76.100", "superfast · crf 20.0", "未判定", "193（基线：97 / 121）"} {
		require.Contains(t, content, expected)
	}
	var log model.ChannelDetectLog
	require.NoError(t, db.First(&log).Error)
	require.Equal(t, alert.VideoChecksJSON, log.VideoChecksJSON)
}

func TestVideoVerificationAlertEvidenceHandlesMissingChecks(t *testing.T) {
	for _, raw := range []string{"", "null", "invalid", "[]"} {
		require.Empty(t, videoVerificationAlertEvidence(raw))
	}
	evidence := videoVerificationAlertEvidence(`[{"id":"frames","status":"fail","value":"193","expected":"97 / 121"}]`)
	require.Contains(t, evidence, "0/5 通过")
	require.Contains(t, evidence, "未返回检测结果")
	require.Contains(t, evidence, "❌ 不通过")
	require.Contains(t, evidence, "193（基线：97 / 121）")
}

func TestVideoVerificationAlertUserEmail(t *testing.T) {
	db := videoVerificationTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.User{}))
	users := []model.User{
		{Id: 7, Username: "owner", Email: "owner@example.com", AffCode: "owner"},
		{Id: 8, Username: "other", Email: "other@example.com", AffCode: "other"},
		{Id: 9, Username: "no-email", AffCode: "no-email"},
	}
	require.NoError(t, db.Create(&users).Error)
	tasks := []model.Task{{ID: 1, UserId: 7}, {ID: 2, UserId: 8}, {ID: 3, UserId: 9}, {ID: 4, UserId: 99}}
	require.NoError(t, db.Create(&tasks).Error)
	for _, testCase := range []struct {
		taskID int64
		email  string
	}{{1, "owner@example.com"}, {2, "other@example.com"}, {3, ""}, {4, ""}, {99, ""}} {
		email, err := videoVerificationAlertUserEmail(testCase.taskID)
		require.NoError(t, err)
		require.Equal(t, testCase.email, email)
	}
	require.NoError(t, db.Delete(&users[0]).Error)
	email, err := videoVerificationAlertUserEmail(1)
	require.NoError(t, err)
	require.Empty(t, email)
	require.NoError(t, db.Migrator().DropTable(&model.User{}))
	_, err = videoVerificationAlertUserEmail(1)
	require.Error(t, err)
}
