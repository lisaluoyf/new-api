package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"strings"
)

type videoFeeCertification struct {
	InterfaceCode string `json:"interface_code,omitempty"`
	Status        string `json:"status"`
}
type videoFeeReview struct {
	Purpose        string                  `json:"purpose"`
	Status         string                  `json:"status"`
	Certifications []videoFeeCertification `json:"certifications,omitempty"`
}

func videoFeeStatus(value string) string {
	switch strings.ToLower(value) {
	case "active", "failed", "blocked", "rejected", "certifying", "pending", "uploaded", "processing":
		return strings.ToLower(value)
	}
	return "unknown"
}
func videoFeeReviewedStatus(asset *model.SeedanceResource, row map[string]any) (string, error) {
	if asset.GroupType == "channel_portrait" && seedanceString(row, "purpose") != "" && seedanceString(row, "purpose") != "real_person" {
		return "Failed", nil
	}
	status := seedanceString(row, "status")
	if status == "" {
		cert, _ := row["assetCertification"].(map[string]any)
		status = seedanceString(cert, "status")
	}
	if status == "" {
		entries, _ := row["certifications"].([]any)
		if len(entries) > 0 {
			status = "active"
			for _, entry := range entries {
				cert, _ := entry.(map[string]any)
				item := videoFeeStatus(seedanceString(cert, "status"))
				if item == "failed" || item == "blocked" || item == "rejected" {
					status = item
					break
				}
				if item != "active" {
					status = "pending"
				}
			}
		}
	}
	switch videoFeeStatus(status) {
	case "active":
		return "Active", nil
	case "failed", "blocked", "rejected":
		return "Failed", nil
	case "uploaded":
		if asset.GroupType == "ordinary" {
			return "Active", nil
		}
		return "Pending", nil
	case "certifying", "pending", "processing":
		return "Pending", nil
	default:
		return "", seedanceError(502, "Invalid media certification status")
	}
}
func storeVideoFeeReview(asset *model.SeedanceResource, row map[string]any) error {
	review := videoFeeReview{Status: videoFeeStatus(seedanceString(row, "status"))}
	if purpose := seedanceString(row, "purpose"); purpose == "real_person" || purpose == "ordinary" {
		review.Purpose = purpose
	}
	entries, _ := row["certifications"].([]any)
	for _, entry := range entries {
		if len(review.Certifications) >= 32 {
			break
		}
		cert, _ := entry.(map[string]any)
		code := seedanceString(cert, "interfaceCode")
		if len(code) > 64 || strings.ContainsAny(code, "/?=& \r\n") {
			code = ""
		}
		review.Certifications = append(review.Certifications, videoFeeCertification{InterfaceCode: code, Status: videoFeeStatus(seedanceString(cert, "status"))})
	}
	if review.Status == "unknown" {
		cert, _ := row["assetCertification"].(map[string]any)
		review.Status = videoFeeStatus(seedanceString(cert, "status"))
	}
	raw, err := common.Marshal(review)
	if err != nil {
		return err
	}
	asset.ResultData = string(raw)
	return seedanceDB().Model(asset).Update("result_data", asset.ResultData).Error
}
func videoFeeStoredReview(asset *model.SeedanceResource) map[string]any {
	var review videoFeeReview
	if asset.ResultData == "" || common.UnmarshalJsonStr(asset.ResultData, &review) != nil {
		return nil
	}
	return map[string]any{"status": review.Status, "purpose": review.Purpose, "scope_model": asset.Model, "owner_verified": false}
}
