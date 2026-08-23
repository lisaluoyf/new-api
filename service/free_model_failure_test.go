package service

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestClassifyFreeModelFailure(t *testing.T) {
	tests := []struct {
		name      string
		err       *types.NewAPIError
		transient bool
		permanent bool
		reason    string
	}{
		{"removed model", types.NewOpenAIError(errors.New("This model is unavailable for free"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway), false, true, "upstream_model_unavailable"},
		{"empty output", types.NewOpenAIError(errors.New("empty"), types.ErrorCode("empty_response"), http.StatusBadGateway), true, false, "invalid_upstream_response"},
		{"rate limit", types.NewOpenAIError(errors.New("limited"), types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests), true, false, "temporary_upstream_failure"},
		{"bad request", types.NewOpenAIError(errors.New("bad request"), types.ErrorCodeInvalidRequest, http.StatusBadRequest), false, false, "non_retryable_request_error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ClassifyFreeModelFailure(test.err)
			require.Equal(t, test.transient, got.Transient)
			require.Equal(t, test.permanent, got.Permanent)
			require.Equal(t, test.reason, got.Reason)
		})
	}
}

func TestRecordFreeModelPermanentFailureQuarantinesChannel(t *testing.T) {
	resetFreeModelHealthForTest()
	now := time.Unix(1787356800, 0)
	oldNow := freeModelHealthNow
	freeModelHealthNow = func() time.Time { return now }
	t.Cleanup(func() {
		freeModelHealthNow = oldNow
		resetFreeModelHealthForTest()
	})

	health := RecordFreeModelFailureDisposition(172, http.StatusBadGateway, FreeModelFailureDisposition{Permanent: true, Reason: "upstream_model_unavailable"})
	require.Equal(t, "upstream_model_unavailable", health.LastFailureReason)
	require.Equal(t, now.Add(freeModelQuarantine).UnixMilli(), health.QuarantineUntil)
	require.True(t, health.IsAvoided(now))
}
