package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/shopspring/decimal"
)

type callCostAlertRule struct {
	ChannelID     int             `json:"channel_id"`
	DailyUSD      decimal.Decimal `json:"daily_usd"`
	HourlyUSD     decimal.Decimal `json:"hourly_usd"`
	FiveMinuteUSD decimal.Decimal `json:"five_minute_usd"`
}

func parseCallCostAlertRules(raw string) ([]callCostAlertRule, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	if len(raw) > 32768 {
		return nil, fmt.Errorf("CALL_COST_ALERT_RULES is too large")
	}
	var fields []map[string]any
	if err := common.UnmarshalJsonStr(raw, &fields); err != nil {
		return nil, fmt.Errorf("CALL_COST_ALERT_RULES must be a JSON array")
	}
	if len(fields) > 64 || strings.TrimSpace(raw) == "null" {
		return nil, fmt.Errorf("CALL_COST_ALERT_RULES must contain at most 64 rules")
	}
	for i, field := range fields {
		if value, ok := field["channel_id"]; !ok || value == nil {
			return nil, fmt.Errorf("rule %d requires channel_id (0 means this gateway's total)", i+1)
		}
		for key, value := range field {
			if (key != "channel_id" && key != "daily_usd" && key != "hourly_usd" && key != "five_minute_usd") || value == nil {
				return nil, fmt.Errorf("rule %d contains an invalid field", i+1)
			}
		}
	}
	var rules []callCostAlertRule
	if err := common.UnmarshalJsonStr(raw, &rules); err != nil {
		return nil, fmt.Errorf("CALL_COST_ALERT_RULES contains invalid channels or amounts")
	}
	seen := map[int]bool{}
	maxAmount := decimal.NewFromInt(1_000_000_000)
	for i, rule := range rules {
		if rule.ChannelID < 0 || seen[rule.ChannelID] {
			return nil, fmt.Errorf("rule %d has a negative or duplicate channel_id", i+1)
		}
		seen[rule.ChannelID] = true
		for _, amount := range []decimal.Decimal{rule.DailyUSD, rule.HourlyUSD, rule.FiveMinuteUSD} {
			if amount.Exponent() < -10 || amount.Exponent() > 9 || amount.IsNegative() || amount.GreaterThan(maxAmount) {
				return nil, fmt.Errorf("rule %d threshold must be between 0 and 1000000000 with at most 10 decimal places", i+1)
			}
		}
		if rule.DailyUSD.IsZero() && rule.HourlyUSD.IsZero() && rule.FiveMinuteUSD.IsZero() {
			return nil, fmt.Errorf("rule %d requires a positive daily_usd, hourly_usd or five_minute_usd", i+1)
		}
	}
	return rules, nil
}

type callCostAlertWindow struct {
	kind       string
	label      string
	start, end time.Time
	nextSendAt time.Time
}

func callCostAlertWindows(now time.Time) []callCostAlertWindow {
	local := now.In(time.FixedZone("Asia/Shanghai", 8*3600))
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	return []callCostAlertWindow{
		{kind: "daily", label: "今日累计", start: day, end: local, nextSendAt: day.AddDate(0, 0, 1)},
		{kind: "hourly", label: "最近 60 分钟", start: local.Add(-time.Hour), end: local, nextSendAt: local.Add(time.Hour)},
		{kind: "five_minute", label: "最近 5 分钟", start: local.Add(-5 * time.Minute), end: local, nextSendAt: local.Add(5 * time.Minute)},
	}
}

func (r callCostAlertRule) threshold(kind string) decimal.Decimal {
	if kind == "daily" {
		return r.DailyUSD
	}
	if kind == "five_minute" {
		return r.FiveMinuteUSD
	}
	return r.HourlyUSD
}

func callCostAlertKey(rule callCostAlertRule, kind string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("call-cost-v1:%d:%s:%s", rule.ChannelID, kind, rule.threshold(kind).String()))))
}

type callCostAlertRunner struct {
	now         func() time.Time
	load        func(context.Context, int64, int64, []int) ([]model.CallCostTotal, error)
	acquire     func(context.Context, string, time.Time) (string, bool, error)
	finish      func(context.Context, string, string, int64) error
	send        func(string, map[string]any) error
	destination string
}

func (runner callCostAlertRunner) run(rules []callCostAlertRule, now time.Time) {
	windows := callCostAlertWindows(now)
	windowTotals := map[string]map[int]model.CallCostTotal{}
	for _, window := range windows {
		if !window.start.Before(window.end) {
			continue
		}
		var channelIDs []int
		allChannels, enabled := false, false
		for _, rule := range rules {
			// Always load the recent five minutes for the summary on every alert.
			if window.kind != "five_minute" && !rule.threshold(window.kind).IsPositive() {
				continue
			}
			enabled = true
			if rule.ChannelID == 0 {
				allChannels = true
			} else {
				channelIDs = append(channelIDs, rule.ChannelID)
			}
		}
		if !enabled {
			continue
		}
		if allChannels {
			channelIDs = nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		rows, err := runner.load(ctx, window.start.Unix(), window.end.Unix(), channelIDs)
		cancel()
		if err != nil {
			logger.LogWarn(context.Background(), fmt.Sprintf("call-cost alert %s query failed: %v", window.kind, err))
			continue
		}
		totals := map[int]model.CallCostTotal{}
		var total model.CallCostTotal
		for _, row := range rows {
			totals[row.ChannelID] = row
			total.CostUSD = total.CostUSD.Add(row.CostUSD)
			total.RequestCount += row.RequestCount
			total.UnknownRequests += row.UnknownRequests
		}
		totals[0] = total
		windowTotals[window.kind] = totals
	}
	for _, window := range windows {
		totals, loaded := windowTotals[window.kind]
		if !loaded {
			continue
		}
		for _, rule := range rules {
			amount := totals[rule.ChannelID]
			threshold := rule.threshold(window.kind)
			exceeded := amount.AmountUSD().GreaterThanOrEqual(threshold)
			if window.kind == "five_minute" {
				// The short-window rule is strictly greater than, unlike daily/hourly.
				exceeded = amount.AmountUSD().GreaterThan(threshold)
				logger.LogInfo(context.Background(), fmt.Sprintf("call-cost alert 5m summary: channel_id=%d cost_usd=%s requests=%d unknown=%d window_start=%d window_end=%d", rule.ChannelID, amount.AmountUSD().String(), amount.RequestCount, amount.UnknownRequests, window.start.Unix(), window.end.Unix()))
			}
			if threshold.IsPositive() && exceeded {
				var recent *model.CallCostTotal
				if fiveMinuteTotals, ok := windowTotals["five_minute"]; ok {
					value := fiveMinuteTotals[rule.ChannelID]
					recent = &value
				}
				runner.notify(rule, window, amount, recent)
			}
		}
	}
}

func (runner callCostAlertRunner) notify(rule callCostAlertRule, window callCostAlertWindow, total model.CallCostTotal, recent *model.CallCostTotal) {
	key := callCostAlertKey(rule, window.kind)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	token, acquired, err := runner.acquire(ctx, key, runner.now())
	cancel()
	if err != nil {
		logger.LogWarn(context.Background(), fmt.Sprintf("call-cost alert acquire failed: %v", err))
		return
	}
	if !acquired {
		return
	}
	nextSendAt := int64(0)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := runner.finish(ctx, key, token, nextSendAt); err != nil {
			logger.LogWarn(context.Background(), fmt.Sprintf("call-cost alert finish failed: %v", err))
		}
	}()
	if err := runner.send(runner.destination, buildCallCostAlertCard(rule, window, total, recent)); err != nil {
		logger.LogWarn(context.Background(), fmt.Sprintf("call-cost alert send failed: %v", err))
		return
	}
	nextSendAt = window.nextSendAt.Unix()
	if window.kind == "hourly" {
		nextSendAt = runner.now().Add(time.Hour).Unix()
	}
}

func buildCallCostAlertCard(rule callCostAlertRule, window callCostAlertWindow, total model.CallCostTotal, recent *model.CallCostTotal) map[string]any {
	scope := "本网关全部渠道"
	if rule.ChannelID > 0 {
		scope = fmt.Sprintf("渠道 #%d", rule.ChannelID)
	}
	comparison := "≥"
	if window.kind == "five_minute" {
		comparison = ">"
	}
	lines := []string{
		"**" + common.FeishuNotificationTitle("⚠️ 网关调用成本超限") + "**",
		fmt.Sprintf("范围：%s｜%s", scope, window.label),
		fmt.Sprintf("窗口：%s ～ %s（北京时间，结束时间不含）", window.start.Format("2006-01-02 15:04:05"), window.end.Format("2006-01-02 15:04:05")),
		fmt.Sprintf("累计成本：%s U（折算）｜阈值：%s %s U", total.AmountUSD().String(), comparison, rule.threshold(window.kind).String()),
		"",
		"**本次统计明细**",
		fmt.Sprintf("消费日志：%d 条｜成本未知：%d 条（未计入金额，不代表免费）", total.RequestCount, total.UnknownRequests),
	}
	if recent != nil {
		lines = append(lines, fmt.Sprintf("最近 5 分钟消耗：%s U（折算）｜消费日志：%d 条｜成本未知：%d 条", recent.AmountUSD().String(), recent.RequestCount, recent.UnknownRequests))
	} else {
		lines = append(lines, "最近 5 分钟消耗：查询失败，暂不可用")
	}
	if server := strings.TrimSpace(os.Getenv("FEISHU_SERVER_NAME")); server != "" {
		lines = append(lines, "服务器："+server)
	}
	return map[string]any{"schema": "2.0", "body": map[string]any{"elements": []any{
		map[string]any{"tag": "markdown", "content": strings.Join(lines, "\n")},
	}}}
}

var callCostAlertOnce sync.Once

func StartCallCostAlertTask() {
	startCallCostAlertTask(&callCostAlertOnce, common.IsMasterNode, runCallCostAlertTask)
}

// Even slow/failed initialization belongs to the optional alert goroutine.
// Worker startup and other background jobs must not wait for it.
func startCallCostAlertTask(once *sync.Once, master bool, task func()) {
	if !master {
		return
	}
	once.Do(func() {
		gopool.Go(func() {
			defer func() {
				if recover() != nil {
					// Do not log a panic value that could include a webhook URL.
					logger.LogWarn(context.Background(), "call-cost alert disabled: unexpected task failure")
				}
			}()
			task()
		})
	})
}

func initializeCallCostAlertTask() ([]callCostAlertRule, callCostAlertRunner, error) {
	rules, err := parseCallCostAlertRules(os.Getenv("CALL_COST_ALERT_RULES"))
	if err != nil || len(rules) == 0 {
		return nil, callCostAlertRunner{}, err
	}
	destination, err := callCostAlertWebhook()
	if err != nil {
		return nil, callCostAlertRunner{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := model.InitCallCostAlertStore(ctx); err != nil {
		return nil, callCostAlertRunner{}, fmt.Errorf("state initialization failed: %w", err)
	}
	runner := callCostAlertRunner{now: time.Now, load: model.GetCallCostTotals, acquire: model.AcquireCallCostAlert, finish: model.FinishCallCostAlert, send: sendCallCostAlert, destination: destination}
	return rules, runner, nil
}

func runCallCostAlertTask() {
	rules, runner, err := initializeCallCostAlertTask()
	if err != nil {
		logger.LogWarn(context.Background(), "call-cost alert disabled: "+err.Error())
		return
	}
	if len(rules) == 0 {
		return
	}
	logger.LogInfo(context.Background(), "call-cost alert task started (5m interval)")
	tick := func() {
		if !common.LogConsumeEnabled {
			logger.LogWarn(context.Background(), "call-cost alert skipped: consume logging is disabled")
			return
		}
		runner.run(rules, time.Now())
	}
	tick()
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		tick()
	}
}
