package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSeedanceSubmitTransportErrorDoesNotExposePrivateMedia(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "seedance-2.5")
	common.SetContextKey(c, constant.ContextKeyChannelKey, "private-credential")
	respondTaskError(c, &dto.TaskError{StatusCode: http.StatusInternalServerError, Code: "do_request_failed", Message: "Failed to fetch https://private.example/video.mp4 key=private-credential asset_id=private-asset", Error: errors.New("transport failure")})
	require.Equal(t, 500, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "private.example")
	require.NotContains(t, recorder.Body.String(), "private-credential")
	require.NotContains(t, recorder.Body.String(), "private-asset")
	require.Contains(t, recorder.Body.String(), "Failed to fetch")
}
