package controller

import (
	"github.com/QuantumNous/new-api/model"
	"net/http"
	"testing"
)

func TestVeoDownloadUsesStoredUpstreamURLWithoutCredentials(t *testing.T) {
	task := &model.Task{Platform: "apimart-veo", TaskID: "task_public", PrivateData: model.TaskPrivateData{
		ResultURL:        "https://apimaster.ai/v1/videos/task_public/content",
		UpstreamVideoURL: "https://cdn.example.com/signed-video.mp4",
	}}
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	got := resolveOpenAIVideoFetchURL(task, "https://api.apimart.ai", req, "private-upstream-key")
	if got != task.PrivateData.UpstreamVideoURL {
		t.Fatalf("unexpected source: %s", got)
	}
	if req.Header.Get("Authorization") != "" {
		t.Fatal("CDN request must not contain channel credentials")
	}
}
