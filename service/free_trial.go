package service

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const (
	FreeTrialGroup       = "Subscription"
	legacyFreeTrialGroup = "Free Trial"
)

func IsFreeTrialGroup(group string) bool {
	normalized := strings.TrimSpace(group)
	return strings.EqualFold(normalized, FreeTrialGroup) ||
		strings.EqualFold(normalized, legacyFreeTrialGroup)
}

func IsFreeTrialEligibleModel(modelName string) bool {
	lower := strings.ToLower(strings.TrimSpace(modelName))
	if lower == "" {
		return false
	}
	if common.IsImageGenerationModel(lower) {
		return false
	}
	if !common.IsOpenAITextModel(lower) {
		return false
	}
	return strings.Contains(lower, "gpt-") || strings.HasPrefix(lower, "chatgpt")
}

func FreeTrialModelAccess(userID int, modelName string) (hasTrial bool, allowed bool, err error) {
	return model.GetActiveGPTTrialModelAccess(userID, modelName)
}

// Trial eligibility selects a funding source; it restricts model access only
// when the caller explicitly uses a trial-only key. Ordinary keys continue
// through normal wallet/subscription billing for models outside the trial.
type FreeTrialRequestAccess struct {
	HasTrial  bool
	Allowed   bool
	Forbidden bool
}

func FreeTrialRequestModelAccess(userID int, modelName, tokenGroup string) (FreeTrialRequestAccess, error) {
	hasTrial, allowed, err := FreeTrialModelAccess(userID, modelName)
	if err != nil {
		return FreeTrialRequestAccess{}, err
	}
	return FreeTrialRequestAccess{
		HasTrial:  hasTrial,
		Allowed:   hasTrial && allowed,
		Forbidden: IsFreeTrialGroup(tokenGroup) && !allowed,
	}, nil
}

func FilterFreeTrialModelsForUser(userID int, models []string) ([]string, error) {
	hasTrial, allowedModels, err := model.GetActiveGPTTrialModels(userID)
	if err != nil {
		return nil, err
	}
	if !hasTrial {
		return []string{}, nil
	}
	filtered := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, modelName := range models {
		trimmed := strings.TrimSpace(modelName)
		key := strings.ToLower(trimmed)
		if trimmed == "" {
			continue
		}
		if _, ok := allowedModels[key]; !ok {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		filtered = append(filtered, trimmed)
	}
	return filtered, nil
}

func FilterFreeTrialModels(models []string) []string {
	if len(models) == 0 {
		return []string{}
	}
	filtered := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, modelName := range models {
		if !IsFreeTrialEligibleModel(modelName) {
			continue
		}
		if _, ok := seen[modelName]; ok {
			continue
		}
		seen[modelName] = struct{}{}
		filtered = append(filtered, modelName)
	}
	return filtered
}
