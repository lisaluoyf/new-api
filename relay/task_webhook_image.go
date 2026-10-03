package relay

import (
	"bytes"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"net/http"
	"time"
)

// Capture adapter output so synchronous providers cannot bypass task persistence.
type taskWebhookImageWriter struct {
	gin.ResponseWriter
	body bytes.Buffer
	code int
}

func (w *taskWebhookImageWriter) WriteHeader(code int) { w.code = code }
func (w *taskWebhookImageWriter) WriteHeaderNow()      {}
func (w *taskWebhookImageWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = 200
	}
	return w.body.Write(b)
}
func (w *taskWebhookImageWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *taskWebhookImageWriter) Status() int {
	if w.code == 0 {
		return 200
	}
	return w.code
}
func (w *taskWebhookImageWriter) Size() int     { return w.body.Len() }
func (w *taskWebhookImageWriter) Written() bool { return w.code != 0 }
func (w *taskWebhookImageWriter) Flush()        {}

func persistTaskWebhookImageResponse(c *gin.Context, info *relaycommon.RelayInfo, raw []byte) ([]byte, error) {
	config := service.TaskWebhookConfigFromContext(c)
	if config == nil {
		return raw, nil
	}
	// OpenAI adapters already create durable task mappings and should keep that ID.
	if id := c.GetString("image_poll_task_id"); id != "" {
		return raw, nil
	}
	urls := service.ExtractImageURLsFromResponse(raw)
	if len(urls) == 0 {
		return nil, fmt.Errorf("image response has no durable task or image outputs")
	}
	for i, u := range urls {
		urls[i] = service.CacheImageLocallyWithHeaders(u, map[string]string{"Authorization": "Bearer " + common.GetContextKeyString(c, constant.ContextKeyChannelKey)})
	}
	now := time.Now().Unix()
	task := model.Task{TaskID: model.GenerateTaskID(), Platform: constant.TaskPlatformOpenAIImage, UserId: info.UserId, Group: info.UsingGroup, ChannelId: info.ChannelId, Status: model.TaskStatusSuccess, Progress: "100%", SubmitTime: now, StartTime: now, FinishTime: now, Properties: model.Properties{OriginModelName: info.OriginModelName, UpstreamModelName: info.UpstreamModelName}, PrivateData: model.TaskPrivateData{Webhook: config, ResultURL: urls[0], ImageResultURLs: urls}}
	if request := service.ImageRequestDataFromContext(c); len(request) > 0 {
		encoded, err := common.Marshal(request)
		if err != nil {
			return nil, err
		}
		task.PrivateData.RequestData = string(encoded)
	}
	if err := task.Insert(); err != nil {
		return nil, err
	}
	c.Set("image_poll_task_id", task.TaskID)
	return common.Marshal(gin.H{"created": now, "data": []gin.H{{"task_id": task.TaskID, "status": "completed", "progress": 100, "result": gin.H{"url": urls[0], "urls": urls}}}})
}

var _ http.Flusher = (*taskWebhookImageWriter)(nil)
