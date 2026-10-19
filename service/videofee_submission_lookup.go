package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const videoFeeSubmissionKeyPrefix = "apimaster-"

// Only the exact provider origin and the original credential are used. Redirects
// are refused so a lookup cannot leak the supplier key or invoke another route.
func lookupVideoFeeSubmission(ctx context.Context, row *model.VideoSubmission) (*model.VideoFeeSubmissionObservation, error) {
	var snapshot model.VideoSubmissionRecovery
	if row.ChannelID != 273 || !row.ProviderLookupEnabled || common.UnmarshalJsonStr(string(row.RecoveryJSON), &snapshot) != nil || snapshot.CredentialFingerprint == "" {
		return nil, errors.New("missing original lookup identity")
	}
	channel, err := model.GetChannelById(row.ChannelID, true)
	if err != nil {
		return nil, errors.New("lookup channel unavailable")
	}
	base, err := url.Parse(channel.GetBaseURL())
	if err != nil || base.Scheme != "https" || base.Host != "seedance2026.vip" || base.User != nil || (base.Path != "" && base.Path != "/") || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("lookup provider origin mismatch")
	}
	key := ""
	for _, candidate := range channel.GetKeys() {
		if model.VideoFeeCredentialFingerprint(candidate) == snapshot.CredentialFingerprint {
			key = strings.TrimSpace(candidate)
			break
		}
	}
	if key == "" {
		return nil, errors.New("original lookup credential unavailable")
	}
	client, err := GetHttpClientWithProxy(channel.GetSetting().Proxy)
	if err != nil || client == nil {
		return nil, errors.New("lookup transport unavailable")
	}
	return fetchVideoFeeSubmission(ctx, client, videoFeeSubmissionKeyPrefix+row.TaskID, key)
}

func fetchVideoFeeSubmission(ctx context.Context, client *http.Client, lookupKey, key string) (*model.VideoFeeSubmissionObservation, error) {
	copyClient := *client
	copyClient.Timeout = 8 * time.Second
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://seedance2026.vip/v1/submissions/by-key", nil)
	if err != nil {
		return nil, errors.New("invalid lookup request")
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Idempotency-Key", lookupKey)
	request.Header.Set("Cache-Control", "no-cache")
	response, err := copyClient.Do(request)
	if err != nil {
		return nil, errors.New("provider lookup transport failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("provider lookup unavailable")
	}
	const maxResponse = 128 * 1024
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil || len(raw) > maxResponse {
		return nil, errors.New("invalid provider lookup response")
	}
	var observation model.VideoFeeSubmissionObservation
	if common.Unmarshal(raw, &observation) != nil {
		return nil, errors.New("invalid provider lookup JSON")
	}
	if err := observation.Validate(lookupKey, time.Now()); err != nil {
		return nil, err
	}
	return &observation, nil
}

func ReconcileVideoFeeSubmissions() error {
	rows, err := model.ClaimVideoFeeLookups(time.Now().Unix(), 5)
	if err != nil {
		return err
	}
	var firstErr error
	for i := range rows {
		row := &rows[i]
		observation, lookupErr := lookupVideoFeeSubmission(context.Background(), row)
		var retryAfter *int
		if lookupErr == nil {
			retryAfter = observation.RetryAfter
			lookupErr = model.ApplyVideoFeeObservation(row, observation, videoFeeSubmissionKeyPrefix+row.TaskID)
		}
		if lookupErr != nil && firstErr == nil {
			firstErr = lookupErr
		}
		if e := model.ScheduleVideoFeeLookup(row, time.Now().Unix(), retryAfter); e != nil && firstErr == nil {
			firstErr = e
		}
	}
	return firstErr
}

// The public receipt distinguishes platform acceptance from generation state.
// Supplier prices, private task IDs, credentials and result URLs stay private.
func videoFeeReconciliationDTO(row *model.VideoSubmission) map[string]any {
	if row.ProviderObservation == "" {
		return nil
	}
	var o model.VideoFeeSubmissionObservation
	if common.UnmarshalJsonStr(string(row.ProviderObservation), &o) != nil {
		return nil
	}
	d := map[string]any{"acceptance_status": o.Acceptance, "observed_at": o.ObservedAt, "next_check_at": row.ProviderLookupNext, "customer_settlement_status": "requires_review", "independent_asset_fees_confirmed": false}
	if o.Task != nil {
		d["task_status"] = o.Task.Status
	}
	if row.ProviderRefundCompleted {
		d["customer_settlement_status"] = "refunded"
	}
	if row.RecoverySettled {
		d["customer_settlement_status"] = "original_reservation_committed"
	}
	return d
}
