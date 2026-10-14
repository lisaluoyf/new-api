package service

import (
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// ChannelDataPricingRow is the shared pricing source for the admin page and scheduled fingerprint ranking.
type ChannelDataPricingRow struct {
	ChannelID                  int
	ChannelName                string
	ChannelGroup               string
	Priority                   *int64
	BaseURL                    *string
	Setting                    *string
	ModelMapping               *string
	InputPrice                 *float64
	OutputPrice                *float64
	CachePrice                 *float64
	CacheCreationPrice         *float64
	CacheCreationPrice1h       *float64 `gorm:"column:cache_creation_price_1h"`
	GroupRatio                 *float64 // upstream group multiplier; nil when no pricing row
	PricingSource              *string
	RechargeRate               *float64
	ApimasterPriceRatio        float64 // per-channel markup; COALESCE'd to 1.0
	ModelPriceRatios           *string // per-model markup overrides JSON
	OtherSettings              string  // channel capability matrix (channels.settings)
	Status                     int
	ConsecutiveFingerprintPass int
	ModelEnabled               bool    // abilities.enabled for this (channel, model)
	OtherInfo                  *string // raw JSON from channels.other_info
}

func LoadChannelDataPricingRows(modelName string) ([]ChannelDataPricingRow, error) {

	// Match canonical model + all known provider variants (e.g. claude-haiku-4-5 ↔
	// claude-haiku-4-5-20251001 ↔ anthropic/claude-haiku-4.5). Without this, channels
	// that only stored a dated variant in channel_model_pricings get dropped.
	candidates := ModelNameCandidates(modelName)

	// channels.models is comma-separated; OR over (= / starts-with / ends-with / middle)
	// for every candidate name.
	modelsClauses := make([]string, 0, len(candidates))
	modelsArgs := make([]interface{}, 0, len(candidates)*4)
	for _, m := range candidates {
		modelsClauses = append(modelsClauses, "c.models = ? OR c.models LIKE ? OR c.models LIKE ? OR c.models LIKE ?")
		modelsArgs = append(modelsArgs, m, m+",%", "%,"+m, "%,"+m+",%")
	}

	// LEFT JOIN so channels that advertise the model in c.models but have no
	// channel_model_pricings row (upstream /api/pricing returned 401/404, or the
	// site uses cookie-only auth like nekocode) still appear in the table with
	// input_price=0. The `p.model_name IN (candidates)` filter must live in the
	// ON clause — putting it in WHERE would degenerate LEFT JOIN to INNER JOIN
	// (any non-NULL filter on the right table re-excludes the no-match rows).
	channelGroupColumn := "c.`group`"
	if common.UsingPostgreSQL {
		channelGroupColumn = `c."group"`
	}
	var rows []ChannelDataPricingRow
	// Match the same aliases and groups as the toggle. EXISTS avoids duplicate
	// pricing rows and reports enabled only while a matching route remains usable.
	err := model.DB.Table("channels c").
		Select("c.id as channel_id, c.name as channel_name, "+channelGroupColumn+" as channel_group, c.priority, c.base_url, c.setting, c.model_mapping, p.input_price, p.output_price, p.cache_price, p.cache_creation_price, p.cache_creation_price_1h, p.group_ratio, p.pricing_source, c.recharge_rate, COALESCE(c.apimaster_price_ratio, 1.0) AS apimaster_price_ratio, c.model_price_ratios, c.settings as other_settings, c.status, c.consecutive_fingerprint_pass, EXISTS (SELECT 1 FROM abilities a WHERE a.channel_id = c.id AND a.model IN ? AND a.enabled = ?) as model_enabled, c.other_info", candidates, true).
		Joins("LEFT JOIN channel_model_pricings p ON c.id = p.channel_id AND p.model_name IN ?", candidates).
		// Show all status (1/2/3) so the operator can act on auto-disabled ones from the table.
		Where("c.status IN (1, 2, 3)").
		Where("("+strings.Join(modelsClauses, " OR ")+")", modelsArgs...).
		// Missing-pricing rows (LEFT JOIN with no match) sort last via CASE;
		// portable across SQLite / MySQL / PostgreSQL (NULLS LAST is PG-only).
		Order("c.id ASC, CASE WHEN p.input_price IS NULL OR p.input_price <= 0 THEN 1 ELSE 0 END, p.input_price ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	// A single channel may have multiple variant rows in channel_model_pricings
	// (e.g. claude-haiku-4-5-20251001 + claude-haiku-4-5-20251001-thinking).
	// Keep the cheapest per channel.
	seen := map[int]bool{}
	deduped := make([]ChannelDataPricingRow, 0, len(rows))
	for _, r := range rows {
		if seen[r.ChannelID] {
			continue
		}
		seen[r.ChannelID] = true
		deduped = append(deduped, r)
	}
	rows = deduped

	// Per-channel model_mapping: upstream pricing may use the mapped name only.
	for i := range rows {
		// FreeModel's stored price is only a routing weight. Do not replace it with
		// the mapped real model's normal billing price or a global/manual fallback.
		if IsFreeModel(modelName) {
			continue
		}
		ApplyModelMappingPricingToRow(
			rows[i].ChannelID, rows[i].ModelMapping, modelName,
			&rows[i].InputPrice, &rows[i].OutputPrice, &rows[i].CachePrice, &rows[i].CacheCreationPrice,
			&rows[i].GroupRatio, &rows[i].PricingSource,
		)
		ApplyPublicManualPricingToRow(
			rows[i].Setting, modelName,
			&rows[i].InputPrice, &rows[i].OutputPrice, &rows[i].CachePrice, &rows[i].CacheCreationPrice,
			&rows[i].GroupRatio, &rows[i].PricingSource,
		)
		ApplyGlobalModelPricingToRow(
			modelName,
			&rows[i].InputPrice, &rows[i].OutputPrice, &rows[i].CachePrice, &rows[i].CacheCreationPrice,
			&rows[i].GroupRatio, &rows[i].PricingSource,
		)
		ApplyModelGroupRatioToRow(rows[i].Setting, modelName,
			&rows[i].InputPrice, &rows[i].OutputPrice, &rows[i].CachePrice, &rows[i].CacheCreationPrice,
			&rows[i].GroupRatio)
	}

	return rows, nil
}

// applyModelMappingPricingToRow replaces pricing with the mapped upstream
// model's row. The mapping is authoritative even when a cheaper canonical row
// was already joined: billing must follow the model actually sent upstream.
func ApplyModelMappingPricingToRow(
	channelID int,
	modelMapping *string,
	canonical string,
	inputPrice, outputPrice, cachePrice, cacheCreationPrice, groupRatio **float64,
	pricingSource **string,
) {
	pr, ok := ResolvePricingViaModelMapping(channelID, modelMapping, canonical)
	if !ok {
		return
	}
	in := pr.InputPrice
	*inputPrice = &in
	if outputPrice != nil && (pr.OutputPrice > 0 || IsJevModel(canonical) || IsTextEmbeddingModel(canonical)) {
		out := pr.OutputPrice
		*outputPrice = &out
	}
	if cachePrice != nil && pr.CachePrice > 0 {
		cp := pr.CachePrice
		*cachePrice = &cp
	}
	if cacheCreationPrice != nil && pr.CacheCreationPrice > 0 {
		ccp := pr.CacheCreationPrice
		*cacheCreationPrice = &ccp
	}
	if groupRatio != nil {
		gr := pr.GroupRatio
		*groupRatio = &gr
	}
	if pricingSource != nil && pr.PricingSource != "" {
		src := pr.PricingSource
		*pricingSource = &src
	}
}

// applyPublicManualPricingToRow fills pricing from public_model_prices × manual_group_ratio
// when upstream /api/pricing is unavailable and channel_model_pricings has no row.
func ApplyPublicManualPricingToRow(
	setting *string,
	canonical string,
	inputPrice, outputPrice, cachePrice, cacheCreationPrice, groupRatio **float64,
	pricingSource **string,
) {
	if inputPrice != nil && *inputPrice != nil && **inputPrice > 0 {
		return
	}
	pr, ok := LookupPublicManualPricing(setting, canonical)
	if !ok || pr.InputPrice <= 0 {
		return
	}
	in := pr.InputPrice
	*inputPrice = &in
	if outputPrice != nil && (pr.OutputPrice > 0 || IsJevModel(canonical) || IsTextEmbeddingModel(canonical)) {
		out := pr.OutputPrice
		*outputPrice = &out
	}
	if cachePrice != nil && pr.CachePrice > 0 {
		cp := pr.CachePrice
		*cachePrice = &cp
	}
	if cacheCreationPrice != nil && pr.CacheCreationPrice > 0 {
		ccp := pr.CacheCreationPrice
		*cacheCreationPrice = &ccp
	}
	if groupRatio != nil {
		gr := pr.GroupRatio
		*groupRatio = &gr
	}
	if pricingSource != nil {
		src := "manual"
		*pricingSource = &src
	}
}

// applyGlobalModelPricingToRow fills pricing from System Settings → Group & Model Pricing
// when channel_model_pricings has no row (common for direct MiniMax / self-hosted channels).
func ApplyGlobalModelPricingToRow(
	canonical string,
	inputPrice, outputPrice, cachePrice, cacheCreationPrice, groupRatio **float64,
	pricingSource **string,
) {
	if inputPrice != nil && *inputPrice != nil && **inputPrice > 0 {
		return
	}
	in, out, cache, cacheCreate, ok := GlobalModelPricingUSD(canonical)
	if !ok || in <= 0 {
		return
	}
	*inputPrice = &in
	if outputPrice != nil && (out > 0 || IsJevModel(canonical) || IsTextEmbeddingModel(canonical)) {
		o := out
		*outputPrice = &o
	}
	if cachePrice != nil && cache > 0 {
		cp := cache
		*cachePrice = &cp
	}
	if cacheCreationPrice != nil && cacheCreate > 0 {
		ccp := cacheCreate
		*cacheCreationPrice = &ccp
	}
	if groupRatio != nil && *groupRatio == nil {
		gr := 1.0
		*groupRatio = &gr
	}
	if pricingSource != nil && (*pricingSource == nil || **pricingSource == "") {
		src := "global"
		*pricingSource = &src
	}
}

func ApplyModelGroupRatioToRow(setting *string, modelName string,
	inputPrice, outputPrice, cachePrice, cacheCreationPrice, groupRatio **float64,
) {
	row := ChannelPricingLookupRow{}
	fields := []struct {
		pointer **float64
		value   *float64
	}{
		{inputPrice, &row.InputPrice}, {outputPrice, &row.OutputPrice},
		{cachePrice, &row.CachePrice}, {cacheCreationPrice, &row.CacheCreationPrice},
		{groupRatio, &row.GroupRatio},
	}
	for _, field := range fields {
		if field.pointer != nil && *field.pointer != nil {
			*field.value = **field.pointer
		}
	}
	ApplyModelGroupRatio(setting, modelName, &row)
	for _, field := range fields {
		if field.pointer != nil && *field.pointer != nil {
			// Replace pointers rather than modifying a shared SQL result.
			value := *field.value
			*field.pointer = &value
		}
	}
}

// ChannelDataUserPrice matches the admin page's input user price, including timed official pricing.
func ChannelDataUserPrice(r ChannelDataPricingRow, modelName string) *float64 {
	recharge := 1.0
	if r.RechargeRate != nil && *r.RechargeRate > 0 {
		recharge = *r.RechargeRate
	}
	markup := EffectiveModelPriceRatio(r.ModelPriceRatios, &r.ApimasterPriceRatio, modelName)
	input := r.InputPrice
	if timed, ok := DeepSeekV4OfficialPricingAt(modelName, time.Now()); ok {
		gr := 1.0
		if manual := EffectiveManualGroupRatio(r.Setting, modelName); manual > 0 {
			gr = manual
		} else if r.GroupRatio != nil && *r.GroupRatio > 0 {
			gr = *r.GroupRatio
		}
		value := timed.InputPrice * gr
		input = &value
	}
	// Media models display their base variant's configured price on the same page.
	gr := 1.0
	if manual := EffectiveManualGroupRatio(r.Setting, modelName); manual > 0 {
		gr = manual
	} else if r.GroupRatio != nil && *r.GroupRatio > 0 {
		gr = *r.GroupRatio
	}
	image, imageOK := ratio_setting.GetImageModelPricingDetails(modelName)
	if dto.IsMidjourneyImagineModel(modelName) {
		media, ok := ratio_setting.GetVideoModelPricingDetails(modelName)
		image.BasePrice, image.BaseVariant, image.Prices = media.BasePrice, media.BaseVariant, media.Prices
		imageOK = ok
	}
	if imageOK && image.BasePrice > 0 && len(image.Prices) > 0 {
		variant := strings.TrimSpace(image.BaseVariant)
		if variant == "" {
			variant = "1K"
		}
		supported := !IsGptImage2Family(modelName) || GptImage2ChannelSupportsResolution(&model.Channel{Id: r.ChannelID, OtherSettings: r.OtherSettings}, variant)
		if price := image.Prices[variant]; price > 0 && supported {
			value := price * gr * recharge * markup
			return &value
		}
	} else if video, ok := GlobalVideoMediaPricingUSD(modelName); ok && video.BasePrice > 0 && len(video.Prices) > 0 {
		variant := strings.TrimSpace(video.BaseVariant)
		if variant == "" {
			variant = "base"
		}
		if price := video.Prices[variant]; price > 0 {
			value := price * gr * recharge * markup
			return &value
		}
	}
	if input == nil {
		return nil
	}
	value := *input * recharge * markup
	return &value
}
