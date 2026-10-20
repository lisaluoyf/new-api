package channel_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/doubao"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestVideoSubmissionAdapterPreservesOwnedExecutionDeadline(t *testing.T) {
	service.InitHttpClient()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte(`{"id":"original"}`))
	}))
	defer server.Close()
	parent, disconnect := context.WithCancel(context.Background())
	defer disconnect()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/videos", nil).WithContext(parent)
	c.Set(service.VideoSubmissionContextKey, &model.VideoSubmission{ID: "owned"})
	finish := service.BeginVideoSubmissionExecution(c)
	defer finish()
	deadline := time.Now().Add(10 * time.Second)
	execution, stop := context.WithDeadline(c.Request.Context(), deadline)
	defer stop()
	c.Request = c.Request.WithContext(execution)
	disconnect() // after ownership, before any upstream send
	info := &relaycommon.RelayInfo{OriginModelName: "seedance-2.5", ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: server.URL}, TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_owned"}}
	adapter := &doubao.TaskAdaptor{}
	adapter.Init(info)
	resp, err := channel.DoTaskApiRequest(adapter, c, info, strings.NewReader(`{"model":"seedance-2.5"}`))
	require.NoError(t, err)
	got, ok := resp.Request.Context().Deadline()
	require.True(t, ok)
	require.True(t, got.Equal(deadline), "adapter must not reset the shared execution deadline")
	require.NoError(t, resp.Body.Close())
	stop() // owned execution cancellation must still stop subsequent operations
	_, err = channel.DoTaskApiRequest(adapter, c, info, strings.NewReader(`{}`))
	require.ErrorIs(t, err, context.Canceled)
}
