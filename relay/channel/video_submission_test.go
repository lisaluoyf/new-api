package channel_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

type observeSubmissionTransport struct {
	base   http.RoundTripper
	rewind *atomic.Bool
}

func (t observeSubmissionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.GetBody != nil {
		t.rewind.Store(true)
	}
	return t.base.RoundTrip(req)
}
func TestDurableVideoSubmissionDoesNotFollowPaidPostRedirects(t *testing.T) {
	for _, code := range []int{302, 307, 308} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			var posted, redirected atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if r.URL.Path == "/again" {
					redirected.Add(1)
					w.Write([]byte(`{"id":"duplicate"}`))
					return
				}
				posted.Add(1)
				w.Header().Set("Location", "/again")
				w.WriteHeader(code)
			}))
			defer server.Close()
			service.InitHttpClient()
			client := service.GetHttpClient()
			old := client.Transport
			defer func() { client.Transport = old }()
			var rewind atomic.Bool
			client.Transport = observeSubmissionTransport{base: old, rewind: &rewind}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/videos", strings.NewReader("{}"))
			c.Set(service.VideoSubmissionContextKey, &model.VideoSubmission{ID: "protected"})
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: server.URL, ApiKey: "fixture-key"}, TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_fixture"}}
			adapter := &doubao.TaskAdaptor{}
			adapter.Init(info)
			response, err := channel.DoTaskApiRequest(adapter, c, info, strings.NewReader(`{"model":"seedance-2.0","content":[]}`))
			require.NoError(t, err)
			defer response.Body.Close()
			require.Equal(t, code, response.StatusCode)
			require.EqualValues(t, 1, posted.Load())
			require.Zero(t, redirected.Load())
			require.False(t, rewind.Load())
		})
	}
}

// Cancel only after the provider has received the one original POST. Both
// response headers and body must remain readable to save its acceptance ID.
func TestDurableVideoSubmissionObservesOriginalResponseAfterClientDisconnect(t *testing.T) {
	service.InitHttpClient()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		cancel()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		time.Sleep(20 * time.Millisecond)
		_, _ = w.Write([]byte(`{"id":"original-upstream-task"}`))
	}))
	defer server.Close()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/videos", strings.NewReader("{}")).WithContext(ctx)
	c.Set(service.VideoSubmissionContextKey, &model.VideoSubmission{ID: "protected"})
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: server.URL, ApiKey: "fixture"}, TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_original"}}
	adapter := &doubao.TaskAdaptor{}
	adapter.Init(info)
	resp, err := channel.DoTaskApiRequest(adapter, c, info, strings.NewReader(`{"model":"seedance-2.0"}`))
	require.NoError(t, err)
	deadline, bounded := resp.Request.Context().Deadline()
	require.True(t, bounded)
	require.Positive(t, time.Until(deadline))
	require.LessOrEqual(t, time.Until(deadline), 90*time.Second)
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Contains(t, string(raw), "original-upstream-task")
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.EqualValues(t, 1, posts.Load())
	require.Error(t, resp.Request.Context().Err())
}

func TestDurableVideoSubmissionDoesNotStartAfterClientAlreadyGone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { posts.Add(1) }))
	defer server.Close()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/videos", nil).WithContext(ctx)
	c.Set(service.VideoSubmissionContextKey, &model.VideoSubmission{ID: "protected"})
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: server.URL}}
	adapter := &doubao.TaskAdaptor{}
	adapter.Init(info)
	_, err := channel.DoTaskApiRequest(adapter, c, info, strings.NewReader("{}"))
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, posts.Load())
}

// Model-level protection also covers legacy paths without a durable claim.
func TestSeedanceModelsObserveOriginalResponseWithoutClientIdempotencyKey(t *testing.T) {
	for _, name := range []string{"seedance-2.0", "seedance-2.0-fast", "seedance-2.0-mini", "seedance-2.5"} {
		t.Run(name, func(t *testing.T) {
			service.InitHttpClient()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				cancel()
				time.Sleep(10 * time.Millisecond)
				_, _ = w.Write([]byte(`{"id":"original"}`))
			}))
			defer server.Close()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/videos", strings.NewReader("{}")).WithContext(ctx)
			info := &relaycommon.RelayInfo{OriginModelName: name, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: server.URL, ApiKey: "fixture"}, TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_original"}}
			adaptor := &doubao.TaskAdaptor{}
			adaptor.Init(info)
			resp, err := channel.DoTaskApiRequest(adaptor, c, info, strings.NewReader(`{}`))
			require.NoError(t, err)
			raw, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			require.Contains(t, string(raw), "original")
			require.EqualValues(t, 1, posts.Load())
			require.ErrorIs(t, ctx.Err(), context.Canceled)
		})
	}
}
