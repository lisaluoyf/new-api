package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUserModelDiscountAdminUpdatesAndClearsWithoutChangingPreferences(t *testing.T) {
	setupUserAffRatioOverrideControllerTestDB(t)
	user := model.User{Id: 1, Username: "discount-owner", DisplayName: "Discount Owner", Group: "default", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	user.SetSetting(dto.UserSetting{Language: "zh", BillingPreference: "wallet_only", QuotaWarningThreshold: 12})
	require.NoError(t, model.DB.Create(&user).Error)
	for _, ratios := range []string{`{"seedance-2.5":0.947368}`, `{}`} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPut, "/api/user/", strings.NewReader(`{"id":1,"username":"discount-owner","display_name":"Discount Owner","group":"default","model_discount_ratios":`+ratios+`}`))
		ctx.Set("role", common.RoleRootUser)
		UpdateUser(ctx)
		require.Contains(t, recorder.Body.String(), `"success":true`)
		require.NoError(t, model.DB.First(&user, 1).Error)
		setting := user.GetSetting()
		require.Equal(t, "wallet_only", setting.BillingPreference)
		require.Equal(t, "zh", setting.Language)
		require.Equal(t, 12.0, setting.QuotaWarningThreshold)
		if ratios == `{}` {
			require.Empty(t, setting.ModelDiscountRatios)
		} else {
			require.Equal(t, .947368, setting.ModelDiscountRatios["seedance-2.5"])
		}
	}
}

func TestUserModelDiscountRejectsInvalidAdminInputWithoutClearingExistingRule(t *testing.T) {
	setupUserAffRatioOverrideControllerTestDB(t)
	user := model.User{Id: 1, Username: "discount-owner", Role: common.RoleCommonUser}
	user.SetSetting(dto.UserSetting{DefaultModelDiscount: .8, ModelDiscountRatios: map[string]float64{"seedance-2.5": .9}})
	require.NoError(t, model.DB.Create(&user).Error)
	for _, ratios := range []string{`{"seedance-2.5":0}`, `{"seedance-2.5":1.1}`, `{"":0.9}`} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPut, "/api/user/", strings.NewReader(`{"id":1,"username":"discount-owner","model_discount_ratios":`+ratios+`}`))
		ctx.Set("role", common.RoleRootUser)
		UpdateUser(ctx)
		require.Contains(t, recorder.Body.String(), `"success":false`)
		require.NoError(t, model.DB.First(&user, 1).Error)
		require.Equal(t, .9, user.GetSetting().ModelDiscountRatios["seedance-2.5"])
	}
}

func TestUserModelDiscountCannotBeOverriddenByUserPreferences(t *testing.T) {
	setupUserAffRatioOverrideControllerTestDB(t)
	user := model.User{Id: 1, Username: "discount-owner", Role: common.RoleCommonUser}
	user.SetSetting(dto.UserSetting{DefaultModelDiscount: .8, ModelDiscountRatios: map[string]float64{"seedance-2.5": .9}})
	require.NoError(t, model.DB.Create(&user).Error)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/user/setting", strings.NewReader(`{"notify_type":"email","quota_warning_threshold":10,"default_model_discount":0.01,"model_discount_ratios":{"seedance-2.5":0.01}}`))
	ctx.Set("id", 1)
	UpdateUserSetting(ctx)
	require.Contains(t, recorder.Body.String(), `"success":true`)
	require.NoError(t, model.DB.First(&user, 1).Error)
	require.Equal(t, .9, user.GetSetting().ModelDiscountRatios["seedance-2.5"])
	require.Equal(t, .8, user.GetSetting().DefaultModelDiscount)
}

func TestUserDefaultDiscountAdminLifecycleAndReadback(t *testing.T) {
	setupUserAffRatioOverrideControllerTestDB(t)
	user := model.User{Id: 1, Username: "default-owner", Role: common.RoleCommonUser, Group: "default"}
	user.SetSetting(dto.UserSetting{Language: "zh", ModelDiscountRatios: map[string]float64{"model": 1}})
	require.NoError(t, model.DB.Create(&user).Error)
	for _, test := range []struct {
		fields string
		want   float64
	}{
		{`,"default_model_discount":0.8`, .8},
		{"", .8},
		{`,"default_model_discount":1`, 1},
		{`,"default_model_discount":null`, 0},
	} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPut, "/api/user/", strings.NewReader(`{"id":1,"username":"default-owner","group":"default"`+test.fields+`}`))
		ctx.Set("role", common.RoleRootUser)
		UpdateUser(ctx)
		require.Contains(t, recorder.Body.String(), `"success":true`)
		require.NoError(t, model.DB.First(&user, 1).Error)
		require.Equal(t, test.want, user.GetSetting().DefaultModelDiscount)
		require.Equal(t, "zh", user.GetSetting().Language)
		require.Equal(t, 1.0, user.GetSetting().ModelDiscountRatios["model"])

		readback := httptest.NewRecorder()
		readCtx, _ := gin.CreateTestContext(readback)
		readCtx.Params = gin.Params{{Key: "id", Value: "1"}}
		readCtx.Set("role", common.RoleRootUser)
		GetUser(readCtx)
		var response struct {
			Success bool `json:"success"`
			Data    struct {
				DefaultModelDiscount float64 `json:"default_model_discount"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(readback.Body.Bytes(), &response))
		require.True(t, response.Success)
		require.Equal(t, test.want, response.Data.DefaultModelDiscount)
	}
}

func TestUserDefaultDiscountRejectsInvalidOrUnauthorizedUpdates(t *testing.T) {
	setupUserAffRatioOverrideControllerTestDB(t)
	user := model.User{Id: 1, Username: "default-owner", DisplayName: "Original", Role: common.RoleCommonUser}
	user.SetSetting(dto.UserSetting{DefaultModelDiscount: .8})
	require.NoError(t, model.DB.Create(&user).Error)
	for _, value := range []string{"0", "-1", "1.01", "0.0000001", `"0.9"`, "true", "{}", "[]"} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPut, "/api/user/", strings.NewReader(`{"id":1,"username":"default-owner","display_name":"Changed","default_model_discount":`+value+`}`))
		ctx.Set("role", common.RoleRootUser)
		UpdateUser(ctx)
		require.Contains(t, recorder.Body.String(), `"success":false`)
		require.NoError(t, model.DB.First(&user, 1).Error)
		require.Equal(t, .8, user.GetSetting().DefaultModelDiscount)
		require.Equal(t, "Original", user.DisplayName)
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/user/", strings.NewReader(`{"id":1,"username":"default-owner","default_model_discount":0.1}`))
	ctx.Set("role", common.RoleCommonUser)
	UpdateUser(ctx)
	require.Contains(t, recorder.Body.String(), `"success":false`)
	require.NoError(t, model.DB.First(&user, 1).Error)
	require.Equal(t, .8, user.GetSetting().DefaultModelDiscount)
}
