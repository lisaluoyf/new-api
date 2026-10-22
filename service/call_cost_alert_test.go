package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestParseCallCostAlertRules(t *testing.T) {
	for _, raw := range []string{"", "[]"} {
		rules, err := parseCallCostAlertRules(raw)
		require.NoError(t, err)
		require.Empty(t, rules)
	}
	rules, err := parseCallCostAlertRules(`[{"channel_id":0,"daily_usd":100,"hourly_usd":"0.01"},{"channel_id":59,"daily_usd":50}]`)
	require.NoError(t, err)
	require.Len(t, rules, 2)
	require.Equal(t, "0.01", rules[0].HourlyUSD.String())
	for _, raw := range []string{
		`null`, `{}`, `[null]`, `[{"daily_usd":1}]`, `[{"channel_id":1,"day_usd":10}]`,
		`[{"channel_id":-1,"daily_usd":1}]`, `[{"channel_id":1,"daily_usd":0}]`,
		`[{"channel_id":1,"daily_usd":-1}]`, `[{"channel_id":1,"daily_usd":null}]`,
		`[{"channel_id":1,"daily_usd":"NaN"}]`, `[{"channel_id":1,"daily_usd":"1e999999"}]`,
		`[{"channel_id":1,"daily_usd":"1e-999999"}]`, `[{"channel_id":1,"daily_usd":1000000001}]`,
		`[{"channel_id":1,"daily_usd":1},{"channel_id":1,"hourly_usd":2}]`,
	} {
		_, err := parseCallCostAlertRules(raw)
		require.Error(t, err, raw)
	}
}

func TestCallCostWindowsUseBeijingDayAndRollingHour(t *testing.T) {
	now := time.Date(2026, 10, 22, 16, 10, 0, 0, time.UTC) // Beijing 00:10
	windows := callCostAlertWindows(now)
	require.Equal(t, "2026-10-23 00:00", windows[0].start.Format("2006-01-02 15:04"))
	require.Equal(t, "2026-10-24 00:00", windows[0].nextSendAt.Format("2006-01-02 15:04"))
	require.Equal(t, "2026-10-22 23:10", windows[1].start.Format("2006-01-02 15:04"))
	require.Equal(t, time.Hour, windows[1].end.Sub(windows[1].start))
}

func TestCallCostRunnerThresholdScopeRetryAndDedup(t *testing.T) {
	t.Setenv("FEISHU_SERVER_NAME", "test gateway")
	rules, err := parseCallCostAlertRules(`[{"channel_id":0,"daily_usd":3,"hourly_usd":3},{"channel_id":59,"daily_usd":2},{"channel_id":60,"daily_usd":2}]`)
	require.NoError(t, err)
	now := time.Date(2026, 10, 22, 4, 0, 0, 0, time.UTC)
	rows := []model.CallCostTotal{
		{ChannelID: 59, CostUSD: decimal.NewFromInt(2), RequestCount: 4, UnknownRequests: 2},
		{ChannelID: 60, CostUSD: decimal.NewFromInt(1), RequestCount: 1},
	}
	sentUntil := map[string]int64{}
	attempts, successes, loads := 0, 0, 0
	runner := callCostAlertRunner{
		destination: "test-only",
		now:         func() time.Time { return now },
		load: func(_ context.Context, start, end int64, channels []int) ([]model.CallCostTotal, error) {
			loads++
			require.Empty(t, channels) // global rule includes all channels
			require.Less(t, start, end)
			return rows, nil
		},
		acquire: func(_ context.Context, key string, at time.Time) (string, bool, error) {
			return "lease", sentUntil[key] <= at.Unix(), nil
		},
		finish: func(_ context.Context, key, token string, next int64) error {
			require.Equal(t, "lease", token)
			sentUntil[key] = next
			return nil
		},
		send: func(chat string, card map[string]any) error {
			attempts++
			require.Equal(t, "test-only", chat)
			content := fmt.Sprint(card)
			require.Contains(t, content, "成本未知：2 条")
			require.Contains(t, content, "累计成本：")
			for _, removed := range []string{"本次报警规则", "统计规则", "数据限制", "处理建议"} {
				require.NotContains(t, content, removed)
			}
			require.Contains(t, content, "test gateway")
			if attempts == 1 {
				return errors.New("simulated Feishu rejection")
			}
			successes++
			return nil
		},
	}
	runner.run(rules, now)
	require.Equal(t, 3, attempts) // global daily, channel 59 daily, global hourly; channel 60 below threshold
	require.Equal(t, 2, successes)
	now = now.Add(5 * time.Minute)
	runner.run(rules, now)
	require.Equal(t, 4, attempts) // retry only the failed global daily
	require.Equal(t, 3, successes)
	runner.run(rules, now)
	require.Equal(t, 4, attempts)
	now = now.Add(time.Hour)
	runner.run(rules, now)
	require.Equal(t, 5, attempts) // rolling-hour reminder after 60-minute cooldown
	require.Equal(t, 8, loads)
	now = now.AddDate(0, 0, 1)
	runner.run(rules, now)
	require.Equal(t, 8, attempts) // next Beijing day resets daily notifications
}

func TestCallCostRunnerQueryFailureAndSingleChannelScope(t *testing.T) {
	rules, err := parseCallCostAlertRules(`[{"channel_id":59,"hourly_usd":1}]`)
	require.NoError(t, err)
	loaded := 0
	runner := callCostAlertRunner{load: func(_ context.Context, _, _ int64, ids []int) ([]model.CallCostTotal, error) {
		loaded++
		require.Equal(t, []int{59}, ids)
		return nil, errors.New("database unavailable")
	}}
	// Other callbacks are nil: a failed query must never acquire/send a zero or partial result.
	runner.run(rules, time.Now())
	require.Equal(t, 1, loaded)
}

func TestCallCostAlertKeySeparatesRuleWindowsAndThresholds(t *testing.T) {
	a := callCostAlertRule{ChannelID: 59, DailyUSD: decimal.NewFromInt(10), HourlyUSD: decimal.NewFromInt(10)}
	b := a
	b.DailyUSD = decimal.NewFromInt(20)
	require.NotEqual(t, callCostAlertKey(a, "daily"), callCostAlertKey(a, "hourly"))
	require.NotEqual(t, callCostAlertKey(a, "daily"), callCostAlertKey(b, "daily"))
	b.DailyUSD = decimal.RequireFromString("10.0")
	require.Equal(t, callCostAlertKey(a, "daily"), callCostAlertKey(b, "daily"))
	require.Len(t, strings.TrimSpace(callCostAlertKey(a, "daily")), 64)
}

func TestCallCostInitializationFailureOnlyDisablesAlert(t *testing.T) {
	oldLogDB := model.LOG_DB
	model.LOG_DB = nil
	t.Cleanup(func() { model.LOG_DB = oldLogDB })
	t.Setenv("CALL_COST_ALERT_WEBHOOK_URL", "https://open.feishu.cn/open-apis/bot/v2/hook/test-only")
	for _, rules := range []string{"", "[]"} {
		t.Setenv("CALL_COST_ALERT_RULES", rules)
		configured, runner, err := initializeCallCostAlertTask()
		require.NoError(t, err) // disabled rules do not touch the unavailable DB
		require.Empty(t, configured)
		require.Nil(t, runner.load)
	}
	t.Setenv("CALL_COST_ALERT_RULES", `[{"channel_id":0,"daily_usd":100,"hourly_usd":20}]`)
	configured, runner, err := initializeCallCostAlertTask()
	require.ErrorContains(t, err, "state initialization failed")
	require.Empty(t, configured)
	require.Nil(t, runner.load)
	runCallCostAlertTask() // returns instead of killing the worker or starting checks
	t.Setenv("CALL_COST_ALERT_WEBHOOK_URL", "")
	_, _, err = initializeCallCostAlertTask()
	require.ErrorContains(t, err, "webhook")
	runCallCostAlertTask()
	t.Setenv("CALL_COST_ALERT_RULES", "invalid")
	runCallCostAlertTask()
}

func TestCallCostStartupNeverWaitsForOptionalInitialization(t *testing.T) {
	var once sync.Once
	entered, release, exited, returned := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer close(release)
	go func() {
		startCallCostAlertTask(&once, true, func() {
			close(entered)
			<-release // simulates a slow schema operation
			close(exited)
		})
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("worker startup blocked by optional initialization")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("optional task did not start")
	}
	select {
	case <-exited:
		t.Fatal("test initialization should still be waiting")
	default:
	}
	startCallCostAlertTask(&once, true, func() { t.Error("duplicate alert worker") })
}

func TestCallCostSlaveSkipsInitializationAndPanicIsContained(t *testing.T) {
	var slaveOnce, panicOnce sync.Once
	startCallCostAlertTask(&slaveOnce, false, func() { t.Error("slave must not initialize alert") })
	done := make(chan struct{})
	startCallCostAlertTask(&panicOnce, true, func() {
		defer close(done)
		panic("simulated optional initialization failure")
	})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("optional task did not execute")
	}
}
