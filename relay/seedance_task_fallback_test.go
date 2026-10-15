package relay

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSeedanceFallbackUsesTargetAdaptorMappingAndStableID(t *testing.T) {
	service.InitHttpClient()
	sends := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends++
		require.Equal(t, "/v1/video/generations", r.URL.Path)
		require.Equal(t, "Bearer backup-key", r.Header.Get("Authorization"))
		require.Equal(t, "apimaster-task-stable", r.Header.Get("Idempotency-Key"))
		var fields map[string]any
		require.NoError(t, common.DecodeJson(r.Body, &fields))
		require.Equal(t, "target-seedance-model", fields["model"])
		require.Equal(t, false, fields["generate_audio"])
		require.Equal(t, float64(0), fields["seed"])
		fmt.Fprint(w, `{"id":"backup-provider-id"}`)
	}))
	defer server.Close()
	mapping := `{"seedance-2.5":"target-seedance-model"}`
	ch := &model.Channel{Id: 273, Type: constant.ChannelTypeDoubaoVideo, Key: "backup-key", BaseURL: &server.URL, ModelMapping: &mapping}
	task := &model.Task{TaskID: "task-stable", UserId: 1, Properties: model.Properties{OriginModelName: "seedance-2.5"}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/videos/generations", strings.NewReader(`{"model":"seedance-2.5","prompt":"A paper kite","duration":4,"resolution":"480p","generate_audio":false,"seed":0}`))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "seedance-2.5")
	send, err := prepareSeedanceFallbackSubmit(c, task, ch, "backup-key")
	require.NoError(t, err)
	require.Zero(t, sends)
	result, err := send()
	require.NoError(t, err)
	require.Equal(t, 1, sends)
	require.Equal(t, "backup-provider-id", result.UpstreamID)
	require.Equal(t, "target-seedance-model", result.UpstreamModel)
	require.Equal(t, constant.TaskPlatform(constant.TaskPlatformVideoFee), result.Platform)
}
