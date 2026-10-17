package service

import (
	"bytes"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func diagnosticOption(t *testing.T, cfg claudeThinkingDiagnosticConfig) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	old, existed := common.OptionMap[ClaudeThinkingDiagnosticOption]
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	raw, err := common.Marshal(cfg)
	require.NoError(t, err)
	common.OptionMap[ClaudeThinkingDiagnosticOption] = string(raw)
	common.OptionMapRWMutex.Unlock()
	thinkingDiagnosticBudget.Lock()
	thinkingDiagnosticBudget.config = ""
	thinkingDiagnosticBudget.used = 0
	thinkingDiagnosticBudget.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if existed {
			common.OptionMap[ClaudeThinkingDiagnosticOption] = old
		} else {
			delete(common.OptionMap, ClaudeThinkingDiagnosticOption)
		}
		common.OptionMapRWMutex.Unlock()
	})
}
func diagnosticScope() claudeThinkingDiagnosticConfig {
	return claudeThinkingDiagnosticConfig{Enabled: true, UserID: 13254, Model: "claude-opus-5-5", ChannelIDs: []int{221, 293, 85}, ExpiresAt: time.Now().Unix() + 900, MaxCaptures: 6}
}
func diagnosticInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{UserId: 13254, OriginModelName: "claude-opus-5-5", ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 221}}
}
func TestClaudeThinkingDiagnosticScopeAndExpiry(t *testing.T) {
	now := time.Now().Unix()
	for _, tc := range []struct {
		name   string
		change func(*claudeThinkingDiagnosticConfig)
	}{
		{"disabled", func(c *claudeThinkingDiagnosticConfig) { c.Enabled = false }},
		{"other user", func(c *claudeThinkingDiagnosticConfig) { c.UserID++ }},
		{"other model", func(c *claudeThinkingDiagnosticConfig) { c.Model = "claude-sonnet-5-5" }},
		{"other channel", func(c *claudeThinkingDiagnosticConfig) { c.ChannelIDs = []int{1} }},
		{"expired", func(c *claudeThinkingDiagnosticConfig) { c.ExpiresAt = now }},
		{"unbounded duration", func(c *claudeThinkingDiagnosticConfig) { c.ExpiresAt = now + 1801 }},
		{"zero budget", func(c *claudeThinkingDiagnosticConfig) { c.MaxCaptures = 0 }},
		{"unbounded budget", func(c *claudeThinkingDiagnosticConfig) { c.MaxCaptures = 13 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := diagnosticScope()
			tc.change(&cfg)
			diagnosticOption(t, cfg)
			require.False(t, claimThinkingDiagnostic(diagnosticInfo(), now))
		})
	}
	diagnosticOption(t, diagnosticScope())
	require.True(t, claimThinkingDiagnostic(diagnosticInfo(), now))
	require.False(t, claimThinkingDiagnostic(nil, now))
}
func TestClaudeThinkingDiagnosticConcurrentCaptureBudget(t *testing.T) {
	diagnosticOption(t, diagnosticScope())
	var accepted int32
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if claimThinkingDiagnostic(diagnosticInfo(), time.Now().Unix()) {
				atomic.AddInt32(&accepted, 1)
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 6, accepted)
}
func TestClaudeThinkingDiagnosticNeverSerializesArbitraryValues(t *testing.T) {
	body := []byte(`{"thinking":{"type":"secret-type-value","display":"secret-display-value","budget_tokens":"secret-budget-value","extra":"secret-extra"},"output_config":{"effort":"secret-effort-value"},"messages":[{"content":"secret-prompt"}],"api_key":"secret-key"}`)
	summary := summarizeThinkingParameters(body)
	raw, err := common.Marshal(summary)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "secret-")
	require.Equal(t, "other", summary.Type)
	require.True(t, summary.BudgetInvalid)
	normal := summarizeThinkingParameters([]byte(`{"thinking":{"type":"enabled","budget_tokens":4096},"output_config":{"effort":"high"}}`))
	require.Equal(t, "enabled", normal.Type)
	require.EqualValues(t, 4096, *normal.BudgetTokens)
	require.Equal(t, "high", normal.Effort)
	for _, body := range []string{`{}`, `{"thinking":null}`, `{"thinking":"secret"}`, `{"thinking":{"budget_tokens":-1}}`, `{"thinking":{"budget_tokens":1.5}}`, `{"thinking":{"budget_tokens":999999999999999}}`} {
		raw, err := common.Marshal(summarizeThinkingParameters([]byte(body)))
		require.NoError(t, err)
		require.NotContains(t, string(raw), "secret")
	}
}
func TestClaudeThinkingDiagnosticPreservesBodyAndRecordsTransformation(t *testing.T) {
	diagnosticOption(t, diagnosticScope())
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := `{"model":"claude-opus-5-5","thinking":{"type":"enabled","budget_tokens":4096},"messages":[{"role":"user","content":"private-conversation"}]}`
	ctx.Request = httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	ctx.Request.Header.Set("Authorization", "secret-credential")
	ctx.Set(common.RequestIdKey, "diagnostic-owned-request")
	storage, err := common.GetBodyStorage(ctx)
	require.NoError(t, err)
	_, err = storage.Seek(11, io.SeekStart)
	require.NoError(t, err)
	observation := BeginClaudeThinkingObservation(ctx, diagnosticInfo())
	require.NotNil(t, observation)
	position, err := storage.Seek(0, io.SeekCurrent)
	require.NoError(t, err)
	require.EqualValues(t, 11, position, "observation must not rewind the relay body")
	_, err = storage.Seek(0, io.SeekStart)
	require.NoError(t, err)
	original, err := io.ReadAll(common.ReaderOnly(storage))
	require.NoError(t, err)
	require.Equal(t, body, string(original))
	var log bytes.Buffer
	common.LogWriterMu.Lock()
	old := gin.DefaultWriter
	gin.DefaultWriter = &log
	common.LogWriterMu.Unlock()
	defer func() { common.LogWriterMu.Lock(); gin.DefaultWriter = old; common.LogWriterMu.Unlock() }()
	observation.Record(ctx, []byte(`{"thinking":{"type":"adaptive"},"output_config":{"effort":"medium"},"messages":[{"content":"other-private-conversation"}]}`), false)
	first := log.String()
	require.Contains(t, first, "CLAUDE_THINKING_DIAG")
	require.Contains(t, first, `"thinking_type":"enabled"`)
	require.Contains(t, first, `"thinking_type":"adaptive"`)
	require.Contains(t, first, `"output_effort":"medium"`)
	require.NotContains(t, first, "private-conversation")
	require.NotContains(t, first, "secret-credential")
	observation.Record(ctx, nil, true)
	require.Equal(t, first, log.String())
}
func TestClaudeThinkingDiagnosticPassthroughAndNil(t *testing.T) {
	diagnosticOption(t, diagnosticScope())
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"thinking":{"type":"disabled"}}`))
	_, err := common.GetBodyStorage(ctx)
	require.NoError(t, err)
	observation := BeginClaudeThinkingObservation(ctx, diagnosticInfo())
	require.NotNil(t, observation)
	var log bytes.Buffer
	common.LogWriterMu.Lock()
	old := gin.DefaultWriter
	gin.DefaultWriter = &log
	common.LogWriterMu.Unlock()
	defer func() { common.LogWriterMu.Lock(); gin.DefaultWriter = old; common.LogWriterMu.Unlock() }()
	observation.Record(ctx, nil, true)
	require.Contains(t, log.String(), `"passthrough":true`)
	require.Equal(t, 2, strings.Count(log.String(), `"thinking_type":"disabled"`))
	var absent *ClaudeThinkingObservation
	absent.Record(ctx, nil, false)
}
