package service

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/tidwall/gjson"
)

// Only bounded, sanitized diagnostic fields are retained. Never attach the raw
// provider body: it can contain prompts, signed media URLs and credentials.
type BeeNexTaskQueryError struct {
	Protocol   string
	HTTPStatus int
	Kind       string
	Status     string
	Code       string
	RequestID  string
	RetryAfter int
}

func (e *BeeNexTaskQueryError) Error() string {
	return fmt.Sprintf("BeeNex task query failed: HTTP %d kind=%s protocol=%s status=%s code=%s request_id=%s", e.HTTPStatus, e.Kind, e.Protocol, e.Status, e.Code, e.RequestID)
}

func BeeNexTaskQueryFailureSummary(err error) string {
	var queryError *BeeNexTaskQueryError
	if errors.As(err, &queryError) {
		return queryError.Error()
	}
	return "query_or_state_update_failed"
}

func BeeNexTaskQueryRetryable(err error) bool {
	var failure *BeeNexTaskQueryError
	if !errors.As(err, &failure) {
		return false
	}
	if failure.HTTPStatus == 401 || failure.HTTPStatus == 403 || failure.HTTPStatus >= 300 && failure.HTTPStatus < 400 {
		return false
	}
	if failure.Kind == "identity_mismatch" || failure.Kind == "invalid_context" {
		return false
	}
	if failure.HTTPStatus >= 400 && failure.HTTPStatus < 500 && failure.HTTPStatus != 404 && failure.HTTPStatus != 408 && failure.HTTPStatus != 429 {
		return false
	}
	code := strings.ToLower(failure.Code)
	for _, word := range []string{"unauthor", "forbidden", "permission", "invalid_api_key", "authentication"} {
		if strings.Contains(code, word) {
			return false
		}
	}
	return true
}

var beeNexDiagnosticIdentifier = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

func safeBeeNexQueryField(value string, secrets ...string) string {
	if value == "" {
		return ""
	}
	if !beeNexDiagnosticIdentifier.MatchString(value) {
		return "[withheld]"
	}
	return common.SanitizeUpstreamErrorText(value, secrets...)
}

// DecodeBeeNexTaskResponse accepts both observed video envelopes. Unknown or
// missing statuses remain errors; they never turn into invented progress.
func DecodeBeeNexTaskResponse(raw []byte, secrets ...string) (gjson.Result, error) {
	failure := &BeeNexTaskQueryError{HTTPStatus: 200}
	if len(raw) > 4<<20 || !gjson.ValidBytes(raw) || !gjson.ParseBytes(raw).IsObject() {
		failure.Kind = "invalid_json"
		return gjson.Result{}, failure
	}
	root := gjson.ParseBytes(raw)
	data := root
	if nested := root.Get("data"); nested.IsObject() {
		data = nested
	}
	for _, path := range []string{"ResponseMetadata.RequestId", "request_id", "requestId"} {
		if value := root.Get(path).String(); value != "" {
			failure.RequestID = safeBeeNexQueryField(value, secrets...)
			break
		}
	}
	status := strings.ToUpper(strings.TrimSpace(data.Get("status").String()))
	failure.Status = safeBeeNexQueryField(data.Get("status").String(), secrets...)
	terminalFailure := status == "FAILED" || status == "FAILURE" || status == "CANCELED" || status == "CANCELLED"
	for _, path := range []string{"ResponseMetadata.Error", "error", "data.error", "Result.Error"} {
		detail := root.Get(path)
		if !detail.Exists() || detail.Type == gjson.Null || detail.Raw == "{}" || detail.String() == "" {
			continue
		}
		if (path == "error" || path == "data.error") && terminalFailure && (data.Get("task_id").String() != "" || data.Get("id").Type == gjson.String) {
			continue // Resource moderation/failure, not a rejected query.
		}
		failure.Kind = "upstream_error"
		failure.Code = safeBeeNexQueryField(detail.Get("code").String(), secrets...)
		if failure.Code == "" {
			failure.Code = safeBeeNexQueryField(detail.Get("Code").String(), secrets...)
		}
		return gjson.Result{}, failure
	}
	if code := root.Get("code"); code.Exists() && code.Type != gjson.Null {
		value := strings.ToLower(code.String())
		if value != "" && value != "success" && value != "ok" && value != "0" && value != "200" {
			failure.Kind, failure.Code = "upstream_error", safeBeeNexQueryField(code.String(), secrets...)
			return gjson.Result{}, failure
		}
	}
	switch status {
	case "QUEUED", "PENDING", "SUBMITTED", "NOT_START", "IN_PROGRESS", "PROCESSING", "RUNNING", "SUCCESS", "SUCCEEDED", "COMPLETED", "FAILURE", "FAILED", "CANCELLED", "CANCELED":
	default:
		failure.Kind = "unknown_status"
		if !data.Get("status").Exists() || strings.TrimSpace(data.Get("status").String()) == "" {
			failure.Kind = "missing_status"
		}
		return gjson.Result{}, failure
	}
	taskID, id := data.Get("task_id"), data.Get("id")
	if taskID.Type != gjson.String && id.Type != gjson.String || taskID.String() == "" && id.String() == "" {
		failure.Kind = "missing_task_id"
		return gjson.Result{}, failure
	}
	if taskID.Type == gjson.String && id.Type == gjson.String && taskID.String() != "" && id.String() != "" && taskID.String() != id.String() {
		failure.Kind = "identity_mismatch"
		return gjson.Result{}, failure
	}
	return data, nil
}

// Preserve the provider's longer rate-limit delay in the public polling hint.
func BeeNexTaskQueryRetryAfter(err error) int {
	var detail *BeeNexTaskQueryError
	if errors.As(err, &detail) && detail.RetryAfter > 2 {
		return detail.RetryAfter
	}
	return 2
}
