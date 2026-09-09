package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestImageTaskMultiURLPolling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/tasks/example", r.URL.Path)
		require.Equal(t, "Bearer test", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"data":{"status":"completed","result":{"images":[{"url":["https://example.com/1.png","https://example.com/2.png"]},{"url":"https://example.com/3.png"}]}}}`))
	}))
	defer server.Close()
	want := []string{"https://example.com/1.png", "https://example.com/2.png", "https://example.com/3.png"}
	targets := []ImageTaskTarget{{ChannelID: 59, BaseURL: server.URL, APIKey: "test", TaskID: "example"}}
	winner, first, ok := RaceImageTask(targets, time.Now().Add(time.Second))
	require.True(t, ok)
	require.Equal(t, want[0], first)
	require.Equal(t, want, winner.ImageURLs)
	winner, status, first, _, _ := CheckImageTaskTargetsOnce(targets)
	require.Equal(t, "completed", status)
	require.Equal(t, want[0], first)
	require.Equal(t, want, winner.ImageURLs)
}

func TestImageTaskURLShapes(t *testing.T) {
	require.Equal(t, []string{"flat"}, extractImageTaskURLs("flat", nil))
	require.Nil(t, extractImageTaskURLs("", nil))
	require.Equal(t, []string{"a", "b"}, extractImageTaskURLs("", []imageTaskPollImage{{URL: []any{nil, "", "a"}}, {URL: "b"}}))
}
