package model

import (
	"errors"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// TopupHistoryFilter uses inclusive Beijing calendar dates and creation time,
// matching the Time column. User-scoped history retains its existing 30-day limit.
type TopupHistoryFilter struct {
	UserID                                          int
	Keyword, Status, PaymentMethod, TransactionType string
	StartTime, EndTime                              int64
}

type TopupHistorySummary struct {
	Count       int64  `json:"count"`
	RechargeUSD string `json:"recharge_usd"`
}

func ParseTopupHistoryDates(start, end string) (int64, int64, error) {
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	var from, until int64
	for _, value := range []struct {
		raw    string
		target *int64
		end    bool
	}{{start, &from, false}, {end, &until, true}} {
		if value.raw == "" {
			continue
		}
		date, err := time.ParseInLocation("2006-01-02", value.raw, zone)
		if err != nil {
			return 0, 0, errors.New("invalid transaction history date")
		}
		if value.end {
			date = date.AddDate(0, 0, 1)
		}
		*value.target = date.Unix()
	}
	if from > 0 && until > 0 && from >= until {
		return 0, 0, errors.New("start date must not exceed end date")
	}
	return from, until, nil
}

func topupHistoryQuery(db *gorm.DB, f TopupHistoryFilter) (*gorm.DB, error) {
	q := db.Model(&TopUp{})
	if f.UserID > 0 {
		q = q.Where("user_id = ? AND create_time >= ?", f.UserID, topUpQueryCutoff())
	}
	if f.StartTime > 0 {
		q = q.Where("create_time >= ?", f.StartTime)
	}
	if f.EndTime > 0 {
		q = q.Where("create_time < ?", f.EndTime)
	}
	if f.Keyword != "" {
		pattern, err := sanitizeLikePattern(f.Keyword)
		if err != nil {
			return nil, err
		}
		if f.UserID == 0 && isDigitOnly(f.Keyword) {
			q = q.Where("user_id = ?", f.Keyword)
		} else if f.UserID == 0 && len(f.Keyword) > 1 && f.Keyword[0] != '@' && strings.Contains(f.Keyword, "@") {
			q = q.Where("user_id IN (SELECT id FROM users WHERE email LIKE ? ESCAPE '!')", pattern)
		} else {
			q = q.Where("trade_no LIKE ? ESCAPE '!'", pattern)
		}
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.PaymentMethod != "" {
		q = q.Where("payment_method = ?", f.PaymentMethod)
	}
	return applyTopupTransactionTypeFilter(q, f.TransactionType), nil
}

// QueryTopupHistory computes its summary over every matching successful wallet
// recharge, before pagination. Never adds different settlement currencies.
func QueryTopupHistory(f TopupHistoryFilter, page *common.PageInfo) (rows []*TopUp, total int64, summary TopupHistorySummary, err error) {
	summary.RechargeUSD = "0"
	err = DB.Transaction(func(tx *gorm.DB) error {
		query, e := topupHistoryQuery(tx, f)
		if e != nil {
			return e
		}
		if e = query.Session(&gorm.Session{}).Count(&total).Error; e != nil {
			return e
		}
		if e = query.Session(&gorm.Session{}).Order("id desc").Limit(page.GetPageSize()).Offset(page.GetStartIdx()).Find(&rows).Error; e != nil {
			return e
		}
		paidQuery := applyTopupTransactionTypeFilter(query.Session(&gorm.Session{}), TopupTransactionTypeWallet).
			Where("status = ? AND payment_method <> ? AND payment_provider <> ?", common.TopUpStatusSuccess, PaymentMethodFree, PaymentProviderFree)
		var aggregate struct {
			Count int64
			Total decimal.Decimal
		}
		if e = paidQuery.Select("COUNT(*) AS count, COALESCE(SUM(CASE WHEN credited_amount > 0 THEN credited_amount ELSE amount END), 0) AS total").Scan(&aggregate).Error; e != nil {
			return e
		}
		summary.Count = aggregate.Count
		summary.RechargeUSD = aggregate.Total.Round(6).String()

		return nil
	})
	return
}

func ExportTopupHistory(f TopupHistoryFilter) (rows []*TopUp, err error) {
	query, err := topupHistoryQuery(DB, f)
	if err != nil {
		return nil, err
	}
	err = query.Order("id desc").Find(&rows).Error
	return
}
