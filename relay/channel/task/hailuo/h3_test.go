package hailuo

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

func TestH3BuildRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("task_request", relaycommon.TaskSubmitReq{
		Model:    "MiniMax-H3",
		Prompt:   "A red paper boat on a calm lake.",
		Duration: 4,
		Size:     "768P",
	})

	a := &H3TaskAdaptor{}
	reader, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, expected := range []string{`"model":"MiniMax-H3"`, `"type":"text"`, `"duration":4`, `"resolution":"768P"`, `"ratio":"16:9"`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("request body missing %s: %s", expected, text)
		}
	}
}

func TestH3ParseTaskResult(t *testing.T) {
	a := &H3TaskAdaptor{}
	result, err := a.ParseTaskResult([]byte(`{
      "task": {
        "id": "h3-task-1",
        "model": "MiniMax-H3",
        "status": "succeeded",
        "content": {"url": "https://cdn.example/video.mp4"},
        "usage": {"output_seconds": 4}
      }
    }`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != model.TaskStatusSuccess || result.Url != "https://cdn.example/video.mp4" || result.BillableSeconds != 4 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestH3ParseTaskResultRunning(t *testing.T) {
	a := &H3TaskAdaptor{}
	result, err := a.ParseTaskResult([]byte(`{"task":{"id":"h3-task-1","status":"running"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != model.TaskStatusInProgress {
		t.Fatalf("expected in-progress, got %+v", result)
	}
}
