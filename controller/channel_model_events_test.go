package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRecoveryProbeDoesNotOverwriteAnotherModel(t *testing.T) {
	db := setupModelDataToggleTestDB(t)
	stale := &model.Channel{Id: 214, Status: 1, Key: "test"}
	stale.SetOtherInfo(map[string]interface{}{"auto_disabled_models": map[string]interface{}{
		"gpt-5.6-terra": map[string]interface{}{"reason": "terra failure", "disabled_at": 1},
	}})
	require.NoError(t, db.Create(stale).Error)
	current := *stale
	info := current.GetOtherInfo()
	info["auto_disabled_models"].(map[string]interface{})["gpt-5.6-sol"] = map[string]interface{}{"reason": "sol failure", "disabled_at": 2}
	current.SetOtherInfo(info)
	require.NoError(t, db.Model(&current).Update("other_info", current.OtherInfo).Error)
	recordCommonAutoReenableModelProbe(stale, "gpt-5.6-terra", testResult{}, 100)
	require.NoError(t, db.First(&current, 214).Error)
	require.Contains(t, current.GetOtherInfo()["auto_disabled_models"], "gpt-5.6-sol")
	// A completed probe cannot resurrect recovery metadata removed by an operator.
	require.NoError(t, db.Model(&current).Update("other_info", `{}`).Error)
	recordCommonAutoReenableModelProbe(stale, "gpt-5.6-terra", testResult{}, 100)
	require.NoError(t, db.First(&current, 214).Error)
	require.Empty(t, current.GetOtherInfo())
}

func TestChannelModelHistoryFiltersAndPaginates(t *testing.T) {
	db := setupModelDataToggleTestDB(t)
	for i := 0; i < 52; i++ {
		require.NoError(t, model.RecordChannelModelEvent(db, 214, "gpt-5.6-sol", "disable", "manual", "Manually disabled", 42))
	}
	require.NoError(t, model.RecordChannelModelEvent(db, 219, "gpt-5.6-sol", "disable", "manual", "other channel", 42))
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/?channel_id=214&model=gpt-5.6-sol&before_id=3", nil)
	GetChannelModelEvents(ctx)
	require.Equal(t, 200, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"has_more":false`)
	require.NotContains(t, recorder.Body.String(), "other channel")
	require.Contains(t, recorder.Body.String(), `"id":2`)
	require.NotContains(t, recorder.Body.String(), `"id":3`)
}

func TestModelStatusSourceDistinguishesMissingHistory(t *testing.T) {
	manual := `{"manually_disabled_models":["gpt-5.6-sol"]}`
	require.Equal(t, "manual", modelDataStatusSource(1, false, &manual, "gpt-5.6-sol"))
	require.Equal(t, "unknown", modelDataStatusSource(1, false, nil, "gpt-5.6-sol"))
	require.Equal(t, "enabled", modelDataStatusSource(1, true, nil, "gpt-5.6-sol"))
}
