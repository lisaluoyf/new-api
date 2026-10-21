package service

import (
	"bytes"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

const upstreamErrorCaptureKey = "public_upstream_error_capture"

type upstreamErrorCapture struct {
	io.ReadCloser
	response  *http.Response
	body      bytes.Buffer
	channelID int
	secrets   []string
	truncated bool
}

func (r *upstreamErrorCapture) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	remaining := (2 << 20) - r.body.Len()
	if n > remaining {
		r.truncated = true
	}
	if remaining > n {
		remaining = n
	}
	if remaining > 0 {
		r.body.Write(p[:remaining])
	}
	return n, err
}

// Called at the shared HTTP boundary, so future adapters inherit capture without
// channel-specific registration. Reads, status, retries and closing are unchanged.
func ObserveUpstreamErrorResponse(c *gin.Context, resp *http.Response, secrets ...string) {
	if resp == nil || resp.StatusCode < 400 {
		return
	}
	ObserveTaskSubmissionResponse(c, resp, secrets...)
}

// Task responses are small non-streaming envelopes. Capture successful HTTP
// responses too: a provider may put an error or malformed payload in HTTP 200.
func ObserveTaskSubmissionResponse(c *gin.Context, resp *http.Response, secrets ...string) {
	if resp == nil || resp.Body == nil {
		return
	}
	if _, already := resp.Body.(*upstreamErrorCapture); already {
		return
	}
	capture := &upstreamErrorCapture{ReadCloser: resp.Body, response: resp, channelID: c.GetInt("channel_id"), secrets: secrets}
	resp.Body = capture
	c.Set(upstreamErrorCaptureKey, capture)
}
func CapturedUpstreamError(c *gin.Context) *common.PublicUpstreamError {
	value, ok := c.Get(upstreamErrorCaptureKey)
	capture, valid := value.(*upstreamErrorCapture)
	if !ok || !valid || capture == nil || capture.channelID != c.GetInt("channel_id") {
		return nil
	}
	if capture.truncated {
		return &common.PublicUpstreamError{StatusCode: capture.response.StatusCode, Body: "Upstream error response exceeds diagnostic limit", Truncated: true}
	}
	return common.PublicUpstreamErrorFromResponse(capture.response, capture.body.Bytes(), capture.secrets...)
}
func ResetUpstreamErrorCapture(c *gin.Context) {
	c.Set(upstreamErrorCaptureKey, (*upstreamErrorCapture)(nil))
}
func AttachCapturedUpstreamError(c *gin.Context, failure *types.NewAPIError) {
	if failure != nil && failure.UpstreamError == nil {
		failure.UpstreamError = CapturedUpstreamError(c)
		if failure.UpstreamError == nil && failure.RelayError != nil {
			raw, _ := common.Marshal(failure.RelayError)
			failure.UpstreamError = common.NewPublicUpstreamError(failure.StatusCode, raw, nil, common.GetContextKeyString(c, constant.ContextKeyChannelKey))
		}
	}
}
