package service

import (
	"io"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// SafeRealtimeUpstreamFrame is only for upstream-to-client messages. A recoverable
// error is an event, not a failed connection: log each event without triggering
// channel disable/retry, closing the socket, or changing usage settlement.
func SafeRealtimeUpstreamFrame(c *gin.Context, data string) (string, bool) {
	frame := gjson.Parse(data)
	hasError := func(v gjson.Result) bool {
		return v.Exists() && v.Type != gjson.Null && v.Type != gjson.False && (v.Type != gjson.String || v.Str != "") && (v.Type != gjson.Number || v.Num != 0)
	}
	eventType := frame.Get("type").String()
	path := "error"
	if hasError(frame.Get("response.status_details.error")) {
		path = "response.status_details.error"
	} else if hasError(frame.Get("response.error")) {
		path = "response.error"
	}
	failed := frame.Get("response.status").String() == "failed"
	if !hasError(frame.Get(path)) && eventType != "error" && eventType != "response.failed" && eventType != "response.error" && !(eventType == "response.done" && failed) {
		return data, false
	}
	// Build fresh diagnostics for every event; never reuse a previous response's
	// failure from this long-lived connection. 101 is the WebSocket handshake.
	values := PublicErrorRedactionValues(c)
	var headers http.Header
	if c != nil {
		if value, ok := c.Get("public_realtime_handshake"); ok {
			if metadata, ok := value.(*sdkResponseMetadata); ok && metadata.channelID == c.GetInt("channel_id") {
				headers = metadata.headers
			}
		}
		values = append(values, c.GetStringSlice("public_realtime_identity_values")...)
	}
	detail := common.NewPublicUpstreamError(http.StatusSwitchingProtocols, []byte(data), headers, values...)
	// Use a separate copy: attaching detail to its own Body would create a cycle.
	safe := common.NewPublicUpstreamError(0, []byte(data), nil, detail.RedactionValues()...)
	object, ok := safe.Body.(map[string]any)
	if !ok || safe.Truncated {
		errorObject := map[string]any{"type": "upstream_error", "code": "upstream_error", "message": "Realtime error diagnostic exceeded the public limit", "upstream_error": detail}
		object = map[string]any{"type": "error", "error": errorObject}
		if eventType == "response.done" || eventType == "response.failed" || eventType == "response.error" {
			response := map[string]any{"status": "failed", "status_details": map[string]any{"type": "failed", "error": errorObject}, "output": []any{}}
			if usage := frame.Get("response.usage"); usage.Exists() {
				response["usage"] = usage.Value()
			}
			object = map[string]any{"type": eventType, "response": response}
		}
		if id := frame.Get("event_id").String(); len(id) > 0 && len(id) <= 256 {
			object["event_id"] = common.SanitizeUpstreamErrorText(id, detail.RedactionValues()...)
		}
	} else {
		switch eventType {
		case "error", "response.done", "response.failed", "response.error":
			object["type"] = eventType
		}
		if response, ok := object["response"].(map[string]any); ok {
			// Partial generated content and usage retain their protocol types/values.
			// They are not included unredacted in the diagnostic or error log.
			for _, key := range []string{"usage", "output"} {
				value := frame.Get("response." + key)
				if value.Exists() {
					response[key] = value.Value()
				}
			}
			if frame.Get("response.object").String() == "realtime.response" {
				response["object"] = "realtime.response"
			}
		}
		target := object
		if failed && path == "error" && !hasError(frame.Get(path)) {
			path = "response.status_details.error"
		}
		switch path {
		case "response.error", "response.status_details.error":
			response, ok := object["response"].(map[string]any)
			if !ok {
				response = map[string]any{}
				object["response"] = response
			}
			target = response
			if path == "response.status_details.error" {
				status, ok := response["status_details"].(map[string]any)
				if !ok {
					status = map[string]any{}
					response["status_details"] = status
				}
				target = status
			}
		}
		errorObject, ok := target["error"].(map[string]any)
		if !ok {
			message := "Upstream realtime response failed without error details"
			if text, ok := target["error"].(string); ok && text != "" {
				message = text
			}
			errorObject = map[string]any{"type": "upstream_error", "code": "upstream_error", "message": message}
			target["error"] = errorObject
		}
		errorObject["upstream_error"] = detail
	}
	encoded, err := common.Marshal(object)
	if err != nil {
		return `{"type":"error","error":{"code":"upstream_error","message":"Realtime response failed"}}`, true
	}
	recordRealtimeErrorLog(c, detail, object)
	return string(encoded), true
}

func recordRealtimeErrorLog(c *gin.Context, detail *common.PublicUpstreamError, object map[string]any) {
	if c == nil || !constant.ErrorLogEnabled || c.GetInt("id") <= 0 || model.LOG_DB == nil {
		return
	}
	// Store only sanitized diagnostics, never the session's audio/text payloads.
	raw, _ := common.Marshal(detail)
	other := map[string]any{"error_type": "realtime_error", "error_code": "upstream_error", "status_code": 0, "upstream_error": detail, "realtime_event_type": object["type"], "realtime_event_error": true}
	if c.Request != nil && c.Request.URL != nil {
		other["request_path"] = c.Request.URL.Path
	}
	start := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
	elapsed := 0
	if !start.IsZero() {
		elapsed = int(time.Since(start).Seconds())
	}
	_ = model.RecordErrorLog(c, c.GetInt("id"), c.GetInt("channel_id"), c.GetString("original_model"), c.GetString("token_name"), "Realtime upstream error | upstream_error: "+string(raw), c.GetInt("token_id"), elapsed, true, c.GetString("group"), other)
}

// ObserveRealtimeHandshake captures a rejected upgrade before the dial response
// is discarded, and retains only allowlisted response IDs after a successful one.
func ObserveRealtimeHandshake(c *gin.Context, response *http.Response, upstreamURL string, secrets ...string) {
	if c == nil || response == nil {
		return
	}
	values := append(PublicErrorRedactionValues(c), common.UpstreamIdentityValues("", upstreamURL, "", "")...)
	values = append(values, secrets...)
	c.Set("public_realtime_identity_values", values)
	if response.StatusCode == http.StatusSwitchingProtocols {
		headers := make(http.Header)
		for _, key := range []string{"X-Request-Id", "Request-Id", "X-Tt-Logid", "X-Amzn-Requestid"} {
			if value := response.Header.Get(key); value != "" {
				headers.Set(key, value)
			}
		}
		c.Set("public_realtime_handshake", &sdkResponseMetadata{c.GetInt("channel_id"), response.StatusCode, headers})
		return
	}
	var body []byte
	if response.Body != nil {
		body, _ = io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
		_ = response.Body.Close()
	}
	detail := common.NewPublicUpstreamError(response.StatusCode, body, response.Header, values...)
	// Gorilla's failed upgrade response exposes at most 1024 body bytes. Be
	// explicit that a response hitting that library limit may be incomplete.
	if len(body) >= 1024 {
		detail.Truncated = true
	}
	c.Set(streamErrorCaptureKey, &capturedStreamError{c.GetInt("channel_id"), detail})
}
