package model

import (
	"errors"
	"sort"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm/clause"
)

const dailyStatsTimezoneOffsetSeconds int64 = 8 * 3600

// DailyStatsSummary mirrors the metrics in scripts/daily_stats_report.py.
// Day is Beijing midnight expressed as a Unix timestamp.
type DailyStatsSummary struct {
	Day                            int64   `json:"day" gorm:"primaryKey;not null"`
	UV                             int64   `json:"uv" gorm:"default:0"`
	PV                             int64   `json:"pv" gorm:"default:0"`
	TrafficAvailable               bool    `json:"traffic_available" gorm:"default:false"`
	RegistrationCount              int64   `json:"registration_count" gorm:"default:0"`
	TelegramRegistrationCount      int64   `json:"telegram_registration_count" gorm:"default:0"`
	GoogleRegistrationCount        int64   `json:"google_registration_count" gorm:"default:0"`
	ReferralRegistrationCount      int64   `json:"referral_registration_count" gorm:"default:0"`
	PayingUserCount                int64   `json:"paying_user_count" gorm:"default:0"`
	SameDayPayingRegistrationCount int64   `json:"same_day_paying_registration_count" gorm:"default:0"`
	PaidAmountUSD                  float64 `json:"paid_amount_usd" gorm:"type:decimal(20,6);default:0"`
	CommissionUSD                  float64 `json:"commission_usd" gorm:"type:decimal(20,6);default:0"`
	FirstPurchaseCount             int64   `json:"first_purchase_count" gorm:"default:0"`
	TrialClaimCount                int64   `json:"trial_claim_count" gorm:"default:0"`
	TrialRejectedCount             int64   `json:"trial_rejected_count" gorm:"default:0"`
	BenefitImpressionCount         int64   `json:"benefit_impression_count" gorm:"default:0"`
	BenefitClickCount              int64   `json:"benefit_click_count" gorm:"default:0"`
	OnboardingImpressionCount      int64   `json:"onboarding_impression_count" gorm:"default:0"`
	KeyUserCount                   int64   `json:"key_user_count" gorm:"default:0"`
	HomeClickCount                 int64   `json:"home_click_count" gorm:"default:0"`
	UpdatedAt                      int64   `json:"updated_at" gorm:"index;not null"`
}

type dailyStatsRegistrationRow struct {
	DayKey                    string
	RegistrationCount         int64
	TelegramRegistrationCount int64
	GoogleRegistrationCount   int64
	ReferralRegistrationCount int64
}

type dailyStatsCountRow struct {
	DayKey string
	Count  int64
}

type dailyStatsEventRow struct {
	DayKey                    string
	BenefitImpressionCount    int64
	BenefitClickCount         int64
	OnboardingImpressionCount int64
	HomeClickCount            int64
}

type dailyStatsTrafficRow struct {
	DayKey string
	UV     int64
	PV     int64
}

type dailyStatsUserRow struct {
	DayKey   string
	Username string
}

type dailyStatsTopupRow struct {
	UserID              int
	Username            string
	PaidAmountUSD       float64
	PaidAmountUSDSource string
	Money               float64
	PaymentProvider     string
	PaymentMethod       string
	CreateTime          int64
	CompleteTime        int64
}

func dailyStatsDayStart(unixSeconds int64) int64 {
	return ((unixSeconds + dailyStatsTimezoneOffsetSeconds) / 86400 * 86400) - dailyStatsTimezoneOffsetSeconds
}

func dailyStatsDayKey(day int64) string {
	return time.Unix(day, 0).In(time.FixedZone("Asia/Shanghai", int(dailyStatsTimezoneOffsetSeconds))).Format("2006-01-02")
}

func dailyStatsPaidAmount(row dailyStatsTopupRow) float64 {
	if row.PaidAmountUSD > 0 {
		return row.PaidAmountUSD
	}
	if row.PaidAmountUSD == 0 && row.PaidAmountUSDSource != "" {
		return 0
	}
	switch row.PaymentProvider {
	case PaymentProviderStripe, PaymentProviderPayPal, PaymentProviderClink, PaymentProviderCrypto, PaymentProviderCreem:
		if row.Money > 0 {
			return row.Money
		}
	case PaymentProviderFree:
		return 0
	}
	if row.PaymentMethod == PaymentMethodFree {
		return 0
	}
	return 0
}

func GetDailyStatsOldestRegistrationDay() (int64, error) {
	if APIMASTER_PG_DB == nil {
		return 0, errors.New("APIMASTER_PG_DSN is not configured")
	}
	var row struct {
		CreatedAt *time.Time
	}
	if err := APIMASTER_PG_DB.Table("users").Select("MIN(created_at) AS created_at").Scan(&row).Error; err != nil {
		return 0, err
	}
	if row.CreatedAt == nil {
		return dailyStatsDayStart(time.Now().Unix()), nil
	}
	return dailyStatsDayStart(row.CreatedAt.Unix()), nil
}

func EnsureDailyStatsSourceSchema() error {
	if APIMASTER_PG_DB == nil {
		return errors.New("APIMASTER_PG_DSN is not configured")
	}
	return APIMASTER_PG_DB.Exec(`CREATE TABLE IF NOT EXISTS ga_daily_traffic (
		day date PRIMARY KEY,
		uv bigint NOT NULL DEFAULT 0,
		pv bigint NOT NULL DEFAULT 0,
		updated_at timestamptz NOT NULL DEFAULT now()
	)`).Error
}

func BuildDailyStatsSummaries(startDay, endDay, updatedAt int64) ([]DailyStatsSummary, error) {
	if APIMASTER_PG_DB == nil {
		return nil, errors.New("APIMASTER_PG_DSN is not configured")
	}
	if err := EnsureDailyStatsSourceSchema(); err != nil {
		return nil, err
	}
	startDay = dailyStatsDayStart(startDay)
	endDay = dailyStatsDayStart(endDay)
	if endDay < startDay {
		return []DailyStatsSummary{}, nil
	}
	startTime := time.Unix(startDay, 0)
	endTime := time.Unix(endDay+86400, 0)

	byDay := make(map[int64]*DailyStatsSummary)
	for day := startDay; day <= endDay; day += 86400 {
		byDay[day] = &DailyStatsSummary{Day: day, UpdatedAt: updatedAt}
	}
	dayFromKey := func(key string) (int64, error) {
		parsed, err := time.ParseInLocation("2006-01-02", key, time.FixedZone("Asia/Shanghai", int(dailyStatsTimezoneOffsetSeconds)))
		if err != nil {
			return 0, err
		}
		return parsed.Unix(), nil
	}

	var registrations []dailyStatsRegistrationRow
	if err := APIMASTER_PG_DB.Raw(`
		SELECT TO_CHAR(u.created_at AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD') AS day_key,
		       COUNT(*)::bigint AS registration_count,
		       COUNT(*) FILTER (WHERE u.provider = 'telegram')::bigint AS telegram_registration_count,
		       COUNT(*) FILTER (WHERE u.provider = 'google')::bigint AS google_registration_count,
		       COUNT(inv.id)::bigint AS referral_registration_count
		FROM users u
		LEFT JOIN users inv ON inv.id = u.referred_by
		WHERE u.created_at >= ? AND u.created_at < ?
		GROUP BY 1`, startTime, endTime).Scan(&registrations).Error; err != nil {
		return nil, err
	}
	for _, row := range registrations {
		day, err := dayFromKey(row.DayKey)
		if err != nil {
			return nil, err
		}
		summary := byDay[day]
		if summary == nil {
			continue
		}
		summary.RegistrationCount = row.RegistrationCount
		summary.TelegramRegistrationCount = row.TelegramRegistrationCount
		summary.GoogleRegistrationCount = row.GoogleRegistrationCount
		summary.ReferralRegistrationCount = row.ReferralRegistrationCount
	}

	var users []dailyStatsUserRow
	if err := APIMASTER_PG_DB.Raw(`
		SELECT TO_CHAR(created_at AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD') AS day_key,
		       LEFT(REPLACE(id::text, '-', ''), 20) AS username
		FROM users
		WHERE created_at >= ? AND created_at < ?`, startTime, endTime).Scan(&users).Error; err != nil {
		return nil, err
	}
	registeredUsers := make(map[int64]map[string]struct{})
	for _, row := range users {
		day, err := dayFromKey(row.DayKey)
		if err != nil {
			return nil, err
		}
		if registeredUsers[day] == nil {
			registeredUsers[day] = make(map[string]struct{})
		}
		registeredUsers[day][row.Username] = struct{}{}
	}

	var events []dailyStatsEventRow
	if err := APIMASTER_PG_DB.Raw(`
		SELECT TO_CHAR(created_at AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD') AS day_key,
		       COUNT(*) FILTER (WHERE event IN ('impression', 'impression_usd', 'impression_token'))::bigint AS benefit_impression_count,
		       COUNT(*) FILTER (WHERE event IN ('claim', 'claim_usd', 'claim_token'))::bigint AS benefit_click_count,
		       COUNT(*) FILTER (WHERE event = 'onboarding_impression')::bigint AS onboarding_impression_count,
		       COUNT(*) FILTER (WHERE event = 'homeclick')::bigint AS home_click_count
		FROM claimfirstdeposit
		WHERE created_at >= ? AND created_at < ?
		GROUP BY 1`, startTime, endTime).Scan(&events).Error; err != nil {
		return nil, err
	}
	for _, row := range events {
		day, err := dayFromKey(row.DayKey)
		if err != nil {
			return nil, err
		}
		if summary := byDay[day]; summary != nil {
			summary.BenefitImpressionCount = row.BenefitImpressionCount
			summary.BenefitClickCount = row.BenefitClickCount
			summary.OnboardingImpressionCount = row.OnboardingImpressionCount
			summary.HomeClickCount = row.HomeClickCount
		}
	}

	var trialPopupEvents []dailyStatsEventRow
	if err := APIMASTER_PG_DB.Raw(`
		SELECT TO_CHAR(created_at AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD') AS day_key,
		       COUNT(*) FILTER (WHERE event = 'trial_popup_impression')::bigint AS benefit_impression_count,
		       COUNT(*) FILTER (WHERE event = 'trial_popup_register_click')::bigint AS benefit_click_count
		FROM trial_promo_events
		WHERE created_at >= ? AND created_at < ?
		GROUP BY 1`, startTime, endTime).Scan(&trialPopupEvents).Error; err != nil {
		return nil, err
	}
	for _, row := range trialPopupEvents {
		day, err := dayFromKey(row.DayKey)
		if err != nil {
			return nil, err
		}
		if summary := byDay[day]; summary != nil {
			summary.BenefitImpressionCount += row.BenefitImpressionCount
			summary.BenefitClickCount += row.BenefitClickCount
		}
	}

	var trialClaims []dailyStatsCountRow
	if err := APIMASTER_PG_DB.Raw(`
		SELECT TO_CHAR(claimed_at AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD') AS day_key,
		       COUNT(DISTINCT apimaster_user_id)::bigint AS count
		FROM trial_claims
		WHERE claim_status = 'granted' AND claimed_at >= ? AND claimed_at < ?
		GROUP BY 1`, startTime, endTime).Scan(&trialClaims).Error; err != nil {
		return nil, err
	}
	for _, row := range trialClaims {
		day, err := dayFromKey(row.DayKey)
		if err != nil {
			return nil, err
		}
		if summary := byDay[day]; summary != nil {
			summary.TrialClaimCount = row.Count
		}
	}

	var trialRejected []dailyStatsCountRow
	if err := APIMASTER_PG_DB.Raw(`
		SELECT TO_CHAR(u.created_at AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD') AS day_key,
		       COUNT(DISTINCT u.id)::bigint AS count
		FROM users u
		JOIN trial_claims tc ON tc.apimaster_user_id = u.id
		WHERE u.created_at >= ? AND u.created_at < ?
		  AND tc.risk_notified_at IS NOT NULL
		  AND (tc.claimed_at IS NULL OR tc.risk_notified_at < tc.claimed_at)
		GROUP BY 1`, startTime, endTime).Scan(&trialRejected).Error; err != nil {
		return nil, err
	}
	for _, row := range trialRejected {
		day, err := dayFromKey(row.DayKey)
		if err != nil {
			return nil, err
		}
		if summary := byDay[day]; summary != nil {
			summary.TrialRejectedCount = row.Count
		}
	}

	var traffic []dailyStatsTrafficRow
	trafficErr := APIMASTER_PG_DB.Raw(`
			SELECT TO_CHAR(day, 'YYYY-MM-DD') AS day_key,
			       COALESCE(SUM(uv), 0)::bigint AS uv,
			       COALESCE(SUM(pv), 0)::bigint AS pv
			FROM ga_daily_traffic
			WHERE day >= ? AND day <= ?
			GROUP BY day`, dailyStatsDayKey(startDay), dailyStatsDayKey(endDay)).Scan(&traffic).Error
	if trafficErr == nil {
		for _, row := range traffic {
			day, err := dayFromKey(row.DayKey)
			if err != nil {
				return nil, err
			}
			if summary := byDay[day]; summary != nil {
				summary.UV = row.UV
				summary.PV = row.PV
				summary.TrafficAvailable = true
			}
		}
	} else {
		common.SysLog("daily-stats: GA traffic cache unavailable: " + trafficErr.Error())
	}

	var topups []dailyStatsTopupRow
	if err := DB.Table("top_ups t").
		Select(`t.user_id, u.username, t.paid_amount_usd, t.paid_amount_usd_source,
		        t.money, t.payment_provider, t.payment_method, t.create_time, t.complete_time`).
		Joins("JOIN users u ON u.id = t.user_id").
		Where("t.status = ? AND (t.paid_amount_usd > 0 OR t.money > 0)", common.TopUpStatusSuccess).
		Where("COALESCE(NULLIF(t.complete_time, 0), t.create_time) >= ?", startDay).
		Where("COALESCE(NULLIF(t.complete_time, 0), t.create_time) < ?", endDay+86400).
		Scan(&topups).Error; err != nil {
		return nil, err
	}
	payingUsers := make(map[int64]map[int]struct{})
	payingUsernames := make(map[int64]map[string]struct{})
	for _, row := range topups {
		effectiveTime := row.CompleteTime
		if effectiveTime == 0 {
			effectiveTime = row.CreateTime
		}
		day := dailyStatsDayStart(effectiveTime)
		summary := byDay[day]
		if summary == nil {
			continue
		}
		summary.PaidAmountUSD += dailyStatsPaidAmount(row)
		if payingUsers[day] == nil {
			payingUsers[day] = make(map[int]struct{})
		}
		payingUsers[day][row.UserID] = struct{}{}
		if payingUsernames[day] == nil {
			payingUsernames[day] = make(map[string]struct{})
		}
		payingUsernames[day][row.Username] = struct{}{}
	}
	for day, userIDs := range payingUsers {
		byDay[day].PayingUserCount = int64(len(userIDs))
	}
	for day, usernames := range payingUsernames {
		for username := range usernames {
			if _, ok := registeredUsers[day][username]; ok {
				byDay[day].SameDayPayingRegistrationCount++
			}
		}
	}

	var firstPurchases []struct {
		UserID  int
		FirstTS int64
	}
	if err := DB.Table("top_ups").
		Select("user_id, MIN(COALESCE(NULLIF(complete_time, 0), create_time)) AS first_ts").
		Where("status = ? AND (paid_amount_usd > 0 OR money > 0)", common.TopUpStatusSuccess).
		Group("user_id").
		Scan(&firstPurchases).Error; err != nil {
		return nil, err
	}
	for _, row := range firstPurchases {
		day := dailyStatsDayStart(row.FirstTS)
		if summary := byDay[day]; summary != nil {
			summary.FirstPurchaseCount++
		}
	}

	var commissions []struct {
		CreatedAt  int64
		Commission int64
	}
	if err := DB.Table("aff_logs").
		Select("created_at, commission").
		Where("created_at >= ? AND created_at < ?", startDay, endDay+86400).
		Scan(&commissions).Error; err != nil {
		return nil, err
	}
	for _, row := range commissions {
		if summary := byDay[dailyStatsDayStart(row.CreatedAt)]; summary != nil {
			summary.CommissionUSD += float64(row.Commission) / common.QuotaPerUnit
		}
	}

	var keyUsernames []string
	if err := DB.Table("users u").
		Distinct("u.username").
		Joins("JOIN tokens t ON t.user_id = u.id").
		Where("u.username <> ''").
		Scan(&keyUsernames).Error; err != nil {
		return nil, err
	}
	keyUsers := make(map[string]struct{}, len(keyUsernames))
	for _, username := range keyUsernames {
		keyUsers[username] = struct{}{}
	}
	for day, usernames := range registeredUsers {
		for username := range usernames {
			if _, ok := keyUsers[username]; ok {
				byDay[day].KeyUserCount++
			}
		}
	}

	rows := make([]DailyStatsSummary, 0, len(byDay))
	for _, row := range byDay {
		rows = append(rows, *row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Day < rows[j].Day })
	return rows, nil
}

func UpsertDailyStatsSummaries(rows []DailyStatsSummary) error {
	if len(rows) == 0 {
		return nil
	}
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "day"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"uv", "pv", "traffic_available", "registration_count",
			"telegram_registration_count", "google_registration_count",
			"referral_registration_count", "paying_user_count",
			"same_day_paying_registration_count", "paid_amount_usd",
			"commission_usd", "first_purchase_count", "trial_claim_count",
			"trial_rejected_count", "benefit_impression_count",
			"benefit_click_count", "onboarding_impression_count",
			"key_user_count", "home_click_count", "updated_at",
		}),
	}).CreateInBatches(&rows, 50).Error
}

func GetDailyStatsSummaries(startTimestamp, endTimestamp int64) ([]DailyStatsSummary, error) {
	query := DB.Model(&DailyStatsSummary{})
	if startTimestamp > 0 {
		query = query.Where("day >= ?", dailyStatsDayStart(startTimestamp))
	}
	if endTimestamp > 0 {
		query = query.Where("day <= ?", dailyStatsDayStart(endTimestamp))
	}
	var rows []DailyStatsSummary
	err := query.Order("day DESC").Find(&rows).Error
	return rows, err
}
