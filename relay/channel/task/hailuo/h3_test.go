package hailuo

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
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

func TestH3BuildRequestBodySupportsOfficialMultimodalContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("task_request", relaycommon.TaskSubmitReq{
		Model:   "MiniMax-H3",
		Prompt:  "A person walks through a neon city.",
		Webhook: "https://example.com/h3-callback",
		Metadata: map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "text", "text": "A person walks through a neon city."},
				map[string]interface{}{"type": "image_url", "role": "first_frame", "image_url": map[string]interface{}{"url": "https://example.com/first.png"}},
				map[string]interface{}{"type": "video_url", "role": "reference_video", "video_url": map[string]interface{}{"url": "https://example.com/reference.mp4"}},
				map[string]interface{}{"type": "audio_url", "role": "reference_audio", "audio_url": map[string]interface{}{"url": "https://example.com/reference.mp3"}},
			},
			"resolution":     "2K",
			"duration":       8,
			"ratio":          "21:9",
			"aigc_watermark": true,
		},
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
	for _, expected := range []string{
		`"type":"image_url"`, `"role":"first_frame"`, `"url":"https://example.com/first.png"`,
		`"type":"video_url"`, `"role":"reference_video"`, `"type":"audio_url"`,
		`"callback_url":"https://example.com/h3-callback"`, `"resolution":"2K"`, `"duration":8`, `"ratio":"21:9"`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("request body missing %s: %s", expected, text)
		}
	}
}

func TestH3EstimateBillingUsesDurationAndResolution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &H3TaskAdaptor{}
	for _, tc := range []struct {
		name     string
		duration int
		size     string
		wantSize float64
	}{
		{name: "768P", duration: 5, size: "768P", wantSize: 1},
		{name: "2K", duration: 10, size: "2K", wantSize: ratio_setting.GetVideoModelResolutionRatio("minimax-h3", "2K")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set("task_request", relaycommon.TaskSubmitReq{
				Model: "MiniMax-H3", Duration: tc.duration, Size: tc.size,
			})
			got := a.EstimateBilling(c, &relaycommon.RelayInfo{})
			if got["seconds"] != float64(tc.duration) || got["size"] != tc.wantSize {
				t.Fatalf("billing ratios = %#v, want seconds=%d size=%v", got, tc.duration, tc.wantSize)
			}
		})
	}
}

func TestH3AdjustBillingOnCompleteUsesActualOutputSeconds(t *testing.T) {
	a := &H3TaskAdaptor{}
	task := &model.Task{
		Quota: 1300,
		PrivateData: model.TaskPrivateData{
			BillingContext: &model.TaskBillingContext{
				OtherRatios: map[string]float64{
					"seconds": 10,
					"size":    ratio_setting.GetVideoModelResolutionRatio("minimax-h3", "2K"),
				},
			},
		},
	}
	result := &relaycommon.TaskInfo{BillableSeconds: 5}
	// 1300 / (10 × 1.625) × 5 × 1.625 = 650.
	if got := a.AdjustBillingOnComplete(task, result); got != 650 {
		t.Fatalf("final quota = %d, want 650", got)
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

func TestH3ParseTaskResultFallsBackToTotalSeconds(t *testing.T) {
	a := &H3TaskAdaptor{}
	result, err := a.ParseTaskResult([]byte(`{"task":{"id":"h3-task-2","status":"succeeded","usage":{"total_seconds":7},"content":{"url":"https://cdn.example/video.mp4"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.BillableSeconds != 7 {
		t.Fatalf("billable seconds = %d, want 7", result.BillableSeconds)
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
