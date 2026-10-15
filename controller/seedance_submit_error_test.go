package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/service"
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

func TestSeedanceSubmitErrorEnvelopeAndLegacyFields(t *testing.T) {
	for _, name := range []string{"seedance-2.0", "seedance-2.5", "seedance-2.0-fast", "seedance-2.0-mini", "doubao-seedance-2.5"} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		common.SetContextKey(c, constant.ContextKeyOriginalModel, name)
		err := service.PublicVideoSubmitError(400, []byte(`{"error":{"code":"InputImageSensitiveContentDetected","message":"The input image may contain sensitive information. Please replace it and try again."}}`), "", 0)
		respondTaskError(c, err)
		var body struct {
			Code, Message string
			Error         struct{ Code, Message string }
		}
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &body))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Equal(t, "InputImageSensitiveContentDetected", body.Error.Code)
		require.Equal(t, body.Code, body.Error.Code)
		require.Equal(t, body.Message, body.Error.Message)
	}
}
