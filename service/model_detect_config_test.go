package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestScheduledFingerprintSkipsConfiguredChannels(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.Channel{}, &model.ChannelDetectLog{}))
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })

	writeConfig := func(name string, skipped []int) {
		cfg := defaultDetectConfig()
		cfg.FingerprintEnabled = true
		cfg.FingerprintIntervalMinutes = 1
		cfg.FingerprintSkipChannelIDs = skipped
		value, marshalErr := common.Marshal(cfg)
		require.NoError(t, marshalErr)
		require.NoError(t, db.Save(&model.Option{Key: DetectConfigKey(name), Value: string(value)}).Error)
	}
	writeConfig("model-a", []int{38, 224})
	writeConfig("model-b", []int{})
	baseURL := "https://upstream.example"
	for _, id := range []int{38, 224, 39} {
		require.NoError(t, db.Create(&model.Channel{
			Id: id, Status: common.ChannelStatusEnabled, Key: "test-key",
			BaseURL: &baseURL, Models: "model-a,model-b",
		}).Error)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/fingerprint", r.URL.Path)
		_, _ = w.Write([]byte("{\"type\":\"error\",\"error\":\"local test result\"}\n"))
	}))
	defer server.Close()
	count := func(channelID int, name string) int64 {
		var total int64
		require.NoError(t, db.Model(&model.ChannelDetectLog{}).
			Where("channel_id = ? AND claimed_model = ?", channelID, name).Count(&total).Error)
		return total
	}
	runAutoDetectOnce(server.URL)
	for _, id := range []int{38, 224} {
		require.Zero(t, count(id, "model-a"), "skipped channels must not produce fingerprint logs")
		require.EqualValues(t, 1, count(id, "model-b"), "the other model must still run")
	}
	require.EqualValues(t, 1, count(39, "model-a"))
	require.EqualValues(t, 1, count(39, "model-b"))

	// Clearing the list restores scheduled checks without changing the enable flag.
	writeConfig("model-a", []int{})
	runAutoDetectOnce(server.URL)
	for _, id := range []int{38, 224} {
		require.EqualValues(t, 1, count(id, "model-a"))
	}

	// Manual checks remain available even while this model/channel is skipped.
	writeConfig("model-a", []int{38, 224})
	t.Setenv("APIMASTER_FLASK_URL", server.URL)
	var channel model.Channel
	require.NoError(t, db.First(&channel, 38).Error)
	RunChannelDetectionNow(&channel, "model-a")
	require.EqualValues(t, 2, count(38, "model-a"))
}

func TestScheduledFingerprintPriceTopRanksAllStatuses(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.Channel{}, &model.ChannelModelPricing{}, &model.Ability{}, &model.ChannelDetectLog{}))
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	baseURL, one := "https://upstream.example", 1.0
	for _, ch := range []model.Channel{
		{Id: 1, Status: 2, Models: "model-a"},
		{Id: 2, Status: 3, Models: "model-a"},
		{Id: 3, Status: 1, Models: "model-a"},
		{Id: 4, Status: 1, Models: "model-a"},
	} {
		ch.Key, ch.BaseURL, ch.RechargeRate, ch.ApimasterPriceRatio = "test-key", &baseURL, &one, &one
		require.NoError(t, db.Create(&ch).Error)
		require.NoError(t, db.Create(&model.ChannelModelPricing{ChannelId: ch.Id, ModelName: "model-a", InputPrice: float64(ch.Id), GroupRatio: 1}).Error)
	}
	save := func(enabled bool, skipped []int) {
		cfg := defaultDetectConfig()
		cfg.FingerprintEnabled, cfg.FingerprintPriceTopEnabled, cfg.FingerprintPriceTopN = true, enabled, 2
		cfg.FingerprintSkipChannelIDs = skipped
		value, e := common.Marshal(cfg)
		require.NoError(t, e)
		require.NoError(t, db.Save(&model.Option{Key: DetectConfigKey("model-a"), Value: string(value)}).Error)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{\"type\":\"error\",\"error\":\"local test result\"}\n"))
	}))
	defer server.Close()
	loggedIDs := func() []int {
		var ids []int
		require.NoError(t, db.Model(&model.ChannelDetectLog{}).Order("channel_id").Pluck("channel_id", &ids).Error)
		return ids
	}
	clear := func() { require.NoError(t, db.Where("1 = 1").Delete(&model.ChannelDetectLog{}).Error) }
	save(true, nil)
	runAutoDetectOnce(server.URL)
	require.Equal(t, []int{1, 2}, loggedIDs(), "manually and automatically disabled cheapest channels must run")
	clear()
	save(true, []int{1})
	runAutoDetectOnce(server.URL)
	require.Equal(t, []int{2}, loggedIDs(), "skipped top channel must not be replaced by rank 3")
	clear()
	require.NoError(t, db.Model(&model.ChannelModelPricing{}).Where("channel_id = ?", 4).Update("input_price", 0.5).Error)
	save(true, nil)
	runAutoDetectOnce(server.URL)
	require.Equal(t, []int{1, 4}, loggedIDs(), "a price update must change the next round's selection")
	clear()
	save(false, nil)
	runAutoDetectOnce(server.URL)
	require.Equal(t, []int{2, 3, 4}, loggedIDs(), "unchecked must restore the existing status rules")
	clear()
	save(true, nil)
	t.Setenv("APIMASTER_FLASK_URL", server.URL)
	var ch model.Channel
	require.NoError(t, db.First(&ch, 3).Error)
	RunChannelDetectionNow(&ch, "model-a")
	require.Equal(t, []int{3}, loggedIDs(), "manual detection must ignore the price limit")
}

func TestFingerprintPriceRankingMarkupAndTies(t *testing.T) {
	one, two, cheap, priority := 1.0, 2.0, 0.2, int64(10)
	overrides := `{"model-a":0.1}`
	rows := []ChannelDataPricingRow{
		{ChannelID: 1, InputPrice: &one, RechargeRate: &one, ApimasterPriceRatio: 1},
		{ChannelID: 2, InputPrice: &two, RechargeRate: &one, ApimasterPriceRatio: 1, ModelPriceRatios: &overrides},
		{ChannelID: 3, InputPrice: &cheap, RechargeRate: &one, ApimasterPriceRatio: 1, Priority: &priority},
		{ChannelID: 4, ApimasterPriceRatio: 1},
	}
	require.Equal(t, map[int]bool{3: true}, rankFingerprintPriceChannels(rows, "model-a", 1))
	require.Equal(t, map[int]bool{2: true, 3: true}, rankFingerprintPriceChannels(rows, "model-a", 2))
	require.Len(t, rankFingerprintPriceChannels(rows, "model-a", 10), 4)
	require.Empty(t, rankFingerprintPriceChannels(rows, "model-a", 0))
}
