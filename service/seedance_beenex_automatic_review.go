package service

import (
	"bytes"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

func isBeeNexAutomaticReviewModel(name string) bool {
	return name == "seedance-2.0" || name == "seedance-2.5"
}

func bindBeeNexAutomaticReviewContext(r *model.SeedanceResource) error {
	c, err := loadBeeNexAssetCredentials(274)
	if err != nil {
		return err
	}
	r.ProjectName, r.CredentialFingerprint = c.ProjectName, beeNexControlFingerprint(c)
	return nil
}
func beeNexAutomaticReviewSecrets() []string {
	c, err := loadBeeNexAssetCredentials(274)
	if err != nil {
		return nil
	}
	return []string{c.AccessKeyID, c.SecretAccessKey, c.SessionToken}
}

// An identifier at any response level makes acceptance ambiguous. Review must
// never trigger another chargeable POST in that case.
func beeNexReviewHasTaskID(raw []byte) bool {
	var body any
	if common.Unmarshal(raw, &body) != nil {
		return true
	}
	var hasID func(any) bool
	hasID = func(value any) bool {
		switch v := value.(type) {
		case map[string]any:
			for k, field := range v {
				switch k {
				case "id", "task_id", "taskId", "upstreamTaskId", "upstream_task_id":
					if field != nil && field != "" {
						return true
					}
				}
				if hasID(field) {
					return true
				}
			}
		case []any:
			for _, field := range v {
				if hasID(field) {
					return true
				}
			}
		}
		return false
	}
	return hasID(body)
}

// BeeNex's task gateway puts the Ark rejection JSON in message, while its
// outer code is fail_to_fetch_task and data is null. Unwrap only this observed
// task-less privacy rejection; all other envelopes retain their original body.
func NormalizeBeeNexAutomaticReviewResponse(resp *http.Response) (*http.Response, error) {
	if resp == nil || resp.Body == nil || resp.StatusCode != http.StatusBadRequest {
		return resp, nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	_ = resp.Body.Close()
	if err != nil || len(raw) > 4<<20 {
		return nil, seedanceError(502, "Invalid BeeNex submission response", "submission_unknown")
	}
	var outer struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Data    any    `json:"data"`
	}
	if common.Unmarshal(raw, &outer) == nil && outer.Code == "fail_to_fetch_task" && outer.Data == nil && !beeNexReviewHasTaskID(raw) {
		var inner struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		nested := []byte(outer.Message)
		if common.Unmarshal(nested, &inner) == nil && inner.Error.Code == "InputImageSensitiveContentDetected.PrivacyInformation" && !beeNexReviewHasTaskID(nested) {
			raw = nested
		}
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	resp.ContentLength = int64(len(raw))
	return resp, nil
}
