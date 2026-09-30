package service

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/abema/go-mp4"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestVideoOutputDimensionsReadActualMP4Track(t *testing.T) {
	for _, dim := range []videoOutputDimensions{{854, 480}, {1280, 720}, {480, 854}} {
		file, err := os.CreateTemp(t.TempDir(), "spec-*.mp4")
		require.NoError(t, err)
		defer file.Close()
		w := mp4.NewWriter(file)
		for _, typ := range []mp4.BoxType{mp4.BoxTypeMoov(), mp4.BoxTypeTrak(), mp4.BoxTypeTkhd()} {
			_, err = w.StartBox(&mp4.BoxInfo{Type: typ})
			require.NoError(t, err)
		}
		_, err = mp4.Marshal(w, &mp4.Tkhd{Width: uint32(dim.Width) << 16, Height: uint32(dim.Height) << 16}, mp4.Context{})
		require.NoError(t, err)
		for i := 0; i < 3; i++ {
			_, err = w.EndBox()
			require.NoError(t, err)
		}
		_, err = file.Seek(0, 0)
		require.NoError(t, err)
		got, err := readVideoOutputDimensions(file)
		require.NoError(t, err)
		require.Equal(t, dim, got)
	}
}

func TestVideoLogPersistsRequestedSubmittedAndBillingSpecs(t *testing.T) {
	req := &relaycommon.TaskSubmitReq{Model: "seedance-2.5", Duration: 4, Metadata: map[string]interface{}{
		"resolution": "480p", "aspect_ratio": "9:16", "billing_variant": "480p-input", "has_video": true,
		"requested_spec": map[string]interface{}{"resolution": "480P", "ratio": "9:16"},
	}}
	data := BuildVideoRequestDataForLog(req)
	require.Equal(t, "480p", data["resolution"])
	require.Equal(t, "480P", data["effective_resolution"])
	require.Equal(t, "9:16", data["aspect_ratio"])
	require.Equal(t, "480p-input", data["billing_variant"])
	require.Equal(t, "480P", data["requested_spec"].(map[string]interface{})["resolution"])
	require.Equal(t, "480p", data["submitted_spec"].(map[string]interface{})["resolution"])
}

func TestDurationAccountingFreezesActualSelectedUnitPrice(t *testing.T) {
	oldDB, oldOptions := model.DB, common.OptionMap
	t.Cleanup(func() { model.DB = oldDB; common.OptionMap = oldOptions })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.ChannelModelPricing{}, &model.User{}))
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "video-accounting"}).Error)
	recharge, markup := 1.0, 1.1
	require.NoError(t, db.Create(&model.Channel{Id: 153, RechargeRate: &recharge, ApimasterPriceRatio: &markup}).Error)
	require.NoError(t, model.UpsertChannelModelPricings([]model.ChannelModelPricing{{ChannelId: 153, ModelName: "seedance-2.0", InputPrice: .14, GroupRatio: 1}}))
	for _, price := range []float64{.1562, .0726} {
		got := BuildConsumeAccountingFields(ConsumeAccountingInput{UserId: 1, ChannelId: 153, ModelName: "seedance-2.0", DurationSeconds: 5, DurationUnitPrice: price, GroupRatio: 1.05, Quota: int(price * 5 * 1.05 * common.QuotaPerUnit)})
		var snap consumeAccountingSnapshot
		require.NoError(t, common.UnmarshalJsonStr(got.Snapshot, &snap))
		user := snap.Prices["user_price"].(map[string]interface{})
		require.InDelta(t, price, user["input_price"], 1e-12)
		require.InDelta(t, float64(got.UserFinalAmountUSD), float64(snap.AmountsUSD["user_final"]), 1e-12)
		require.InDelta(t, price*5*1.05, got.UserFinalAmountUSD, .0000021)
	}
}
