package service

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const streamErrorCaptureKey = "public_upstream_stream_error"
const sdkResponseMetadataKey = "public_sdk_response_metadata"

type sdkResponseMetadata struct {
	channelID, status int
	headers           http.Header
}

type capturedStreamError struct {
	channelID int
	detail    *common.PublicUpstreamError
}

// Inspect error frames only. Successful stream content is never accumulated.
func CaptureUpstreamStreamError(c *gin.Context, data string, resp *http.Response, secrets ...string) {
	if c == nil {
		return
	}
	frame := gjson.Parse(data)
	hasError := func(v gjson.Result) bool { return v.Exists() && v.Type != gjson.Null }
	if !hasError(frame.Get("error")) && !hasError(frame.Get("response.error")) && frame.Get("type").String() != "error" && frame.Get("type").String() != "response.failed" {
		return
	}
	status := 0
	var headers http.Header
	if resp != nil {
		status, headers = resp.StatusCode, resp.Header
	}
	if value, ok := c.Get(sdkResponseMetadataKey); ok && len(headers) == 0 {
		if metadata, valid := value.(*sdkResponseMetadata); valid && metadata != nil && metadata.channelID == c.GetInt("channel_id") {
			headers = metadata.headers
		}
	}
	values := append(PublicErrorRedactionValues(c), secrets...)
	detail := common.NewPublicUpstreamError(status, []byte(data), headers, values...)
	c.Set(streamErrorCaptureKey, &capturedStreamError{c.GetInt("channel_id"), detail})
}

// Use the identical safe diagnostic for terminal events and the later log path.
// Internal retry/billing codes and causes remain untouched.
func PublicStreamError(c *gin.Context, failure *types.NewAPIError) types.OpenAIError {
	if failure == nil {
		return types.OpenAIError{}
	}
	AttachCapturedUpstreamError(c, failure)
	if failure.UpstreamError == nil {
		raw, _ := common.Marshal(map[string]any{"message": failure.Error()})
		failure.UpstreamError = common.NewPublicUpstreamError(0, raw, nil, PublicErrorRedactionValues(c)...)
	}
	result := PublicRelayErrorView(c, failure).ToOpenAIError()
	if c != nil {
		result.Message = common.MessageWithRequestId(result.Message, c.GetString(common.RequestIdKey))
	}
	return result
}

type sdkErrorTransport struct {
	base    http.RoundTripper
	context *gin.Context
	secrets []string
}

func (t *sdkErrorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ResetUpstreamErrorCapture(t.context)
	resp, err := t.base.RoundTrip(req)
	if resp != nil {
		headers := make(http.Header)
		for _, key := range []string{"X-Request-Id", "Request-Id", "X-Tt-Logid", "X-Amzn-Requestid"} {
			if id := resp.Header.Get(key); id != "" {
				headers.Set(key, id)
			}
		}
		t.context.Set(sdkResponseMetadataKey, &sdkResponseMetadata{t.context.GetInt("channel_id"), resp.StatusCode, headers})
	}
	if resp != nil && resp.StatusCode >= 400 {
		values := append([]string(nil), t.secrets...)
		values = append(values, common.UpstreamIdentityValues("", req.URL.String(), "", "")...)
		ObserveUpstreamErrorResponse(t.context, resp, values...)
	}
	return resp, err
}

// Clone the SDK client rather than modifying a shared transport or its timeouts.
// HTTP status/body/request ID are observed before the SDK deserializes the error.
func SDKErrorHTTPClient(c *gin.Context, client *http.Client, secrets ...string) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	if c == nil {
		return client
	}
	clone := *client
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	clone.Transport = &sdkErrorTransport{transport, c, append([]string(nil), secrets...)}
	return &clone
}

// SDK event streams and transport errors may have no HTTP response body. Extract
// typed diagnostics without reflecting arbitrary SDK objects (which hold secrets).
func AttachSDKUpstreamError(c *gin.Context, failure *types.NewAPIError, cause error, secrets ...string) *types.NewAPIError {
	if failure == nil || cause == nil {
		return failure
	}
	values := append(PublicErrorRedactionValues(c), secrets...)
	var sdkService interface{ Service() string }
	if errors.As(cause, &sdkService) {
		values = append(values, common.UpstreamIdentityValues(sdkService.Service(), "", "", "")...)
	}
	detail := CapturedUpstreamError(c)
	if detail == nil {
		code, message, status := "", cause.Error(), 0
		var api interface {
			ErrorCode() string
			ErrorMessage() string
		}
		if errors.As(cause, &api) {
			code, message = api.ErrorCode(), api.ErrorMessage()
		}
		var response interface{ HTTPStatusCode() int }
		if errors.As(cause, &response) {
			status = response.HTTPStatusCode()
		}
		headers := make(http.Header)
		if c != nil {
			if value, ok := c.Get(sdkResponseMetadataKey); ok {
				if metadata, valid := value.(*sdkResponseMetadata); valid && metadata != nil && metadata.channelID == c.GetInt("channel_id") {
					if status == 0 {
						status = metadata.status
					}
					headers = metadata.headers.Clone()
				}
			}
		}
		var request interface{ ServiceRequestID() string }
		if errors.As(cause, &request) {
			headers.Set("X-Request-Id", request.ServiceRequestID())
		}
		var requestID interface{ RequestID() string }
		if headers.Get("X-Request-Id") == "" && errors.As(cause, &requestID) {
			headers.Set("X-Request-Id", requestID.RequestID())
		}
		raw, _ := common.Marshal(map[string]any{"error": map[string]any{"code": code, "message": message}})
		detail = common.NewPublicUpstreamError(status, raw, headers, values...)
	}
	failure.PublicRedactionValues = append(failure.PublicRedactionValues, values...)
	failure.UpstreamError = common.RedactPublicUpstreamError(detail, values...)
	return failure
}

// Some adapters forward error frames directly rather than returning a typed
// failure. Apply the public diagnostic policy at the final SSE write as well.
func SafeStreamErrorFrame(c *gin.Context, data string) string {
	frame := gjson.Parse(data)
	hasError := func(v gjson.Result) bool { return v.Exists() && v.Type != gjson.Null }
	if !hasError(frame.Get("error")) && !hasError(frame.Get("response.error")) && frame.Get("type").String() != "error" && frame.Get("type").String() != "response.failed" {
		return data
	}
	values := PublicErrorRedactionValues(c)
	detail := CapturedUpstreamError(c)
	if detail != nil {
		values = append(values, detail.RedactionValues()...)
	}
	safe := common.NewPublicUpstreamError(0, []byte(data), nil, values...)
	if _, ok := safe.Body.(map[string]any); !ok || safe.Truncated {
		encoded, _ := common.Marshal(map[string]any{"error": map[string]any{"code": "upstream_error", "message": "Stream diagnostic exceeded the public limit", "upstream_error": safe}})
		return string(encoded)
	}
	if object, ok := safe.Body.(map[string]any); ok {
		// Protocol event types are public schema, not a vendor error-code namespace.
		switch frame.Get("type").String() {
		case "response.failed", "response.error", "error":
			object["type"] = frame.Get("type").String()
		}
		target, _ := object["error"].(map[string]any)
		if response, ok := object["response"].(map[string]any); ok {
			target, _ = response["error"].(map[string]any)
		}
		if target != nil && target["upstream_error"] == nil && detail != nil {
			target["upstream_error"] = detail
		}
	}
	encoded, err := common.Marshal(safe.Body)
	if err != nil {
		return `{"error":{"code":"upstream_error","message":"Stream failed"}}`
	}
	return string(encoded)
}
