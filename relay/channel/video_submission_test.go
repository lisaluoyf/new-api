package channel_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

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
