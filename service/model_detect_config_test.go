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
