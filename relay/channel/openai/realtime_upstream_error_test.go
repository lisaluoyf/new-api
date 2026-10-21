package openai

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRealtimeWebsocketErrorDirectionAndRecovery(t *testing.T) {
	for _, code := range []int{websocket.CloseNormalClosure, websocket.CloseInternalServerErr} {
		t.Run(strconv.Itoa(code), func(t *testing.T) { testRealtimeWebsocketErrorDirectionAndRecovery(t, code) })
	}
}
func testRealtimeWebsocketErrorDirectionAndRecovery(t *testing.T, closeCode int) {
	// Exercise real sockets through the adapter, including a nested response.done
	// error followed by a normal session event on the same connection.
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	targetCh := make(chan *websocket.Conn, 1)
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			targetCh <- conn
		}
	}))
	defer targetServer.Close()
	targetConn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(targetServer.URL, "http"), nil)
	require.NoError(t, err)
	defer targetConn.Close()
	target := <-targetCh
	defer target.Close()
	done := make(chan struct{})
	relayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer client.Close()
		c, _ := gin.CreateTestContext(w)
		c.Request = r
		c.Set("channel_name", "HiddenRelay")
		c.Set("base_url", "https://private-upstream.example")
		OpenaiRealtimeHandler(c, &relaycommon.RelayInfo{ClientWs: client, TargetWs: targetConn, IsFirstRequest: true, ChannelMeta: &relaycommon.ChannelMeta{}})
		close(done)
	}))
	defer relayServer.Close()
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(relayServer.URL, "http"), nil)
	require.NoError(t, err)
	defer client.Close()
	require.NoError(t, client.SetReadDeadline(time.Now().Add(10*time.Second)))
	require.NoError(t, target.SetReadDeadline(time.Now().Add(10*time.Second)))
	// Client messages are untouched, even if they contain an error-shaped object.
	request := `{"type":"custom.client.event","error":{"message":"HiddenRelay https://private-upstream.example user-supplied content"}}`
	require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte(request)))
	_, received, err := target.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, request, string(received))
	for _, frame := range []string{
		`{"type":"error","event_id":"evt_1","error":{"code":"HiddenRelay.Overloaded","message":"HiddenRelay capacity exhausted at https://private-upstream.example"}}`,
		`{"type":"response.done","response":{"status":"failed","status_details":{"error":{"message":"HiddenRelay invalid audio format"}},"usage":null,"output":[]}}`,
	} {
		require.NoError(t, target.WriteMessage(websocket.TextMessage, []byte(frame)))
		_, received, err = client.ReadMessage()
		require.NoError(t, err)
		require.NotContains(t, string(received), "HiddenRelay")
		require.NotContains(t, string(received), "private-upstream.example")
		require.Contains(t, string(received), "upstream_error")
		require.Equal(t, gjson.Get(frame, "type").String(), gjson.GetBytes(received, "type").String())
	}
	good := `{"type":"session.updated","session":{"input_audio_format":"pcm16","instructions":"HiddenRelay is user content"}}`
	require.NoError(t, target.WriteMessage(websocket.TextMessage, []byte(good)))
	_, received, err = client.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, good, string(received))
	require.NoError(t, target.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(closeCode, "HiddenRelay capacity exhausted at https://private-upstream.example")))
	if closeCode != websocket.CloseNormalClosure {
		_, received, err = client.ReadMessage()
		require.NoError(t, err)
		require.Contains(t, string(received), "capacity exhausted")
		require.Contains(t, string(received), "upstream_error")
		require.NotContains(t, string(received), "HiddenRelay")
		require.NotContains(t, string(received), "private-upstream.example")
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("relay did not finish after upstream close")
	}
}
