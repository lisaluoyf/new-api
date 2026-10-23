package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTaskFailureSnapshotAndReplaySafety(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "rejected", true: "unconfirmed"}[uncertain], func(t *testing.T) {
			withSnapshotTestDB(t)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			body := `{"model":"seedance-2.5","duration":4,"seed":0,"generate_audio":false}`
			c.Request = httptest.NewRequest("POST", "/v1/video/generations", strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Request.Header.Set("Authorization", "Bearer private-token")
			c.Request.Header.Set("Idempotency-Key", "original-video-key")
			requestID := "video-" + t.Name()
			c.Set(common.RequestIdKey, requestID)
			c.Set("id", 1)
			c.Set("token_id", 2)
			c.Set("channel_id", 274)
			c.Set("original_model", "seedance-2.5")
			c.Set("use_channel", []string{"274"})
			status := 400
			if uncertain {
				status = 502
			}
			failure := &dto.TaskError{Code: "material_error", Message: "material rejected", StatusCode: status, ErrorLogRecorded: true}
			RecordPublicTaskErrorLog(c, failure)
			SaveFailedTaskRequestSnapshot(c, failure)
			snapshot, err := model.GetFailedRequestSnapshotByRequestId(requestID)
			require.NoError(t, err)
			require.JSONEq(t, body, snapshot.Body)
			require.Equal(t, 274, snapshot.LastChannelId)
			require.NotContains(t, snapshot.Headers, "private-token")
			require.Contains(t, snapshot.Headers, "Idempotency-Key")
			request, err := BuildReplayRequest(snapshot, 274, "operator-token")
			if uncertain {
				require.ErrorContains(t, err, "acceptance is unverified")
			} else {
				require.NoError(t, err)
				require.Equal(t, "original-video-key", request.Header.Get("Idempotency-Key"))
			}
			var count int64
			require.NoError(t, model.DB.Model(&model.FailedRequestSnapshot{}).Where("request_id = ?", requestID).Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}
