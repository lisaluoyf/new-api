package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupFreeGPTSubscriptionControllerTest(t *testing.T, publicEnabled, whitelisted bool) (*gorm.DB, int) {
	t.Helper()
	originalDB := model.DB
	originalAPIMasterDB := model.APIMASTER_PG_DB
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.SubscriptionPlan{},
		&model.SubscriptionOrder{},
		&model.UserSubscription{},
	))
	model.DB = db
	model.APIMASTER_PG_DB = nil

	common.OptionMapRWMutex.Lock()
	originalOptionMapWasNil := common.OptionMap == nil
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	originalPublic, hadPublic := common.OptionMap[model.GPTSubscriptionPublicEnabledOption]
	originalWhitelist, hadWhitelist := common.OptionMap[model.GPTSubscriptionWhitelistOption]
	if publicEnabled {
		common.OptionMap[model.GPTSubscriptionPublicEnabledOption] = "true"
	} else {
		common.OptionMap[model.GPTSubscriptionPublicEnabledOption] = "false"
	}
	if whitelisted {
		common.OptionMap[model.GPTSubscriptionWhitelistOption] = "free-test@example.com"
	} else {
		common.OptionMap[model.GPTSubscriptionWhitelistOption] = "lisa.luoyf@gmail.com"
	}
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		model.DB = originalDB
		model.APIMASTER_PG_DB = originalAPIMasterDB
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if originalOptionMapWasNil {
			common.OptionMap = nil
			return
		}
		if hadPublic {
			common.OptionMap[model.GPTSubscriptionPublicEnabledOption] = originalPublic
		} else {
			delete(common.OptionMap, model.GPTSubscriptionPublicEnabledOption)
		}
		if hadWhitelist {
			common.OptionMap[model.GPTSubscriptionWhitelistOption] = originalWhitelist
		} else {
			delete(common.OptionMap, model.GPTSubscriptionWhitelistOption)
		}
	})

	const userID = 91001
	require.NoError(t, db.Create(&model.User{
		Id: userID, Username: "free-controller-test", Email: "free-test@example.com",
		Status: common.UserStatusEnabled,
	}).Error)
	return db, userID
}

func performFreeGPTSubscriptionRequest(t *testing.T, userID, planID int) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set("id", userID)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/subscription/gpt/free",
		strings.NewReader(common.GetJsonString(map[string]any{"plan_id": planID})),
	)
	context.Request.Header.Set("Content-Type", "application/json")
	ActivateFreeGPTSubscription(context)

	var response map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	return recorder, response
}

func TestActivateFreeGPTSubscriptionRequiresPurchaseAccess(t *testing.T) {
	db, userID := setupFreeGPTSubscriptionControllerTest(t, false, false)
	plan := model.SubscriptionPlan{
		Title: "Free Pro", PlanType: model.SubscriptionPlanTypeGPTSubscription,
		PriceAmount: 0, Currency: "USD", DurationUnit: model.SubscriptionDurationDay,
		DurationValue: 30, Enabled: true, TierLevel: 1,
	}
	require.NoError(t, db.Create(&plan).Error)

	recorder, response := performFreeGPTSubscriptionRequest(t, userID, plan.Id)
	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Equal(t, false, response["success"])
	var subscriptionCount int64
	require.NoError(t, db.Model(&model.UserSubscription{}).Count(&subscriptionCount).Error)
	require.Zero(t, subscriptionCount)
}

func TestActivateFreeGPTSubscriptionAllowsWhitelistedUser(t *testing.T) {
	db, userID := setupFreeGPTSubscriptionControllerTest(t, false, true)
	plan := model.SubscriptionPlan{
		Title: "Free Pro", PlanType: model.SubscriptionPlanTypeGPTSubscription,
		PriceAmount: 0, Currency: "USD", DurationUnit: model.SubscriptionDurationDay,
		DurationValue: 30, Enabled: true, TierLevel: 1,
	}
	require.NoError(t, db.Create(&plan).Error)

	recorder, response := performFreeGPTSubscriptionRequest(t, userID, plan.Id)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, true, response["success"])
	var subscriptionCount int64
	require.NoError(t, db.Model(&model.UserSubscription{}).Where("user_id = ?", userID).Count(&subscriptionCount).Error)
	require.EqualValues(t, 1, subscriptionCount)
}
