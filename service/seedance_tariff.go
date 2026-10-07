package service

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// PrepareSeedanceTaskBilling runs after protocol normalization on every channel.
// Only the public model and measured media work choose the platform tariff.
func PrepareSeedanceTaskBilling(c *gin.Context, info *relaycommon.RelayInfo) (map[string]float64, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, err
	}
	name := info.OriginModelName
	resolution, _ := req.Metadata["resolution"].(string)
	if resolution == "" {
		switch strings.ToLower(req.Size) {
		case "480p":
			resolution = "480P"
		case "1080p", "1920x1080", "1080x1920":
			resolution = "1080P"
		case "4k":
			resolution = "4K"
		default:
			resolution = "720P"
		}
	}
	seconds := req.Duration
	if seconds == 0 {
		seconds, _ = strconv.Atoi(req.Seconds)
	}
	if seconds == 0 {
		seconds = 5
	}
	auto, _ := req.Metadata["auto_duration"].(bool)
	if seconds == -1 {
		auto = true
		seconds = 30
	}
	fields := map[string]any{}
	if strings.Contains(c.GetHeader("Content-Type"), "application/json") {
		if err := common.UnmarshalBodyReusable(c, &fields); err != nil {
			return nil, err
		}
	}
	for _, key := range []string{"apimart_normalized_request", "seedance25_normalized_request"} {
		if v, ok := c.Get(key); ok {
			fields = v.(map[string]any)
			break
		}
	}
	if value, ok := fields["resolution"].(string); ok && strings.TrimSpace(value) != "" {
		resolution = value
	}
	if seedanceInt(fields["duration"]) == -1 {
		auto = true
		seconds = 30
	}
	// Provider adapters may retain references in metadata or native content.
	urls := seedanceTariffVideoURLs(fields)
	if len(urls) == 0 {
		urls = seedanceTariffVideoURLs(req.Metadata)
	}
	input := c.GetInt("seedance_video_input_seconds")
	if draft, ok := c.Get("seedance_draft_task"); ok {
		source := draft.(*model.Task)
		input = seedanceInt(source.PrivateData.SeedanceRequest["video_input_seconds"])
		if input == 0 {
			urls = seedanceTariffVideoURLs(source.PrivateData.SeedanceRequest)
		}
	}
	if input == 0 && len(urls) > 0 {
		// ValidateSeedanceVideoInputs deliberately skips upgrades; validate the
		// inherited references in a separate context while retaining account access.
		probe := c.Copy()
		probe.Request = c.Request
		delete(probe.Keys, "seedance_draft_task")
		if err := ValidateSeedanceVideoInputs(probe, map[string]any{"model": name, "prompt": "media billing", "video_urls": urls}); err != nil {
			return nil, err
		}
		input = probe.GetInt("seedance_video_input_seconds")
		if measurements, ok := probe.Get("seedance_video_input_measurements"); ok {
			c.Set("seedance_video_input_measurements", measurements)
		}
	}
	hasVideo, _ := req.Metadata["has_video"].(bool)
	if hasVideo && input == 0 {
		return nil, fmt.Errorf("Unable to verify reference video duration before billing")
	}
	variant := resolution
	if input > 0 {
		variant += "-input"
	}
	price, ok := ratio_setting.GetVideoModelPrice(name, variant)
	base, baseOK := ratio_setting.GetVideoModelBasePrice(name)
	if c.GetBool("trial_official_pricing") {
		price, ok = ratio_setting.GetVideoModelOfficialPrice(name, variant)
		base, baseOK = ratio_setting.GetVideoModelOfficialBasePrice(name)
	}
	if !ok || !baseOK || price <= 0 || base <= 0 {
		return nil, fmt.Errorf("Seedance price is not configured for %s", variant)
	}
	measurements, _ := c.Get("seedance_video_input_measurements")
	if draft, ok := c.Get("seedance_draft_task"); ok && measurements == nil {
		measurements = draft.(*model.Task).PrivateData.SeedanceRequest["input_video_measurements"]
	}
	c.Set("seedance_billing_snapshot", map[string]any{"tariff_unit_rate": price, "input_video_measurements": measurements, "duration": seconds, "video_input_seconds": input, "billing_variant": variant, "auto_duration": auto})
	return map[string]float64{"seconds": float64(seconds + input), "size": price / base}, nil
}

func seedanceTariffVideoURLs(fields map[string]any) []any {
	if fields == nil {
		return nil
	}
	var urls []any
	switch list := fields["video_urls"].(type) {
	case []any:
		urls = append(urls, list...)
	case []string:
		for _, u := range list {
			urls = append(urls, u)
		}
	}
	if len(urls) > 0 {
		return urls
	}
	if content, ok := fields["content"].([]any); ok {
		for _, item := range content {
			m, _ := item.(map[string]any)
			if m["type"] == "video_url" {
				v, _ := m["video_url"].(map[string]any)
				if u, ok := v["url"].(string); ok {
					urls = append(urls, u)
				}
			}
		}
	}
	return urls
}

func SeedanceSubmissionQuota(price types.PriceData) int {
	return seedanceQuotaFromRate(seedanceEffectiveRate(price.ModelPrice*price.GroupRatioInfo.GroupRatio*price.OtherRatios["size"]), int(price.OtherRatios["seconds"]))
}

func UsesSeedanceTariff(task *model.Task) bool {
	return task != nil && task.PrivateData.BillingContext != nil && task.PrivateData.BillingContext.SeedanceTariff && IsSeedanceLibraryModel(task.PrivateData.BillingContext.OriginModelName)
}

// SeedanceTariffSeconds measures output independently of provider cost or tokens.
func SeedanceTariffSeconds(task *model.Task, result *relaycommon.TaskInfo) int {
	if !UsesSeedanceTariff(task) {
		return 0
	}
	output := seedanceOutputDuration(task, result)
	return output.BillableSeconds + seedanceInt(task.PrivateData.SeedanceRequest["video_input_seconds"])
}

func SeedanceTariffQuota(task *model.Task, result *relaycommon.TaskInfo) int {
	seconds := SeedanceTariffSeconds(task, result)
	if seconds <= 0 {
		return 0
	}
	bc := task.PrivateData.BillingContext
	if task.Quota > 0 && bc.OtherRatios["seconds"] == float64(seconds) {
		return task.Quota
	}
	price := bc.ModelPrice
	for key, ratio := range bc.OtherRatios {
		if key != "seconds" && ratio > 0 {
			price *= ratio
		}
	}
	return seedanceQuotaFromRate(seedanceEffectiveRate(price*bc.GroupRatio), seconds)
}

// Freeze a decimal rate to 12 places, then round the total exactly once.
func seedanceEffectiveRate(rate float64) decimal.Decimal { return decimal.NewFromFloat(rate).Round(12) }
func seedanceQuotaFromRate(rate decimal.Decimal, seconds int) int {
	return int(rate.Mul(decimal.NewFromInt(int64(seconds))).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).Round(0).IntPart())
}
