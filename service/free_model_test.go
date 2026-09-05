package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestIsFreeModel(t *testing.T) {
	require.True(t, IsFreeModel(FreeModelID))
	require.True(t, IsFreeModel("  "+FreeModelID+"  "))
	require.False(t, IsFreeModel("APIMASTER-FREEMODEL"))
	require.False(t, IsFreeModel("apimaster-freemodel-v2"))
	require.Equal(t, FreeModelID, FreeModelResponseName(FreeModelID, "provider/real-free"))
	require.Equal(t, "provider/paid", FreeModelResponseName("gpt-5.4", "provider/paid"))
}

func TestValidateFreeModelSettings(t *testing.T) {
	require.NoError(t, ValidateFreeModelSettings(DefaultFreeModelSettings()))
	require.Error(t, ValidateFreeModelSettings(FreeModelSettings{AccountRequestsPerMinute: 10}))
	require.Error(t, ValidateFreeModelSettings(FreeModelSettings{CumulativePaidEnabled: true, MinimumCumulativePaidUSD: -1, AccountRequestsPerMinute: 10}))
	require.Error(t, ValidateFreeModelSettings(FreeModelSettings{CumulativePaidEnabled: true, AccountRequestsPerMinute: 0}))
}

func TestFreeModelGlobalAvailabilityDefaultsOnForExistingSettings(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	oldSetting, existed := common.OptionMap[FreeModelSettingsOptionKey()]
	common.OptionMap[FreeModelSettingsOptionKey()] = `{"cumulative_paid_enabled":true,"minimum_cumulative_paid_usd":50,"active_subscription_enabled":true,"minimum_subscription_price_usd":20,"account_requests_per_minute":10,"max_attempts":3}`
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if existed {
			common.OptionMap[FreeModelSettingsOptionKey()] = oldSetting
		} else {
			delete(common.OptionMap, FreeModelSettingsOptionKey())
		}
	})

	require.True(t, IsFreeModelEnabled(), "existing settings without enabled must remain available")

	common.OptionMapRWMutex.Lock()
	common.OptionMap[FreeModelSettingsOptionKey()] = `{"enabled":false,"cumulative_paid_enabled":true,"minimum_cumulative_paid_usd":50,"active_subscription_enabled":true,"minimum_subscription_price_usd":20,"account_requests_per_minute":10,"max_attempts":3}`
	common.OptionMapRWMutex.Unlock()
	require.False(t, IsFreeModelEnabled())
}

func TestFreeModelEligibilityUsesActualPaidOrSubscription(t *testing.T) {
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.UserSubscription{}))

	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	oldSetting := common.OptionMap[FreeModelSettingsOptionKey()]
	common.OptionMap[FreeModelSettingsOptionKey()] = `{"cumulative_paid_enabled":true,"minimum_cumulative_paid_usd":50,"active_subscription_enabled":true,"minimum_subscription_price_usd":20,"account_requests_per_minute":10}`
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap[FreeModelSettingsOptionKey()] = oldSetting
		common.OptionMapRWMutex.Unlock()
	})

	require.NoError(t, db.Create(&model.User{Id: 1, Username: "paid", AffCode: "paid", Role: common.RoleCommonUser}).Error)
	require.NoError(t, db.Create(&model.TopUp{UserId: 1, TradeNo: "paid-49", PaidAmountUSD: 49.99, CreditedAmount: 80, Status: common.TopUpStatusSuccess}).Error)
	ok, _, err := FreeModelEligibility(1)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, db.Create(&model.TopUp{UserId: 1, TradeNo: "paid-extra", PaidAmountUSD: 0.01, Status: common.TopUpStatusSuccess}).Error)
	ok, source, err := FreeModelEligibility(1)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "cumulative_paid", source)

	require.NoError(t, db.Create(&model.User{Id: 2, Username: "subscriber", AffCode: "subscriber", Role: common.RoleCommonUser}).Error)
	require.NoError(t, db.Create(&model.UserSubscription{UserId: 2, Status: "active", EndTime: time.Now().Add(time.Hour).Unix(), PriceAmountSnapshot: 20}).Error)
	ok, source, err = FreeModelEligibility(2)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "active_subscription", source)

	require.NoError(t, db.Create(&model.User{Id: 3, Username: "low-sub", AffCode: "low-sub", Role: common.RoleCommonUser}).Error)
	require.NoError(t, db.Create(&model.UserSubscription{UserId: 3, Status: "active", EndTime: time.Now().Add(time.Hour).Unix(), PriceAmountSnapshot: 19.99}).Error)
	ok, _, err = FreeModelEligibility(3)
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, db.Create(&model.User{Id: 4, Username: "admin", AffCode: "admin", Role: common.RoleAdminUser}).Error)
	ok, source, err = FreeModelEligibility(4)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "admin", source)

	require.NoError(t, db.Create(&model.User{Id: 5, Username: "expired", AffCode: "expired", Role: common.RoleCommonUser}).Error)
	require.NoError(t, db.Create(&model.UserSubscription{UserId: 5, Status: "active", EndTime: time.Now().Add(-time.Hour).Unix(), PriceAmountSnapshot: 100}).Error)
	ok, _, err = FreeModelEligibility(5)
	require.NoError(t, err)
	require.False(t, ok)
}
