package controller

import (
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestXingchiAutomaticReviewPreventsFurtherChannelRetries(t *testing.T) {
	for _, status := range []int{429, 500, 502, 504} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set("xingchi_automatic_review_attempted", true)
		require.False(t, shouldRetryTaskRelay(c, 292, &dto.TaskError{StatusCode: status, Code: "video_service_error"}, 3))
	}
}
