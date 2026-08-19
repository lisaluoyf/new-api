package service

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const FreeModelID = "apimaster-freemodel"

const freeModelSettingsOption = "FreeModelSettings"

type FreeModelSettings struct {
	CumulativePaidEnabled       bool    `json:"cumulative_paid_enabled"`
	MinimumCumulativePaidUSD    float64 `json:"minimum_cumulative_paid_usd"`
	ActiveSubscriptionEnabled   bool    `json:"active_subscription_enabled"`
	MinimumSubscriptionPriceUSD float64 `json:"minimum_subscription_price_usd"`
	AccountRequestsPerMinute    int     `json:"account_requests_per_minute"`
}

var defaultFreeModelSettings = FreeModelSettings{
	CumulativePaidEnabled:       true,
	MinimumCumulativePaidUSD:    50,
	ActiveSubscriptionEnabled:   true,
	MinimumSubscriptionPriceUSD: 20,
	AccountRequestsPerMinute:    10,
}

func IsFreeModel(modelName string) bool {
	return strings.TrimSpace(modelName) == FreeModelID
}

func FreeModelResponseName(originModelName, upstreamModelName string) string {
	if IsFreeModel(originModelName) {
		return FreeModelID
	}
	return upstreamModelName
}

func DefaultFreeModelSettings() FreeModelSettings { return defaultFreeModelSettings }

func GetFreeModelSettings() FreeModelSettings {
	settings := defaultFreeModelSettings
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap[freeModelSettingsOption]
	common.OptionMapRWMutex.RUnlock()
	if strings.TrimSpace(raw) == "" {
		return settings
	}
	if err := common.UnmarshalJsonStr(raw, &settings); err != nil {
		return defaultFreeModelSettings
	}
	return normalizeFreeModelSettings(settings)
}

func ValidateFreeModelSettings(settings FreeModelSettings) error {
	if !settings.CumulativePaidEnabled && !settings.ActiveSubscriptionEnabled {
		return errors.New("at least one FreeModel eligibility condition must be enabled")
	}
	if math.IsNaN(settings.MinimumCumulativePaidUSD) || math.IsInf(settings.MinimumCumulativePaidUSD, 0) ||
		math.IsNaN(settings.MinimumSubscriptionPriceUSD) || math.IsInf(settings.MinimumSubscriptionPriceUSD, 0) ||
		settings.MinimumCumulativePaidUSD < 0 || settings.MinimumSubscriptionPriceUSD < 0 {
		return errors.New("FreeModel eligibility amounts must be finite and non-negative")
	}
	if settings.AccountRequestsPerMinute <= 0 || settings.AccountRequestsPerMinute > 100000 {
		return errors.New("FreeModel requests per minute must be between 1 and 100000")
	}
	return nil
}

func normalizeFreeModelSettings(settings FreeModelSettings) FreeModelSettings {
	if math.IsNaN(settings.MinimumCumulativePaidUSD) || math.IsInf(settings.MinimumCumulativePaidUSD, 0) {
		settings.MinimumCumulativePaidUSD = defaultFreeModelSettings.MinimumCumulativePaidUSD
	}
	if math.IsNaN(settings.MinimumSubscriptionPriceUSD) || math.IsInf(settings.MinimumSubscriptionPriceUSD, 0) {
		settings.MinimumSubscriptionPriceUSD = defaultFreeModelSettings.MinimumSubscriptionPriceUSD
	}
	if settings.AccountRequestsPerMinute <= 0 {
		settings.AccountRequestsPerMinute = defaultFreeModelSettings.AccountRequestsPerMinute
	}
	return settings
}

func SaveFreeModelSettings(settings FreeModelSettings) error {
	if err := ValidateFreeModelSettings(settings); err != nil {
		return err
	}
	raw, err := common.Marshal(settings)
	if err != nil {
		return err
	}
	if err := model.UpdateOption(freeModelSettingsOption, string(raw)); err != nil {
		return err
	}
	return nil
}

func FreeModelEligibility(userID int) (bool, string, error) {
	if userID <= 0 {
		return false, "", errors.New("invalid user")
	}
	if model.IsAdmin(userID) {
		return true, "admin", nil
	}
	settings := GetFreeModelSettings()
	if settings.CumulativePaidEnabled {
		paid, err := model.GetUserPaidAmountUSD(userID)
		if err != nil {
			return false, "", err
		}
		if paid >= settings.MinimumCumulativePaidUSD {
			return true, "cumulative_paid", nil
		}
	}
	if settings.ActiveSubscriptionEnabled {
		ok, err := model.HasActiveSubscriptionAtPrice(userID, settings.MinimumSubscriptionPriceUSD)
		if err != nil {
			return false, "", err
		}
		if ok {
			return true, "active_subscription", nil
		}
	}
	return false, "", nil
}

func FreeModelSettingsOptionKey() string { return freeModelSettingsOption }

func FreeModelNotEligibleError() error {
	s := GetFreeModelSettings()
	conditions := make([]string, 0, 2)
	if s.CumulativePaidEnabled {
		conditions = append(conditions, fmt.Sprintf("cumulative paid amount >= $%.2f", s.MinimumCumulativePaidUSD))
	}
	if s.ActiveSubscriptionEnabled {
		conditions = append(conditions, fmt.Sprintf("an active subscription priced >= $%.2f", s.MinimumSubscriptionPriceUSD))
	}
	return fmt.Errorf("FreeModel requires %s", strings.Join(conditions, " or "))
}

func FreeModelRateLimitKey(userID int) string {
	return fmt.Sprintf("free_model_rate_limit:%d", userID)
}

func FreeModelRateLimitWindow() time.Duration { return time.Minute }
