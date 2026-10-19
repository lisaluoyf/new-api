package middleware

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDurableSubmissionReplaysSanitizedLocalFailureWithoutRepeating(t *testing.T) {
	calls := 0
	r := durableSubmissionRouter(t, func(c *gin.Context) {
		calls++
		c.JSON(400, gin.H{"error": gin.H{"code": "invalid_reference_image", "message": "unreachable https://private.example/a.png Authorization: Bearer secret"}})
	})
	body := `{"model":"seedance-2.5","prompt":"scene"}`
	require.Equal(t, 400, postSubmission(r, "/v1/videos", "local-failure-evidence", body).Code)
	var row model.VideoSubmission
	require.NoError(t, model.DB.First(&row).Error)
	require.Equal(t, "invalid_reference_image", row.ErrorCode)
	require.Equal(t, 400, row.ErrorStatus)
	require.NotContains(t, row.ErrorMessage, "secret")
	require.NotContains(t, row.ErrorMessage, "private.example")
	replay := postSubmission(r, "/v1/videos", "local-failure-evidence", body)
	require.Equal(t, 202, replay.Code)
	require.Contains(t, replay.Body.String(), `"code":"invalid_reference_image"`)
	require.Contains(t, replay.Body.String(), `"status":"not_accepted"`)
	require.Equal(t, 1, calls)
}
