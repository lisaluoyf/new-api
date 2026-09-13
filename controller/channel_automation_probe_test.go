package controller

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestModelNotFoundDisableProbeProtectsChannelModels(t *testing.T) {
	for _, tc := range []struct {
		name        string
		model       string
		probeOK     bool
		wantProbes  int
		wantDisable bool
	}{
		{"probe passes", "gpt-6-astra", true, 1, false},
		{"probe confirms failure", "gpt-6-astra", false, 1, true},
		{"user misspells model", "gpt-6-atsra", false, 0, false},
		{"empty model", "", false, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupModelDataToggleTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.User{}))
			require.NoError(t, db.Create(&model.User{Id: 1, Username: "root", Role: common.RoleRootUser}).Error)
			t.Setenv("FEISHU_CHANNEL_CHAT_ID", "")
			t.Setenv("FEISHU_OPS_CHAT_ID", "")
			redisEnabled := common.RedisEnabled
			common.RedisEnabled = false
			t.Cleanup(func() { common.RedisEnabled = redisEnabled })
			probeCount := 0
			var probeBody string
			flask := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				probeCount++
				body, _ := io.ReadAll(r.Body)
				probeBody = string(body)
				w.Header().Set("Content-Type", "application/json")
				if tc.probeOK {
					_, _ = w.Write([]byte(`{"ok":true,"latency_ms":1}`))
				} else {
					_, _ = w.Write([]byte(`{"ok":false,"error":"model_not_found"}`))
				}
			}))
			defer flask.Close()
			t.Setenv("APIMASTER_FLASK_URL", flask.URL)
			baseURL, setting, autoBan := "https://upstream.example", `{"client_exclusive":"codex"}`, 1
			channel := &model.Channel{
				Id: 219, Name: "probe-test", Status: common.ChannelStatusEnabled,
				Key: "test-key", BaseURL: &baseURL, Setting: &setting, AutoBan: &autoBan,
				Models: "gpt-6-astra,gpt-5.6-terra", Group: "default",
			}
			require.NoError(t, channel.Insert())
			err := types.WithOpenAIError(types.OpenAIError{
				Message: "Model is not supported by any configured account in this group", Type: "model_not_found",
			}, http.StatusNotFound)
			probeBeforeDisablingChannel(types.ChannelError{ChannelId: channel.Id, ChannelName: channel.Name, AutoBan: true}, err, err.ErrorWithStatusCode(), tc.model)
			require.Equal(t, tc.wantProbes, probeCount)
			if probeCount > 0 {
				require.Contains(t, probeBody, `"model":"gpt-6-astra"`)
				require.Contains(t, probeBody, `"base_url":"https://upstream.example"`)
			}
			current, loadErr := model.GetChannelById(channel.Id, true)
			require.NoError(t, loadErr)
			require.Equal(t, common.ChannelStatusEnabled, current.Status)
			_, disabled := current.GetDisabledModels()["gpt-6-astra"]
			require.Equal(t, tc.wantDisable, disabled)
			require.NotContains(t, current.GetDisabledModels(), "gpt-5.6-terra")
			var events []model.ChannelModelEvent
			require.NoError(t, db.Find(&events).Error)
			if tc.wantDisable {
				require.Len(t, events, 1)
				require.Equal(t, "health_probe", events[0].Source)
				require.Equal(t, "gpt-6-astra", events[0].Model)
			} else {
				require.Empty(t, events)
			}
		})
	}
}

func TestProbeChannelForAutomationUsesClaudeCLIForExclusiveChannel(t *testing.T) {
	var gotPath string
	var gotBody string
	flask := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"latency_ms":1234}`))
	}))
	defer flask.Close()
	t.Setenv("APIMASTER_FLASK_URL", flask.URL)

	baseURL := "https://cc-only.example"
	setting := `{"client_exclusive":"claude_code"}`
	channel := &model.Channel{
		Id:      42,
		BaseURL: &baseURL,
		Key:     "secret-test-key",
		Models:  "claude-fable-5",
		Setting: &setting,
	}

	result, latencyMs := probeChannelForAutomation(channel, "claude-fable-5")

	require.NoError(t, result.localErr)
	require.Nil(t, result.newAPIError)
	require.Equal(t, int64(1234), latencyMs)
	require.Equal(t, "/internal/uptime-probe", gotPath)
	require.Contains(t, gotBody, `"model":"claude-fable-5"`)
	require.Contains(t, gotBody, `"base_url":"https://cc-only.example"`)
	require.Contains(t, gotBody, `"api_format":"claude-cli"`)
}

func TestProbeChannelForAutomationUsesCodexCLIForExclusiveChannel(t *testing.T) {
	var gotPath string
	var gotBody string
	flask := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"latency_ms":2345,"api_format":"codex-cli"}`))
	}))
	defer flask.Close()
	t.Setenv("APIMASTER_FLASK_URL", flask.URL)

	baseURL := "https://codex-only.example"
	setting := `{"client_exclusive":"codex"}`
	channel := &model.Channel{
		Id:      11,
		BaseURL: &baseURL,
		Key:     "secret-test-key",
		Models:  "gpt-5.5",
		Setting: &setting,
	}

	result, latencyMs := probeChannelForAutomation(channel, "gpt-5.5")

	require.NoError(t, result.localErr)
	require.Nil(t, result.newAPIError)
	require.Equal(t, int64(2345), latencyMs)
	require.Equal(t, "/internal/uptime-probe", gotPath)
	require.Contains(t, gotBody, `"model":"gpt-5.5"`)
	require.Contains(t, gotBody, `"base_url":"https://codex-only.example"`)
	require.Contains(t, gotBody, `"api_format":"codex-cli"`)
}
