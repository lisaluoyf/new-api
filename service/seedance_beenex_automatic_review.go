package service

import (
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
