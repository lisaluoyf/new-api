package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"strconv"
)

// Persist the public video task ID before billing or contacting the provider.
// An uncertain outcome keeps its receipt and cannot cause another remote POST.
func BeginSeedancePortraitVideo(c *gin.Context, fields map[string]any, asset *model.SeedanceResource) (*model.SeedanceResource, bool, error) {
	key := c.GetHeader("Idempotency-Key")
	if len(key) < 8 || len(key) > 128 {
		return nil, false, seedanceError(400, "Idempotency-Key of 8–128 characters is required for real-person video generation")
	}
	raw, _ := common.Marshal(fields)
	id := "video_request_" + SeedanceKeyFingerprint(strconv.Itoa(c.GetInt("id")) + ":" + key)[:32]
	hash := SeedanceKeyFingerprint(string(raw))
	if existing, e := model.GetSeedanceResource(c.GetInt("id"), "video_request", id); e == nil {
		if existing.RequestData != hash {
			return nil, false, seedanceError(409, "Idempotency-Key was used for a different video request")
		}
		return existing, false, nil
	}
	receipt := &model.SeedanceResource{ID: id, Kind: "video_request", UserID: asset.UserID, ChannelID: asset.ChannelID, KeyFingerprint: asset.KeyFingerprint, ProjectName: asset.ProjectName, CredentialFingerprint: asset.CredentialFingerprint, Model: asset.Model, GroupID: asset.GroupID, RequestData: hash, UpstreamID: model.GenerateTaskID(), Status: "submission_unknown"}
	if e := seedanceDB().Create(receipt).Error; e != nil {
		existing, err := model.GetSeedanceResource(c.GetInt("id"), "video_request", id)
		if err == nil && existing.RequestData == hash {
			return existing, false, nil
		}
		return nil, false, e
	}
	return receipt, true, nil
}
func SeedanceVideoRequestDTO(r *model.SeedanceResource) map[string]any {
	return map[string]any{"request_id": r.ID, "task_id": r.UpstreamID, "status": r.Status, "message": "Query the original task; do not automatically submit a new paid request"}
}
