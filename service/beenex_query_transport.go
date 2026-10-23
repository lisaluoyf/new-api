package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// Every attempt is GET of the same accepted task at the same origin, using the
// same original key. Fallback is bounded and validates identity before returning
// any state to the normal CAS/billing path. No creation POST is ever retried.
func QueryBeeNexTask(ctx context.Context, client *http.Client, baseURL, key, id, protocol string, parse func([]byte) (*relaycommon.TaskInfo, error)) (*http.Response, error) {
	if client == nil || id == "" || strings.ContainsAny(id, "/\\?#\r\n ") || protocol != "sora" && protocol != "video_generations" {
		return nil, &BeeNexTaskQueryError{Kind: "invalid_context"}
	}
	caller := ctx
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	bounded := *client
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	alternate := "sora"
	if protocol == "sora" {
		alternate = "video_generations"
	}
	protocols := []string{protocol, alternate, protocol}
	var last error
	for attempt, current := range protocols {
		if ctx.Err() != nil {
			if caller.Err() != nil {
				return nil, caller.Err()
			}
			return nil, &BeeNexTaskQueryError{Kind: "deadline", Protocol: current}
		}
		if attempt > 0 {
			delay := time.Duration(attempt) * 250 * time.Millisecond
			var previous *BeeNexTaskQueryError
			if errors.As(last, &previous) && previous.HTTPStatus == 429 {
				if previous.RetryAfter > 2 {
					return nil, last // Honor a longer provider backoff at the caller.
				}
				delay = time.Second
				if previous.RetryAfter > 0 {
					delay = time.Duration(previous.RetryAfter) * time.Second
				}
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				if caller.Err() != nil {
					return nil, caller.Err()
				}
				return nil, &BeeNexTaskQueryError{Kind: "deadline", Protocol: current}
			case <-timer.C:
			}
		}
		path := "/v1/videos/"
		if current == "video_generations" {
			path = "/v1/video/generations/"
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+path+url.PathEscape(id), nil)
		if err != nil {
			return nil, &BeeNexTaskQueryError{Kind: "invalid_context"}
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Accept", "application/json")
		resp, err := bounded.Do(req)
		if err != nil {
			last = &BeeNexTaskQueryError{Kind: "transport", Protocol: current}
		} else {
			raw, readErr := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
			_ = resp.Body.Close()
			failure := &BeeNexTaskQueryError{Protocol: current, HTTPStatus: resp.StatusCode, RequestID: safeBeeNexQueryField(resp.Header.Get("X-Request-Id"), key)}
			switch {
			case readErr != nil || len(raw) > 4<<20:
				failure.Kind, last = "read_failed", failure
			case resp.StatusCode != http.StatusOK:
				failure.Kind, last = "http_error", failure
				failure.RetryAfter, _ = strconv.Atoi(resp.Header.Get("Retry-After"))
				if at, e := http.ParseTime(resp.Header.Get("Retry-After")); e == nil && time.Until(at) > 0 {
					failure.RetryAfter = int((time.Until(at) + time.Second - 1) / time.Second)
				}
			case !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "json"):
				failure.Kind, last = "content_type", failure
			default:
				result, parseErr := parse(raw)
				if parseErr == nil && (result == nil || result.TaskID != id) {
					parseErr = &BeeNexTaskQueryError{Kind: "identity_mismatch", HTTPStatus: 200}
				}
				if parseErr == nil {
					resp.Body = io.NopCloser(bytes.NewReader(raw))
					resp.Header.Set("X-BeeNex-Query-Protocol", current)
					resp.Header.Set("X-BeeNex-Query-Attempts", strconv.Itoa(attempt+1))
					return resp, nil
				}
				last = parseErr
				var detail *BeeNexTaskQueryError
				if errors.As(parseErr, &detail) {
					copy := *detail
					copy.Protocol = current
					if copy.RequestID == "" {
						copy.RequestID = failure.RequestID
					}
					last = &copy
				}
			}
		}
		logger.LogWarn(caller, "beenex_query_attempt_failed task="+safeBeeNexQueryField(id, key)+" "+BeeNexTaskQueryFailureSummary(last))
		if !BeeNexTaskQueryRetryable(last) {
			return nil, last
		}
	}
	return nil, last
}
