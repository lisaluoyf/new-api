package helper

import (
	"bytes"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGrokInvalidParametersRemainBadRequest(t *testing.T) {
	for _, body := range []string{
		`{"model":"grok-imagine-image-2.0","prompt":"test","quality":"high"}`,
		`{"model":"grok-imagine-image-2.0","prompt":"test","image_urls":["https://example.test/a.png","https://example.test/a.png"]}`,
		`{"model":"grok-imagine-image-2.0","prompt":"test","n":0}`,
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/images/generations", bytes.NewBufferString(body))
		c.Request.Header.Set("Content-Type", "application/json")
		storage, err := common.CreateBodyStorage([]byte(body))
		require.NoError(t, err)
		c.Set(common.KeyBodyStorage, storage)
		_, err = GetAndValidateRequest(c, types.RelayFormatOpenAIImage)
		require.Error(t, err)
		apiError := types.NewError(err, types.ErrorCodeInvalidRequest)
		require.Equal(t, http.StatusBadRequest, apiError.StatusCode)
	}
}
