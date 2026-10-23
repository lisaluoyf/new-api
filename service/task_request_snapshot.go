package service

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func SaveFailedTaskRequestSnapshot(c *gin.Context, failure *dto.TaskError) {
	if c == nil || failure == nil || c.Request == nil || c.Request.URL == nil || c.Request.Method != http.MethodPost || c.GetInt("id") <= 0 || model.DB == nil {
		return
	}
	path := c.Request.URL.Path
	if path != "/v1/videos" && path != "/v1/video/generations" && path != "/v1/videos/generations" && !(strings.HasPrefix(path, "/v1/videos/") && strings.HasSuffix(path, "/remix")) {
		return
	}
	if !strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
		return
	}
	public := PublicTaskErrorView(c, failure)
	message := public.Message
	if public.UpstreamError != nil {
		raw, _ := common.Marshal(public.UpstreamError)
		message += " | upstream_error: " + string(raw)
	}
	uncertain := failure.StatusCode >= 500 || failure.StatusCode <= 0 || failure.Code == "submission_unknown"
	if public.UpstreamError != nil {
		raw, _ := common.Marshal(public.UpstreamError.Body)
		uncertain = uncertain || beeNexReviewHasTaskID(raw)
	}
	if record := VideoSubmissionFromContext(c); record != nil {
		uncertain = uncertain || record.Status == model.VideoSubmissionAccepted || record.Status == model.VideoSubmissionUnknown
	}
	if c.GetBool("xingchi_automatic_review_rejected") {
		uncertain = false // No reviewed video POST occurred.
	}
	decision, _ := common.Marshal(map[string]any{"upstream_acceptance_unverified": uncertain, "automatic_replay_allowed": !uncertain})
	info := &relaycommon.RelayInfo{UserId: c.GetInt("id"), TokenId: c.GetInt("token_id"), OriginModelName: c.GetString("original_model"), RequestURLPath: path, RelayFormat: types.RelayFormatTask}
	SaveRequestSnapshot(c, info, RequestSnapshotOptions{SnapshotType: model.FailedRequestSnapshotTypeFinalFailed, ErrorCode: public.Code, ErrorType: "task_error", StatusCode: public.StatusCode, ErrorMessage: message, RetryDecisionJSON: string(decision), RelayFormat: types.RelayFormatTask})
}
