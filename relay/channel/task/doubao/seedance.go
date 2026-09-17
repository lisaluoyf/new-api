package doubao

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

func isSeedanceAlias(name string) bool {
	return name == "doubao-seedance-2.0" || name == "seedance-2.5"
}

// Keep the public Seedance request contract when a channel uses the Doubao protocol.
func (a *TaskAdaptor) normalizeSeedanceRequest(c *gin.Context) *dto.TaskError {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil || !isSeedanceAlias(req.Model) {
		return nil
	}
	invalid := func(err error) *dto.TaskError {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if req.Metadata == nil {
		req.Metadata = make(map[string]interface{})
	}
	if strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
		var fields map[string]interface{}
		if err := common.UnmarshalBodyReusable(c, &fields); err != nil {
			return invalid(err)
		}
		for _, key := range []string{"content", "duration", "resolution", "ratio", "generate_audio", "watermark", "seed", "camera_fixed", "return_last_frame", "draft", "frames", "callback_url", "service_tier", "execution_expires_after", "tools"} {
			if value, ok := fields[key]; ok {
				req.Metadata[key] = value
			}
		}
		if value, ok := fields["aspect_ratio"]; ok {
			req.Metadata["ratio"] = value
		}
		if value, ok := fields["audio"]; ok {
			if _, explicit := fields["generate_audio"]; !explicit {
				req.Metadata["generate_audio"] = value
			}
		}
		for _, media := range []string{"image", "video", "audio"} {
			value, ok := fields[media+"_urls"]
			if !ok {
				continue
			}
			data, err := common.Marshal(value)
			if err != nil {
				return invalid(err)
			}
			var urls []string
			if err := common.Unmarshal(data, &urls); err != nil {
				return invalid(fmt.Errorf("%s_urls must be an array of strings", media))
			}
			content, _ := req.Metadata["content"].([]interface{})
			for _, url := range urls {
				content = append(content, map[string]interface{}{"type": media + "_url", media + "_url": map[string]string{"url": url}})
			}
			req.Metadata["content"] = content
		}
	}
	// Convert before charging so malformed parameters fail locally.
	body, err := a.convertToRequestPayload(&req)
	if err != nil {
		return invalid(err)
	}
	seconds := 5
	if body.Duration != nil {
		seconds = int(*body.Duration)
	}
	if seconds != -1 && (seconds < 4 || seconds > 30) {
		return invalid(fmt.Errorf("duration must be -1 or between 4 and 30 seconds"))
	}
	resolution := strings.ToLower(strings.TrimSpace(body.Resolution))
	if resolution == "" {
		resolution = "720p"
	}
	if resolution != "480p" && resolution != "720p" && resolution != "1080p" && !(req.Model == "doubao-seedance-2.0" && resolution == "4k") {
		return invalid(fmt.Errorf("unsupported Seedance resolution"))
	}
	req.Metadata["duration"] = seconds
	req.Metadata["resolution"] = resolution
	if body.Ratio == "" {
		req.Metadata["ratio"] = "16:9"
	}
	req.Duration = seconds
	req.Seconds = strconv.Itoa(seconds)
	c.Set("task_request", req)
	return nil
}

func seedanceBillingRatios(req relaycommon.TaskSubmitReq) map[string]float64 {
	seconds := req.Duration
	if seconds == -1 {
		seconds = 30
	}
	resolution, _ := req.Metadata["resolution"].(string)
	if hasVideoInMetadata(req.Metadata) {
		resolution += "-input"
	}
	ratio := taskcommon.VideoResolutionSizeRatio(resolution)
	if _, ok := ratio_setting.GetVideoModelBasePrice(req.Model); ok {
		ratio = ratio_setting.GetVideoModelResolutionRatio(req.Model, resolution)
	}
	return map[string]float64{"seconds": float64(seconds), "size": ratio}
}

func (a *TaskAdaptor) AdjustBillingOnComplete(task *model.Task, result *relaycommon.TaskInfo) int {
	if task == nil || result == nil || !isSeedanceAlias(task.Properties.OriginModelName) {
		return 0
	}
	bc := task.PrivateData.BillingContext
	if bc == nil || bc.OtherRatios["seconds"] <= 0 {
		return 0
	}
	seconds := result.BillableSeconds
	if seconds <= 0 && result.Url != "" {
		seconds, _ = service.ProbeRemoteVideoDurationSecondsRound(context.Background(), result.Url)
	}
	if seconds <= 0 {
		return 0
	}
	return int(math.Round(float64(task.Quota) * float64(seconds) / bc.OtherRatios["seconds"]))
}
