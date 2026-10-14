package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"strings"
)

// HTTP failures and malformed success envelopes are not acceptance proofs.
func SeedanceExplicitRateRejection(status int, body []byte) bool {
	if status != 429 || !gjson.ValidBytes(body) {
		return false
	}
	for _, path := range []string{"task_id", "id", "data.task_id", "data.id", "Result.Id"} {
		if gjson.GetBytes(body, path).Exists() {
			return false
		}
	}
	text := strings.ToLower(gjson.GetBytes(body, "error").String() + " " + gjson.GetBytes(body, "message").String())
	return strings.Contains(text, "rate") || strings.Contains(text, "too many") || strings.Contains(text, "flow control") || strings.Contains(text, "throttl")
}
func ResetRejectedUnifiedSubmission(c *gin.Context, channel int) error {
	record := VideoSubmissionFromContext(c)
	if record == nil {
		return model.ErrVideoSubmissionClaimLost
	}
	if !c.GetBool("seedance_confirmed_not_accepted") {
		return model.ErrVideoSubmissionClaimLost
	}
	if err := model.RetryRejectedVideoSubmission(record.ID, record.OwnerToken, channel); err != nil {
		return err
	}
	c.Set(VideoSubmissionSentContextKey, false)
	c.Set("seedance_confirmed_not_accepted", false)
	fields, _ := c.Get("seedance_unified_fields")
	if _, err := chooseUnifiedSeedanceRoute(c, fields.(map[string]any), bannedChannelIDsFromContext(c)); err != nil {
		if e := model.FinishVideoSubmission(record.ID, model.VideoSubmissionNotAccepted, "", false); e != nil {
			return e
		}
		return err
	}
	return nil
}
func BindUnifiedSeedanceKey(c *gin.Context, channel *model.Channel) error {
	fingerprint := c.GetString("seedance_asset_key_fingerprint")
	key, index, err := SeedanceResourceKey(channel, fingerprint)
	if err != nil {
		return err
	}
	common.SetContextKey(c, constant.ContextKeyChannelKey, key)
	common.SetContextKey(c, constant.ContextKeyChannelMultiKeyIndex, index)
	return nil
}
