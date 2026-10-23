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
	client, key, err := videoFeeOriginalClient(row)
	if err != nil {
		return nil, err
	}
	return fetchVideoFeeSubmission(ctx, client, videoFeeSubmissionKeyPrefix+row.TaskID, key)
}

func videoFeeOriginalClient(row *model.VideoSubmission) (*http.Client, string, error) {
	var snapshot model.VideoSubmissionRecovery
	if row.ChannelID != 273 || !row.ProviderLookupEnabled || common.UnmarshalJsonStr(string(row.RecoveryJSON), &snapshot) != nil || snapshot.CredentialFingerprint == "" {
		return nil, "", errors.New("missing original lookup identity")
	}
	channel, err := model.GetChannelById(row.ChannelID, true)
	if err != nil {
		return nil, "", errors.New("lookup channel unavailable")
	}
	base, err := url.Parse(channel.GetBaseURL())
	if err != nil || base.Scheme != "https" || base.Host != "seedance2026.vip" || base.User != nil || (base.Path != "" && base.Path != "/") || base.RawQuery != "" || base.Fragment != "" {
		return nil, "", errors.New("lookup provider origin mismatch")
	}
	key := ""
	for _, candidate := range channel.GetKeys() {
		if model.VideoFeeCredentialFingerprint(candidate) == snapshot.CredentialFingerprint {
			key = strings.TrimSpace(candidate)
			break
		}
	}
	if key == "" {
		return nil, "", errors.New("original lookup credential unavailable")
	}
	client, err := GetHttpClientWithProxy(channel.GetSetting().Proxy)
	if err != nil || client == nil {
		return nil, "", errors.New("lookup transport unavailable")
	}
	return client, key, nil
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
	if observation.Task != nil {
		private := []string{key}
		if observation.TaskID != nil {
			private = append(private, *observation.TaskID)
		}
		if observation.Task.ErrorCode != "" {
			observation.Task.ErrorCode = common.PublicDiagnosticCode(observation.Task.ErrorCode, private...)
		}
		observation.Task.ErrorMessage = common.SanitizeUpstreamErrorText(model.SanitizeTaskFailure(observation.Task.ErrorMessage, private...), private...)
	}
	if observation.Error != nil {
		observation.Error.Code = common.PublicDiagnosticCode(observation.Error.Code, key)
		observation.Error.Message = common.SanitizeUpstreamErrorText(model.SanitizeTaskFailure(observation.Error.Message, key), key)
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
			if lookupErr == nil && observation.Acceptance == "accepted" && observation.Task != nil && (observation.Task.Status == "queued" || observation.Task.Status == "processing") {
				// This is the original task's existing polling/settlement endpoint,
				// not the read-only discovery API. Never call it for an ambiguous
				// submission, and never POST or create a replacement/reservation.
				lookupErr = pollOriginalVideoFeeTask(context.Background(), row, *observation.TaskID)
				if lookupErr == nil {
					row.ProviderLookupAttempts = 1
				}
			}
		}
		code := "provider_observation_recorded"
		if lookupErr != nil {
			code = "provider_reconciliation_unavailable"
		}
		// Store only allowlisted diagnostics, never raw provider errors/credentials.
		if e := model.RecordVideoFeeLookupOutcome(row, time.Now().Unix(), code); e != nil && firstErr == nil {
			firstErr = e
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

func pollOriginalVideoFeeTask(ctx context.Context, row *model.VideoSubmission, taskID string) error {
	client, key, err := videoFeeOriginalClient(row)
	if err != nil {
		return err
	}
	copyClient := *client
	copyClient.Timeout = 8 * time.Second
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://seedance2026.vip/v1/videos/"+taskID, nil)
	if err != nil {
		return errors.New("invalid original task query")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := copyClient.Do(req)
	if err != nil {
		return errors.New("original task query unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("original task query unavailable")
	}
	// The next by-key SQL snapshot supplies authoritative state and ledger facts.
	_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 128*1024))
	if err != nil {
		return errors.New("original task query interrupted")
	}
	return nil
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
	d := map[string]any{"acceptance_status": o.Acceptance, "observed_at": o.ObservedAt, "next_check_at": row.ProviderLookupNext, "customer_settlement_status": "requires_review", "independent_asset_fees_confirmed": false, "generation_status": o.GenerationStatus(), "supplier_billing_final": false}
	if o.Reason != "" {
		d["reason"] = common.PublicDiagnosticCode(o.Reason)
	}
	if o.Task != nil {
		d["task_status"] = o.Task.Status
		if o.GenerationStatus() == "failed" {
			private := []string{row.TaskID, row.RequestID}
			if o.TaskID != nil {
				private = append(private, *o.TaskID)
			}
			d["task_error"] = map[string]any{"code": common.PublicDiagnosticCode(o.Task.ErrorCode, private...), "message": common.SanitizeUpstreamErrorText(model.SanitizeTaskFailure(o.Task.ErrorMessage, private...), private...)}
		}
	}
	if o.Error != nil {
		d["submission_error"] = map[string]any{"code": common.PublicDiagnosticCode(o.Error.Code), "message": common.SanitizeUpstreamErrorText(model.SanitizeTaskFailure(o.Error.Message))}
	}
	if row.RecoverySettled {
		d["customer_settlement_status"] = "original_reservation_committed"
	}
	if row.ProviderRefundCompleted {
		d["customer_settlement_status"] = "refunded"
	}
	if row.ProviderLookupDone {
		d["next_check_at"] = nil
	}
	return d
}
