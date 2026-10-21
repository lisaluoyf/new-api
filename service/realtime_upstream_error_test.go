package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRealtimeUpstreamErrorFrameAndCustomerLog(t *testing.T) {
	oldDB, oldLog, oldEnabled := model.DB, model.LOG_DB, constant.ErrorLogEnabled
	t.Cleanup(func() { model.DB, model.LOG_DB, constant.ErrorLogEnabled = oldDB, oldLog, oldEnabled })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	model.DB, model.LOG_DB, constant.ErrorLogEnabled = db, db, true
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Log{}, &model.Channel{}))
	require.NoError(t, db.Create(&model.User{Id: 701, Username: "realtime-test"}).Error)
	c := identityTestContext()
	c.Set("id", 701)
	c.Set("username", "realtime-test")
	c.Set("token_id", 702)
	c.Set("channel_id", 703)
	c.Set(common.RequestIdKey, "realtime-request")
	frames := []string{
		`{"type":"error","event_id":"evt_1","provider":"HiddenRelay","error":{"code":"HiddenRelay.InvalidImage","message":"HiddenRelay expected image/jpeg for image[0] at https://api.private-route.ai/v1; Bearer sk-private-key","details":{"actual":"video/mp4","api_key":"secret-value"}}}`,
		`{"type":"response.done","event_id":"evt_2","response":{"object":"realtime.response","status":"failed","status_details":{"type":"failed","error":{"code":"HiddenRelay.Overloaded","message":"HiddenRelay capacity exhausted","details":{"account":"private-account"}}},"usage":{"total_tokens":17},"output":[{"type":"message","content":[{"type":"text","text":"Partial user content"}]}]}}`,
		`{"type":"response.done","event_id":"evt_3","response":{"status":"failed","usage":null,"output":[]}}`,
		`{"type":"response.error","event_id":"evt_4","response":{"error":{"message":"new failure only"}}}`,
	}
	for i, frame := range frames {
		safe, failed := SafeRealtimeUpstreamFrame(c, frame)
		require.True(t, failed)
		require.Equal(t, gjson.Get(frame, "type").String(), gjson.Get(safe, "type").String())
		require.Equal(t, gjson.Get(frame, "event_id").String(), gjson.Get(safe, "event_id").String())
		for _, value := range []string{"HiddenRelay", "private-route.ai", "sk-private-key", "secret-value", "private-account"} {
			require.NotContains(t, safe, value)
		}
		path := "error.upstream_error"
		if i == 1 || i == 2 {
			path = "response.status_details.error.upstream_error"
		}
		if i == 3 {
			path = "response.error.upstream_error"
			require.NotContains(t, safe, "capacity exhausted")
		}
		detail := gjson.Get(safe, path)
		require.True(t, detail.Exists())
		if i == 1 {
			require.Equal(t, int64(17), gjson.Get(safe, "response.usage.total_tokens").Int())
			require.True(t, gjson.Get(safe, "response.output").IsArray())
			require.Contains(t, safe, "Partial user content")
		}
		var row model.Log
		require.NoError(t, db.Order("id desc").First(&row).Error)
		require.True(t, row.IsStream)
		require.Equal(t, 0, row.Quota)
		require.JSONEq(t, detail.Raw, gjson.Get(row.Other, "upstream_error").Raw)
		require.NotContains(t, row.Other, "Partial user content")
	}
	// A successful response in the same connection must not hide earlier events.
	require.NoError(t, db.Create(&model.Log{UserId: 701, RequestId: "realtime-request", Type: model.LogTypeConsume, Other: "{}"}).Error)

	logs, total, err := model.GetUserLogs(701, model.LogTypeError, 0, 0, "", "", 0, 20, "", "realtime-request")
	require.NoError(t, err)
	require.EqualValues(t, len(frames), total)
	require.Len(t, logs, len(frames))
	for _, log := range logs {
		require.NotZero(t, log.RequestId)
		require.NotContains(t, log.Other, "HiddenRelay")
		require.True(t, gjson.Get(log.Other, "upstream_error").Exists())
	}
	_, total, err = model.GetUserLogs(999, model.LogTypeError, 0, 0, "", "", 0, 20, "", "realtime-request")
	require.NoError(t, err)
	require.Zero(t, total)
}

func TestRealtimeBusinessFramesUntouchedAndErrorsBounded(t *testing.T) {
	c := identityTestContext()
	for _, frame := range []string{
		`{"type":"response.audio.delta","delta":"raw audio HiddenRelay"}`,
		`{"type":"session.created","session":{"id":"session-private","instructions":"HiddenRelay user content"}}`,
		`{"type":"response.done","response":{"status":"completed","status_details":null,"usage":{"total_tokens":17},"output":[]}}`,
		`{"type":"response.done","response":{"status":"cancelled","status_details":{"reason":"client_cancelled"}}}`,
		`{"type":"response.done","response":{"status":"completed","error":false}}`,
	} {
		safe, failed := SafeRealtimeUpstreamFrame(c, frame)
		require.False(t, failed)
		require.Equal(t, frame, safe)
	}
	safe, failed := SafeRealtimeUpstreamFrame(c, `{"type":"error","error":{"message":"`+strings.Repeat("x", 2<<20)+`"}}`)
	require.True(t, failed)
	require.Less(t, len(safe), common.MaxPublicUpstreamErrorBytes)
	require.True(t, gjson.Get(safe, "error.upstream_error.truncated").Bool())
	require.Equal(t, "error", gjson.Get(safe, "type").String())
}

func TestRealtimeHandshakeDiagnostics(t *testing.T) {
	c := identityTestContext()
	ObserveRealtimeHandshake(c, &http.Response{StatusCode: 429, Header: http.Header{"X-Request-Id": []string{"opaque-handshake-id"}}, Body: io.NopCloser(strings.NewReader(`{"provider":"SecretRelay","error":{"message":"SecretRelay rate limit exceeded","api_key":"private-key"}}`))}, "wss://secret-upstream.example/v1", "private-key")
	detail := CapturedUpstreamError(c)
	require.NotNil(t, detail)
	require.Equal(t, 429, detail.StatusCode)
	require.Equal(t, "opaque-handshake-id", detail.RequestID)
	encoded, _ := common.Marshal(detail)
	require.Contains(t, string(encoded), "rate limit exceeded")
	require.NotContains(t, string(encoded), "SecretRelay")
	require.NotContains(t, string(encoded), "private-key")
	ObserveRealtimeHandshake(c, &http.Response{StatusCode: 502, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 1024)))}, "wss://secret-upstream.example/v1")
	require.True(t, CapturedUpstreamError(c).Truncated)
	ResetUpstreamErrorCapture(c)
	ObserveRealtimeHandshake(c, &http.Response{StatusCode: 101, Header: http.Header{"X-Request-Id": []string{"opaque-session-id"}, "Authorization": []string{"private-header"}}}, "wss://secret-upstream.example/v1")
	safe, failed := SafeRealtimeUpstreamFrame(c, `{"type":"error","error":{"message":"secret-upstream.example rejected format"}}`)
	require.True(t, failed)
	require.NotContains(t, safe, "secret-upstream.example")
	require.NotContains(t, safe, "private-header")
	require.Equal(t, "opaque-session-id", gjson.Get(safe, "error.upstream_error.request_id").String())
}

func TestRealtimeActualRejectedWebsocketUpgrade(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "opaque-rejected-upgrade")
		w.WriteHeader(429)
		_, _ = io.WriteString(w, `{"error":{"message":"capacity exhausted `+strings.Repeat("reason ", 300)+`"}}`)
	}))
	defer server.Close()
	upstreamURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, response, err := websocket.DefaultDialer.Dial(upstreamURL, nil)
	require.Error(t, err)
	require.Nil(t, conn)
	require.NotNil(t, response)
	c := identityTestContext()
	ObserveRealtimeHandshake(c, response, upstreamURL)
	detail := CapturedUpstreamError(c)
	require.NotNil(t, detail)
	require.Equal(t, 429, detail.StatusCode)
	require.True(t, detail.Truncated)
	require.Equal(t, "opaque-rejected-upgrade", detail.RequestID)
	encoded, _ := common.Marshal(detail)
	require.Contains(t, string(encoded), "capacity exhausted")
}

func TestRealtimeOversizedResponseKeepsTerminalProtocol(t *testing.T) {
	safe, failed := SafeRealtimeUpstreamFrame(identityTestContext(), `{"type":"response.done","event_id":"evt_long","response":{"status":"failed","status_details":{"error":{"message":"`+strings.Repeat("x", 2<<20)+`"}},"usage":{"total_tokens":9}}}`)
	require.True(t, failed)
	require.Equal(t, "response.done", gjson.Get(safe, "type").String())
	require.Equal(t, "evt_long", gjson.Get(safe, "event_id").String())
	require.True(t, gjson.Get(safe, "response.status_details.error.upstream_error.truncated").Bool())
	require.EqualValues(t, 9, gjson.Get(safe, "response.usage.total_tokens").Int())
	require.True(t, gjson.Get(safe, "response.output").IsArray())
}
