package controller

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestImage25FDVisibleToAdminsButHiddenFromMarketplace(t *testing.T) {
	for _, name := range []string{"gpt-image-2.5-flare-fd", "gpt-image-2.5-sunburst-fd", " GPT-IMAGE-2.5-FLARE-FD "} {
		t.Run(name, func(t *testing.T) {
			require.False(t, isHiddenChannelDataModel(name))
			require.True(t, isHiddenMarketplaceModel(name))
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/public/marketplace?model="+url.QueryEscape(name), nil)
			GetPublicMarketplace(ctx)
			require.Equal(t, http.StatusOK, recorder.Code)
			var response struct {
				Success bool  `json:"success"`
				Data    []any `json:"data"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			require.True(t, response.Success)
			require.Empty(t, response.Data)
		})
	}
	for _, name := range []string{"gpt-image-2", "gpt-image-2-fd", "gpt-image-2.5-flare", "gpt-image-2.5-sunburst"} {
		require.False(t, isHiddenMarketplaceModel(name), name)
	}
}

func TestIsHiddenChannelDataModel(t *testing.T) {
	if !isHiddenChannelDataModel(" GPT-5.4 ") {
		t.Fatal("gpt-5.4 should be hidden from channel data and marketplace")
	}
	if !isHiddenChannelDataModel(" GPT-5.4-MINI ") {
		t.Fatal("gpt-5.4-mini should be hidden from channel data and marketplace")
	}
	if isHiddenChannelDataModel("gpt-5.4-nano") {
		t.Fatal("gpt-5.4-nano should remain visible")
	}
	if !isHiddenChannelDataModel(" gemini-3.1-flash-lite ") {
		t.Fatal("gemini-3.1-flash-lite should be hidden from channel data and marketplace")
	}
	if !isHiddenChannelDataModel(" KIMI-K2.5 ") {
		t.Fatal("kimi-k2.5 should be hidden from channel data and marketplace")
	}
	if !isHiddenChannelDataModel(" GEMINI-3.5-FLASH ") {
		t.Fatal("gemini-3.5-flash should be hidden from channel data and marketplace")
	}
	if isHiddenChannelDataModel("gemini-3.6-flash") {
		t.Fatal("gemini-3.6-flash should remain visible")
	}
	for _, name := range []string{" sora-2 ", " SORA-2-PRO "} {
		if !isHiddenChannelDataModel(name) {
			t.Fatalf("%q should be hidden from channel data and marketplace", name)
		}
	}
}
