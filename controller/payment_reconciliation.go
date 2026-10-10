package controller

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stripe/stripe-go/v81"
	stripesession "github.com/stripe/stripe-go/v81/checkout/session"
	waffoorder "github.com/waffo-com/waffo-go/types/order"
	"gorm.io/gorm"
)

var reconciliationTimezone = time.FixedZone("UTC+8", 8*3600)
var reconciliationOnce sync.Once
var queryClinkReconciliationOrder = service.GetClinkOrder
var queryWaffoReconciliationPayment = service.QueryWaffoReconciliationPayment

type reconciliationCandidate struct {
	trade, provider, method, purpose, status, queryID, currency, payload string
	clinkSessionID                                                       string
	user                                                                 int
	money                                                                float64
	top                                                                  *model.TopUp
}
type reconciliationProof struct {
	id, status, currency, amount, problem  string
	paid, known                            bool
	refundVerified, refundRecoveryVerified bool
	refundUncreditedVerified               bool
}

func reconciliationRange(start, end string) (time.Time, time.Time, error) {
	now := time.Now().In(reconciliationTimezone)
	if start == "" {
		start = now.AddDate(0, 0, -1).Format("2006-01-02")
	}
	if end == "" {
		end = start
	}
	a, e := time.ParseInLocation("2006-01-02", start, reconciliationTimezone)
	if e != nil {
		return a, a, errors.New("invalid start date")
	}
	b, e := time.ParseInLocation("2006-01-02", end, reconciliationTimezone)
	if e != nil {
		return a, b, errors.New("invalid end date")
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, reconciliationTimezone)
	if b.Before(a) || b.Sub(a) > 30*24*time.Hour || b.After(today) {
		return a, b, errors.New("select at most 31 days through today")
	}
	return a, b, nil
}
func reconciliationProviders(provider string) ([]string, error) {
	if provider == "" || provider == "all" {
		return append([]string{}, model.PaymentReconciliationProviders...), nil
	}
	for _, p := range append([]string{}, model.PaymentReconciliationProviders...) {
		if p == provider {
			return []string{p}, nil
		}
	}
	return nil, errors.New("invalid payment provider")
}
func GetPaymentReconciliation(c *gin.Context) {
	a, b, err := reconciliationRange(c.Query("start_date"), c.Query("end_date"))
	if err != nil {
		c.JSON(400, gin.H{"success": false, "message": err.Error()})
		return
	}
	providers, err := reconciliationProviders(c.Query("provider"))
	if err != nil {
		c.JSON(400, gin.H{"success": false, "message": err.Error()})
		return
	}
	var jobs []model.PaymentReconciliationJob
	if err = model.DB.Where("day >= ? AND day <= ? AND provider IN ?", a.Format("2006-01-02"), b.Format("2006-01-02"), providers).Order("day DESC, provider ASC").Find(&jobs).Error; err != nil {
		common.ApiErrorMsg(c, "Could not load reconciliation")
		return
	}
	ids := []int{}
	for _, j := range jobs {
		if j.RunID > 0 {
			ids = append(ids, j.RunID)
		}
	}
	runs := []model.PaymentReconciliationRun{}
	items := []model.PaymentReconciliationItem{}
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	if page > 100000 {
		page = 100000
	}
	var itemsTotal int64
	if len(ids) > 0 {
		if model.DB.Where("id IN ?", ids).Order("day DESC, provider ASC").Find(&runs).Error != nil {
			common.ApiErrorMsg(c, "Could not load reconciliation")
			return
		}
		if model.DB.Model(&model.PaymentReconciliationItem{}).Where("run_id IN ? AND result <> ? AND manual_confirmed_at = 0", ids, "matched").Count(&itemsTotal).Error != nil {
			common.ApiErrorMsg(c, "Could not count differences")
			return
		}
		// Paginated issues; summaries always cover every checked order.
		if model.DB.Where("run_id IN ? AND result <> ? AND manual_confirmed_at = 0", ids, "matched").Order("id ASC").Offset((page-1)*100).Limit(100).Find(&items).Error != nil {
			common.ApiErrorMsg(c, "Could not load differences")
			return
		}
	}
	// Count successful matches from immutable audit items, including historical runs.
	matchedCounts := map[int]int{}
	if len(ids) > 0 {
		var counts []struct {
			RunID                      int
			ManualMatchedCount         int
			MatchedCount               int
			RefundMatchedCount         int
			PriorStatementMatchedCount int
		}
		if model.DB.Model(&model.PaymentReconciliationItem{}).Select("run_id, COUNT(*) AS matched_count, SUM(CASE WHEN manual_confirmed_at > 0 THEN 1 ELSE 0 END) AS manual_matched_count, SUM(CASE WHEN verification = 'refund_matched' THEN 1 ELSE 0 END) AS refund_matched_count, SUM(CASE WHEN verification = 'prior_creation_statement' THEN 1 ELSE 0 END) AS prior_statement_matched_count").Where("run_id IN ? AND (result = ? OR manual_confirmed_at > 0) AND purpose <> ?", ids, "matched", "coverage").Group("run_id").Scan(&counts).Error != nil {
			common.ApiErrorMsg(c, "Could not count reconciled orders")
			return
		}
		for _, count := range counts {
			matchedCounts[count.RunID] = count.MatchedCount
			for index := range runs {
				if runs[index].ID == count.RunID {
					runs[index].ManualMatchedCount = count.ManualMatchedCount
					runs[index].RefundMatchedCount = count.RefundMatchedCount
					runs[index].PriorStatementMatchedCount = count.PriorStatementMatchedCount
				}
			}
		}
	}
	truncated := int64(page*100) < itemsTotal
	for i := range runs {
		runs[i].MatchedCount = matchedCounts[runs[i].ID]
		runs[i].Totals = []model.PaymentReconciliationTotal{}
		_ = common.UnmarshalJsonStr(runs[i].TotalsJSON, &runs[i].Totals)
		sort.Slice(runs[i].Totals, func(a, b int) bool { return runs[i].Totals[a].Currency < runs[i].Totals[b].Currency })
	}
	modes := map[string]string{}
	for _, provider := range providers {
		modes[provider] = "bidirectional_official_statement"
		if model.PaymentReconciliationLocalOnly(provider) {
			modes[provider] = "local_successful_orders_only"
		}
	}
	c.JSON(200, gin.H{"success": true, "provider_modes": modes, "jobs": jobs, "runs": runs, "items": items, "items_truncated": truncated, "items_total": itemsTotal, "page": page, "timezone": "UTC+8", "schedule": "08:30", "scope": "provider_specific_successful_payment_reconciliation", "coverage": "provider_specific"})
}
func QueuePaymentReconciliation(c *gin.Context) {
	var req struct {
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
		Provider  string `json:"provider"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(400, gin.H{"success": false, "message": "invalid request"})
		return
	}
	a, b, err := reconciliationRange(req.StartDate, req.EndDate)
	if err != nil {
		c.JSON(400, gin.H{"success": false, "message": err.Error()})
		return
	}
	ps, err := reconciliationProviders(req.Provider)
	if err != nil {
		c.JSON(400, gin.H{"success": false, "message": err.Error()})
		return
	}
	n := 0
	for d := a; !d.After(b); d = d.AddDate(0, 0, 1) {
		for _, p := range ps {
			if err = model.QueuePaymentReconciliation(d.Format("2006-01-02"), p, c.GetInt("id"), true); err != nil {
				c.JSON(http.StatusConflict, gin.H{"success": false, "message": "Reconciliation is already running; refresh for progress", "queued": n})
				return
			}
			n++
		}
	}
	c.JSON(202, gin.H{"success": true, "queued": n})
}
func StartPaymentReconciliationTask() {
	reconciliationOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		go func() {
			for {
				runPaymentReconciliationCycle()
				time.Sleep(time.Minute)
			}
		}()
	})
}
func runPaymentReconciliationCycle() {
	now := time.Now().In(reconciliationTimezone)
	if now.Hour() > 8 || (now.Hour() == 8 && now.Minute() >= 30) {
		day := now.AddDate(0, 0, -1).Format("2006-01-02")
		for _, p := range append([]string{}, model.PaymentReconciliationProviders...) {
			if err := model.QueuePaymentReconciliation(day, p, 0, false); err != nil {
				common.SysError("daily payment reconciliation scheduling failed")
			}
		}
	}
	for k := 0; k < 20; k++ {
		j, err := model.ClaimPaymentReconciliationJob(time.Now().Unix())
		if err != nil {
			common.SysError("daily payment reconciliation claim failed")
			return
		}
		if j == nil {
			return
		}
		items, err := reconcilePaymentDay(j)
		if err != nil {
			items = append(items, model.PaymentReconciliationItem{Result: "unverified", Problem: "candidate_load_failed", CheckedAt: time.Now().Unix()})
		}
		if model.FinishPaymentReconciliation(j, items, err != nil) != nil {
			common.SysError("daily payment reconciliation persistence failed; lease recovery will retry")
		}
	}
}
func reconcilePaymentDay(j *model.PaymentReconciliationJob) ([]model.PaymentReconciliationItem, error) {
	day, err := time.ParseInLocation("2006-01-02", j.Day, reconciliationTimezone)
	if err != nil {
		return nil, err
	}
	rows, err := loadReconciliationCandidates(day.Unix(), day.AddDate(0, 0, 1).Unix(), j.Provider)
	if err != nil {
		return nil, err
	}
	// Clink checkout sessions expire even after payment. Resolve stable order
	// IDs from the independent daily statement before verifying each order.
	var upstream []service.StatementPayment
	var statementErr error
	if j.Provider == "clink" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		upstream, statementErr = service.ListReconciliationPayments(ctx, j.Provider, day, day.AddDate(0, 0, 1))
		cancel()
		for index := range rows {
			for _, payment := range upstream {
				if payment.TradeNo == rows[index].trade {
					if strings.HasPrefix(rows[index].queryID, "sess_") {
						rows[index].clinkSessionID = rows[index].queryID
					}
					rows[index].queryID = payment.ID
					break
				}
			}
		}
	}
	items := make([]model.PaymentReconciliationItem, 0, len(rows))
	for _, row := range rows {
		if err = model.HeartbeatPaymentReconciliation(j, time.Now().Unix()); err != nil {
			return items, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		proof := queryReconciliationOrder(ctx, row)
		cancel()
		items = append(items, classifyReconciliationOrder(row, proof))
		time.Sleep(500 * time.Millisecond)
	}
	if model.PaymentReconciliationLocalOnly(j.Provider) {
		return items, nil
	}
	if j.Provider != "clink" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		upstream, statementErr = service.ListReconciliationPayments(ctx, j.Provider, day, day.AddDate(0, 0, 1))
		cancel()
	}
	items, err = mergeReconciliationStatement(j.Provider, items, upstream)
	if err != nil {
		return items, err
	}
	if statementErr != nil {
		items = append(items, model.PaymentReconciliationItem{Result: "unverified", Problem: "official_statement_unavailable", Purpose: "coverage", CheckedAt: time.Now().Unix()})
	} else {
		// Official day totals come from the independently enumerated statement,
		// not the present status of an order paid outside the selected day.
		for index, i := range items {
			found := false
			for _, p := range upstream {
				if p.ID == i.OfficialID || (p.TradeNo != "" && p.TradeNo == i.TradeNo) {
					found = true
					break
				}
			}
			if i.OfficialPaid && !found {
				if j.Provider == "platega" && i.Result == "matched" {
					creationTime, err := reconciliationCreationTime(i)
					if err != nil {
						return items, err
					}
					order := model.GetPlategaOrderByTradeNo(i.TradeNo)
					if order == nil {
						return items, errors.New("Platega creation invoice unavailable")
					}
					bill, billErr := service.ExpectedPlategaBill(order)
					created := time.Unix(creationTime, 0).UTC().Truncate(24 * time.Hour)
					ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
					historical, e := service.ListReconciliationPayments(ctx, "platega", created, created.Add(24*time.Hour))
					cancel()
					for _, p := range historical {
						if e == nil && billErr == nil && matchesPriorPlategaStatement(i, creationTime, day, p, decimal.NewFromFloat(bill).String()) {
							// Export filters creation date, unlike local completion date.
							// Retain verified late credits without counting them in the day's creation totals.
							items[index].LocalPaid = false
							items[index].OfficialPaid = false
							items[index].Verification = "prior_creation_statement"
							found = true
							break
						}
					}
					if found {
						continue
					}
				}
				items[index].OfficialPaid = false
				items[index].Result = "unverified"
				items[index].Problem = "official_paid_absent_from_daily_statement"
			}
		}
	}

	return items, nil
}

func reconciliationCreationTime(i model.PaymentReconciliationItem) (int64, error) {
	if i.Purpose == "subscription" {
		var row model.SubscriptionOrder
		err := model.DB.Where("trade_no = ?", i.TradeNo).First(&row).Error
		return row.CreateTime, err
	}
	var row model.TopUp
	err := model.DB.Where("trade_no = ?", i.TradeNo).First(&row).Error
	return row.CreateTime, err
}

func matchesPriorPlategaStatement(i model.PaymentReconciliationItem, created int64, day time.Time, p service.StatementPayment, expectedBill string) bool {
	actual, e1 := decimal.NewFromString(p.Amount)
	// Export amounts include payer fees; the audit's OfficialAmount is the
	// merchant base amount, normalized only after the live bill was verified.
	expected, e2 := decimal.NewFromString(expectedBill)
	return e1 == nil && e2 == nil && actual.Equal(expected) && actual.Sign() > 0 && p.ID == i.OfficialID && p.TradeNo == i.TradeNo && p.Currency == i.OfficialCurrency && p.CreatedAt == created && p.Status == "CONFIRMED" && created > 0 && (created < day.Unix() || created >= day.AddDate(0, 0, 1).Unix())
}

// Look outside the day's local cohort: upstream payment can settle an older
// failed order or have no local order at all. Never infer absence from a DB error.
func mergeReconciliationStatement(provider string, items []model.PaymentReconciliationItem, payments []service.StatementPayment) ([]model.PaymentReconciliationItem, error) {
	seen := map[string]bool{}
	seenTrade := map[string]bool{}
	for _, payment := range payments {
		if seen[payment.ID] {
			continue
		}
		seen[payment.ID] = true
		trade := payment.TradeNo
		if trade == "" && provider == "paypal" {
			var t model.TopUp
			e := model.DB.Where("paypal_capture_id = ?", payment.ID).First(&t).Error
			if e == nil {
				trade = t.TradeNo
			} else if !errors.Is(e, gorm.ErrRecordNotFound) {
				return items, e
			}
		}
		if trade == "" && provider == "platega" {
			var o model.PlategaOrder
			err := model.DB.Where("platega_transaction_id = ?", payment.ID).First(&o).Error
			if err == nil {
				trade = o.TradeNo
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return items, err
			}
		}
		if trade != "" && seenTrade[trade] {
			items = append(items, model.PaymentReconciliationItem{TradeNo: trade, Purpose: "unknown", OfficialID: payment.ID, OfficialPaid: true, OfficialStatus: payment.Status, OfficialAmount: payment.Amount, OfficialCurrency: payment.Currency, Result: "difference", Problem: "official_duplicate_payment_for_order", CheckedAt: time.Now().Unix()})
			continue
		}
		if trade != "" {
			seenTrade[trade] = true
		}
		matched := false
		for index, i := range items {
			if (trade != "" && i.TradeNo == trade) || (i.OfficialID != "" && i.OfficialID == payment.ID) {
				// A successful statement entry is independent evidence even when
				// a legacy order cannot be queried by its saved identifier.
				if i.Result == "unverified" && i.Purpose != "coverage" && i.LocalStatus != "refunded" && !payment.RefundOnly && !strings.Contains(strings.ToLower(i.OfficialStatus), "refund") {
					items[index].OfficialPaid = true
					items[index].OfficialID = payment.ID
					items[index].OfficialStatus = payment.Status
					items[index].OfficialCurrency = payment.Currency
					items[index].OfficialAmount = payment.Amount
					if provider == "platega" {
						items[index].OfficialAmount = ""
					}
					if !i.LocalPaid && provider != "paypal" {
						items[index].Result = "difference"
						items[index].Problem = "official_paid_local_not_success"
					}
				}
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		i := model.PaymentReconciliationItem{TradeNo: trade, Purpose: "unknown", LocalStatus: "missing", OfficialID: payment.ID, OfficialStatus: payment.Status, OfficialPaid: true, OfficialAmount: payment.Amount, OfficialCurrency: payment.Currency, Result: "difference", Problem: "official_paid_local_order_missing", CheckedAt: time.Now().Unix()}
		if trade == "" {
			i.Problem = "official_paid_local_reference_unresolved"
			i.Result = "unverified"
		}
		if trade != "" {
			var top model.TopUp
			var sub model.SubscriptionOrder
			a := model.DB.Where("trade_no = ?", trade).First(&top).Error
			b := model.DB.Where("trade_no = ?", trade).First(&sub).Error
			if a != nil && !errors.Is(a, gorm.ErrRecordNotFound) {
				return items, a
			}
			if b != nil && !errors.Is(b, gorm.ErrRecordNotFound) {
				return items, b
			}
			var row reconciliationCandidate
			if a == nil {
				row = reconciliationCandidate{trade: trade, provider: normalizeReconciliationProvider(top.PaymentProvider, top.PaymentMethod), method: top.PaymentMethod, purpose: "wallet", status: top.Status, user: top.UserId, money: top.Money, top: &top, currency: payment.Currency}
			}
			if b == nil {
				row = reconciliationCandidate{trade: trade, provider: normalizeReconciliationProvider(sub.PaymentProvider, sub.PaymentMethod), method: sub.PaymentMethod, purpose: "subscription", status: sub.Status, user: sub.UserId, money: sub.Money, top: row.top, currency: payment.Currency, payload: sub.ProviderPayload}
				if sub.OrderType == "wallet_transfer" {
					row.purpose = "wallet"
				}
			}
			if a == nil || b == nil {
				if row.provider != provider {
					i.Problem = "official_identity_mismatch"
				} else {
					row.queryID = payment.ID
					if provider == "platega" {
						if o := model.GetPlategaOrderByTradeNo(trade); o != nil {
							row.money = o.RubAmount
						}
					}
					proof := reconciliationProof{id: payment.ID, status: payment.Status, currency: payment.Currency, amount: payment.Amount, paid: true, known: true}
					row.currency = "USD"
					if provider == "epay" {
						row.currency = "CNY"
					}
					if provider == "platega" {
						row.currency = "RUB"
					}
					var ref model.PaymentQueryReference
					if e := model.DB.Where("trade_no = ? AND provider = ?", trade, provider).First(&ref).Error; e == nil {
						if ref.Currency != "" {
							row.currency = ref.Currency
						}
						if provider == "clink" && strings.HasPrefix(ref.QueryID, "sess_") {
							row.clinkSessionID = ref.QueryID
						}
					}
					if provider == "platega" || provider == "clink" || provider == "paypal" || payment.RefundOnly {
						ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
						proof = queryReconciliationOrder(ctx, row)
						cancel()
					}

					i = classifyReconciliationOrder(row, proof)
					if i.Verification == "refund_matched" && row.status == "refunded" && !payment.RefundOnly {
						// Original gross payment still belongs to this statement day.
						i.LocalPaid = true
						i.OfficialPaid = true
					}
					// An independently enumerated successful statement remains
					// in scope even when a second query fails or contradicts it.
					if !i.OfficialPaid && i.Verification != "refund_matched" && !payment.RefundOnly {
						i.OfficialPaid = true
						i.OfficialID = payment.ID
						i.OfficialStatus = payment.Status
						i.OfficialAmount = payment.Amount
						i.OfficialCurrency = payment.Currency
						if proof.known && !proof.paid && proof.problem == "" && proof.status != "REFUNDED" {
							i.Result = "unverified"
							i.Problem = "official_statement_query_conflict"
						}
					}
					if i.Result == "matched" && i.LocalPaid && i.Verification != "refund_matched" {
						i.Result = "difference"
						i.Problem = "official_paid_local_date_mismatch"
					}
				}
			}
		}
		items = append(items, i)
	}
	return items, nil
}
func normalizeReconciliationProvider(provider, method string) string {
	if provider != "" {
		return provider
	}
	if method == "alipay" || method == "wxpay" {
		return "epay"
	}
	// Recognized historical methods remain identifiable even when disabled for reconciliation.
	for _, p := range []string{"epay", "platega", "stripe", "paypal", "creem", "clink", "waffo", "waffo_pancake", "nowpayments", "crypto"} {
		if p == method {
			return p
		}
	}
	return "unknown"
}
func loadReconciliationCandidates(start, end int64, provider string) ([]reconciliationCandidate, error) {
	var tops []model.TopUp
	var subs []model.SubscriptionOrder
	// Start with local successes only. Failed/pending/missing local orders
	// enter independently through the provider's successful payment statement.
	scope := "status = ? AND ((complete_time >= ? AND complete_time < ?) OR ((complete_time IS NULL OR complete_time <= 0) AND create_time >= ? AND create_time < ?))"
	if provider == "platega" {
		scope = "status = ? AND ((complete_time >= ? AND complete_time < ?) OR (create_time >= ? AND create_time < ?))"
	}
	if err := model.DB.Where(scope, common.TopUpStatusSuccess, start, end, start, end).Find(&tops).Error; err != nil {
		return nil, err
	}
	if err := model.DB.Where(scope, common.TopUpStatusSuccess, start, end, start, end).Find(&subs).Error; err != nil {
		return nil, err
	}
	rows := map[string]reconciliationCandidate{}
	for i := range tops {
		t := tops[i]
		if provider != "platega" && t.Status == "success" && t.CompleteTime > 0 && (t.CompleteTime < start || t.CompleteTime >= end) {
			continue
		}
		p := normalizeReconciliationProvider(t.PaymentProvider, t.PaymentMethod)
		if p == "free" {
			continue
		}
		rows[t.TradeNo] = reconciliationCandidate{trade: t.TradeNo, provider: p, method: t.PaymentMethod, purpose: "wallet", status: t.Status, user: t.UserId, money: t.Money, top: &t}
	}
	for _, s := range subs {
		if provider != "platega" && s.Status == "success" && s.CompleteTime > 0 && (s.CompleteTime < start || s.CompleteTime >= end) {
			continue
		}
		p := normalizeReconciliationProvider(s.PaymentProvider, s.PaymentMethod)
		if p == "free" {
			continue
		}
		r := reconciliationCandidate{trade: s.TradeNo, provider: p, method: s.PaymentMethod, purpose: "subscription", status: s.Status, user: s.UserId, money: s.Money, payload: s.ProviderPayload}
		if s.OrderType == "wallet_transfer" {
			r.purpose = "wallet"
		}
		if t, ok := rows[s.TradeNo]; ok {
			r.top = t.top
		}
		rows[s.TradeNo] = r
	}
	result := []reconciliationCandidate{}
	for _, r := range rows {
		if r.provider != provider {
			continue
		}
		r.currency = "USD"
		if provider == "epay" {
			r.currency = "CNY"
		}
		if provider == "platega" {
			r.currency = "RUB"
			if o := model.GetPlategaOrderByTradeNo(r.trade); o != nil {
				r.money = o.RubAmount
			}
		}
		var ref model.PaymentQueryReference
		if err := model.DB.Where("trade_no = ? AND provider = ?", r.trade, provider).First(&ref).Error; err == nil {
			r.queryID = ref.QueryID
			if ref.Currency != "" {
				r.currency = ref.Currency
			}
		}
		if provider == "crypto" {
			r.money = decimal.NewFromFloat(r.money).Round(6).InexactFloat64()
		}
		result = append(result, r)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].trade < result[j].trade })
	return result, nil
}
func classifyReconciliationOrder(r reconciliationCandidate, p reconciliationProof) model.PaymentReconciliationItem {
	var amountProblem string
	if r.provider == model.PaymentProviderEpay && r.purpose == "subscription" {
		r, amountProblem = reconciliationEpaySubscriptionAmount(r)
	}
	i := model.PaymentReconciliationItem{TradeNo: r.trade, UserID: r.user, Purpose: r.purpose, LocalStatus: r.status, LocalPaid: r.status == "success", LocalAmount: decimal.NewFromFloat(r.money).String(), Currency: r.currency, OfficialID: p.id, OfficialStatus: p.status, OfficialPaid: p.paid, OfficialAmount: p.amount, OfficialCurrency: p.currency, Result: "matched", CheckedAt: time.Now().Unix()}
	if amountProblem != "" {
		i.LocalAmount = ""
	}
	if p.known && !knownReconciliationStatus(r.provider, p.status) {
		p.known = false
		p.problem = "unknown_official_status"
	}
	if !p.known {
		i.Result = "unverified"
		i.Problem = p.problem
		if i.Problem == "" {
			i.Problem = "official_query_failed"
		}
		return i
	}
	if i.LocalPaid && r.money <= 0 {
		i.Result = "difference"
		i.Problem = "invalid_local_amount"
		return i
	}
	if strings.HasPrefix(p.problem, "official_identity") {
		i.OfficialPaid = false
	}
	if p.problem != "" {
		i.Result = "difference"
		i.Problem = p.problem
		return i
	}
	if r.status == "refunded" || strings.Contains(strings.ToLower(p.status), "refund") || strings.Contains(strings.ToLower(p.status), "chargeback") {
		if r.provider == "paypal" && p.status == "REFUNDED" && p.refundVerified && ((r.status == "refunded" && p.refundRecoveryVerified) || p.refundUncreditedVerified) && r.currency == p.currency && p.amount == decimal.NewFromFloat(r.money).String() {
			i.LocalPaid = false
			i.OfficialPaid = false
			i.Verification = "refund_matched"
			return i
		}
		i.Result = "unverified"
		i.Problem = "refund_requires_separate_funds_and_entitlement_review"
		return i
	}
	if i.LocalPaid && !p.paid {
		i.Result = "difference"
		i.Problem = "local_success_official_not_paid"
		return i
	}
	if !i.LocalPaid && p.paid {
		i.Result = "difference"
		i.Problem = "official_paid_local_not_success"
		return i
	}
	if !i.LocalPaid && !p.paid {
		return i
	}
	if amountProblem != "" {
		i.Result = "unverified"
		i.Problem = amountProblem
		return i
	}
	if p.amount != "" {
		a, e := decimal.NewFromString(p.amount)
		b := decimal.NewFromFloat(r.money)
		if r.provider == model.PaymentProviderPayPal {
			// Match the exact two-decimal charge sent to PayPal for legacy
			// prorated orders, as the live capture validator does.
			b, _ = decimal.NewFromString(service.FormatPayPalAmount(r.money))
		}
		if e != nil || a.LessThanOrEqual(decimal.Zero) {
			i.Result = "unverified"
			i.Problem = "invalid_official_amount"
			return i
		}
		if p.currency != r.currency {
			i.Result = "difference"
			i.Problem = "currency_mismatch"
			return i
		}
		if !a.Equal(b) {
			i.Result = "difference"
			i.Problem = "amount_mismatch"
			return i
		}
	} else if p.paid {
		i.Result = "unverified"
		i.Problem = "missing_official_amount"
	}
	return i
}

// Subscription Money is USD. Reconcile the frozen payer amount, never a
// current exchange rate or the callback amount being audited.
func reconciliationEpaySubscriptionAmount(r reconciliationCandidate) (reconciliationCandidate, string) {
	var envelope struct {
		Snapshot *subscriptionEpayPaymentSnapshot `json:"payment_snapshot"`
	}
	if common.UnmarshalJsonStr(r.payload, &envelope) != nil {
		return r, "missing_frozen_bill_amount"
	}
	var snapshot subscriptionEpayPaymentSnapshot
	if envelope.Snapshot != nil {
		snapshot = *envelope.Snapshot
	} else if common.UnmarshalJsonStr(r.payload, &snapshot) != nil {
		return r, "missing_frozen_bill_amount"
	}
	expected, err := calculateSubscriptionEpayChargeAmount(snapshot.PayableUSD, snapshot.ExchangeRate)
	amount, amountErr := decimal.NewFromString(snapshot.ChargeAmount)
	if err != nil || amountErr != nil || snapshot.ChargeCurrency != "CNY" ||
		!decimal.NewFromFloat(snapshot.PayableUSD).Equal(decimal.NewFromFloat(r.money)) ||
		amount.Sign() <= 0 || amount.StringFixed(2) != expected || !amount.Equal(amount.Round(2)) {
		return r, "missing_frozen_bill_amount"
	}
	r.money = amount.InexactFloat64()
	r.currency = snapshot.ChargeCurrency
	return r, ""
}

func reconciliationWaffoSubscriptionOrderID(payload string) string {
	var event struct {
		Data struct {
			OrderID string `json:"orderId"`
		} `json:"data"`
	}
	if common.UnmarshalJsonStr(payload, &event) != nil {
		return ""
	}
	id := strings.TrimSpace(event.Data.OrderID)
	if !strings.HasPrefix(id, "ORD_") {
		return ""
	}
	return id
}

func queryReconciliationOrder(ctx context.Context, r reconciliationCandidate) reconciliationProof {
	fail := reconciliationProof{problem: "official_query_failed"}
	missing := reconciliationProof{problem: "missing_official_transaction_id"}
	switch r.provider {
	case "platega":
		o := model.GetPlategaOrderByTradeNo(r.trade)
		if o == nil {
			return missing
		}
		rID := o.PlategaTransactionId
		p, e := service.GetPlategaTransactionStatus(ctx, rID)
		if e != nil {
			return fail
		}
		// API amount is the fee-inclusive bill, while local frozen amount is base.
		expected, e := service.ExpectedPlategaBill(o)
		if e != nil {
			return reconciliationProof{problem: "missing_frozen_bill_amount"}
		}
		proof := reconciliationProof{id: rID, status: p.Status, currency: "RUB", amount: decimal.NewFromFloat(o.RubAmount).String(), paid: p.Status == "CONFIRMED", known: true}
		if service.ValidatePlategaAPIOrder(p, o) != nil || !service.PlategaAmountsMatch(expected, p.Amount) {
			proof.problem = "official_identity_or_bill_mismatch"
		}
		return proof
	case "epay":
		p, e := service.QueryEpayOrderState(ctx, r.trade)
		if e != nil {
			return fail
		}
		status := string(p.Status)
		if p.ProviderStatus != "" {
			status = p.ProviderStatus
		}
		return reconciliationProof{id: p.TradeNo, status: status, currency: "CNY", amount: string(p.Money), paid: string(p.Status) == "1", known: true, problem: reconciliationIdentityProblem(p.MerchantOrder, r.trade, p.Type, r.method)}
	case "stripe":
		stripe.Key = setting.StripeApiSecret
		id := r.queryID
		if id == "" {
			params := &stripe.CheckoutSessionListParams{ListParams: stripe.ListParams{Context: ctx}, CreatedRange: &stripe.RangeQueryParams{GreaterThanOrEqual: time.Now().AddDate(0, 0, -35).Unix()}}
			params.Limit = stripe.Int64(100)
			it := stripesession.List(params)
			for it.Next() {
				s := it.CheckoutSession()
				if s.ClientReferenceID == r.trade {
					id = s.ID
					break
				}
			}
			if it.Err() != nil {
				return fail
			}
			if id == "" {
				return missing
			}
		}
		p, e := queryStripePaidSession(ctx, id)
		if e != nil || p == nil {
			return fail
		}
		return reconciliationProof{id: p.ID, status: string(p.Status) + "/" + string(p.PaymentStatus), currency: strings.ToUpper(string(p.Currency)), amount: decimal.NewFromInt(p.AmountTotal).Div(decimal.NewFromInt(100)).String(), paid: p.Status == stripe.CheckoutSessionStatusComplete && p.PaymentStatus == stripe.CheckoutSessionPaymentStatusPaid, known: true, problem: reconciliationIdentityProblem(p.ClientReferenceID, r.trade, p.ID, id)}
	case "paypal":
		id := r.queryID
		if id == "" && r.top != nil {
			id = r.top.PayPalCaptureID
		}
		if id == "" {
			return missing
		}
		if strings.HasPrefix(id, "order:") {
			return queryPayPalReconciliationOrder(ctx, strings.TrimPrefix(id, "order:"), r)
		}
		raw, e := service.QueryPayPalCapture(ctx, id)
		if e != nil {
			return fail
		}
		var p struct {
			ID       string `json:"id"`
			Status   string `json:"status"`
			CustomID string `json:"custom_id"`
			Amount   struct {
				Value    string `json:"value"`
				Currency string `json:"currency_code"`
			} `json:"amount"`
		}
		if common.Unmarshal(raw, &p) != nil {
			return fail
		}
		proof := reconciliationProof{id: p.ID, status: p.Status, currency: p.Amount.Currency, amount: p.Amount.Value, paid: p.Status == "COMPLETED", known: true, problem: reconciliationIdentityProblem(p.CustomID, r.trade, p.ID, id)}
		return verifyPayPalReconciliationRefund(ctx, r, proof)
	case "creem":
		if r.queryID == "" {
			return missing
		}
		raw, e := service.QueryCreemCheckout(ctx, r.queryID)
		if e != nil {
			return fail
		}
		var p struct {
			ID        string `json:"id"`
			RequestID string `json:"request_id"`
			Status    string `json:"status"`
			Mode      string `json:"mode"`
			Order     struct {
				Status   string `json:"status"`
				Subtotal int64  `json:"sub_total"`
				Currency string `json:"currency"`
			} `json:"order"`
		}
		if common.Unmarshal(raw, &p) != nil {
			return fail
		}
		proof := reconciliationProof{id: p.ID, status: p.Status + "/" + p.Order.Status, currency: strings.ToUpper(p.Order.Currency), amount: decimal.NewFromInt(p.Order.Subtotal).Div(decimal.NewFromInt(100)).String(), paid: p.Status == "completed" && p.Order.Status == "paid", known: true, problem: reconciliationIdentityProblem(p.RequestID, r.trade, p.ID, r.queryID)}
		mode := "prod"
		if setting.CreemTestMode {
			mode = "test"
		}
		if p.Mode != mode {
			proof.problem = "official_identity_mismatch"
		}
		return proof
	case "clink":
		if r.queryID == "" {
			return missing
		}
		if strings.HasPrefix(r.queryID, "order_") {
			p, e := queryClinkReconciliationOrder(ctx, r.queryID)
			if e != nil || p == nil {
				return fail
			}
			problem := reconciliationIdentityProblem(p.MerchantReferenceID, r.trade, p.OrderID, r.queryID)
			if r.clinkSessionID != "" && p.SessionID != r.clinkSessionID {
				problem = "official_identity_mismatch"
			}
			return reconciliationProof{id: p.OrderID, status: p.Status, currency: p.OriginalCurrency, amount: decimal.NewFromFloat(p.AmountSubtotal).String(), paid: p.Status == "success", known: true, problem: problem}
		}
		p, e := service.GetClinkCheckoutSession(ctx, r.queryID)
		if e != nil {
			return fail
		}
		return reconciliationProof{id: p.SessionID, status: p.Status + "/" + p.PaymentStatus, currency: p.OriginalCurrency, amount: decimal.NewFromFloat(p.AmountSubtotal).String(), paid: p.PaymentStatus == "paid", known: true, problem: reconciliationIdentityProblem(p.MerchantReferenceID, r.trade, p.SessionID, r.queryID)}
	case "waffo":
		if r.queryID == "" {
			return missing
		}
		sdk, e := getWaffoSDK()
		if e != nil {
			return fail
		}
		p, e := sdk.Order().Inquiry(ctx, &waffoorder.InquiryOrderParams{PaymentRequestID: r.queryID}, nil)
		if e != nil || p == nil || !p.IsSuccess() || p.Data == nil {
			return fail
		}
		return reconciliationProof{id: p.Data.AcquiringOrderID, status: p.Data.OrderStatus, currency: p.Data.OrderCurrency, amount: p.Data.OrderAmount, paid: p.Data.OrderStatus == "PAY_SUCCESS", known: true, problem: reconciliationIdentityProblem(p.Data.MerchantOrderID, r.trade, p.Data.PaymentRequestID, r.queryID)}
	case "waffo_pancake":
		id := r.queryID
		if id == "" && r.top != nil {
			id = r.top.WaffoOrderID
		}
		if id == "" && r.purpose == "subscription" {
			id = reconciliationWaffoSubscriptionOrderID(r.payload)
		}
		if id == "" {
			return missing
		}
		p, e := queryWaffoReconciliationPayment(ctx, id, r.trade)
		if e != nil {
			return fail
		}
		return reconciliationProof{id: p.ID, status: p.Status, currency: p.Currency, amount: p.Amount, paid: p.Status == "succeeded", known: true}
	case "nowpayments":
		p := model.GetNowPaymentsPaymentByTradeNo(r.trade)
		if p == nil || p.PaymentID == "" {
			return missing
		}
		official, e := service.GetNowPaymentsPayment(ctx, p.PaymentID)
		if e != nil {
			return fail
		}
		proof := reconciliationProof{id: string(official.PaymentID), status: official.PaymentStatus, currency: strings.ToUpper(official.PriceCurrency), amount: decimal.NewFromFloat(float64(official.PriceAmount)).String(), paid: official.PaymentStatus == "finished", known: true, problem: reconciliationIdentityProblem(official.OrderID, r.trade, string(official.PaymentID), p.PaymentID)}
		if proof.paid && !nowPaymentsPaymentCoversAmount(float64(official.PayAmount), float64(official.ActuallyPaid)) {
			proof.problem = "official_underpayment"
		}
		return proof
	case "crypto":
		var intent model.CryptoDepositIntent
		if model.DB.Where("top_up_trade_no = ? OR subscription_order_trade_no = ?", r.trade, r.trade).First(&intent).Error != nil || intent.TxHash == nil {
			return missing
		}
		cfg, ok := cryptoChains[intent.Chain]
		if !ok {
			return fail
		}
		var usd float64
		var e error
		switch cfg.kind {
		case "tron":
			usd, e = verifyTronIntent(&intent, cfg, getRPCs(cfg))
		case "solana":
			usd, e = verifySolanaIntent(&intent, cfg, getRPCs(cfg))
		default:
			usd, e = verifyIntentOnChain(&intent, cfg, getRPCs(cfg))
		}
		if e != nil {
			return reconciliationProof{id: *intent.TxHash, problem: "chain_verification_failed"}
		}
		return reconciliationProof{id: *intent.TxHash, status: "confirmed_on_chain", currency: "USD", amount: decimal.NewFromFloat(usd).Round(6).String(), paid: true, known: true}
	default:
		return reconciliationProof{problem: "unknown_payment_provider"}
	}
}
func reconciliationIdentityProblem(actual, expected, second, expectedSecond string) string {
	if actual != expected || second != expectedSecond {
		return "official_identity_mismatch"
	}
	return ""
}

func knownReconciliationStatus(provider, status string) bool {
	switch provider {
	case "platega":
		return status == "CONFIRMED" || status == "CANCELED" || status == "PENDING" || status == "CHARGEBACKED" || status == "CHARGEBACK"
	case "epay":
		return status == "0" || status == "1" || status == "SUCCESS" || status == "NOTPAY" || status == "USERPAYING" || status == "CLOSED" || status == "REVOKED" || status == "PAYERROR" || status == "REFUND" || status == "NOT_FOUND" || status == "WAIT_BUYER_PAY" || status == "TRADE_SUCCESS" || status == "TRADE_FINISHED" || status == "TRADE_CLOSED"
	case "stripe":
		return status == "complete/paid" || status == "complete/unpaid" || status == "open/unpaid" || status == "expired/unpaid" || status == "complete/no_payment_required"
	case "paypal":
		return status == "CREATED" || status == "APPROVED" || status == "SAVED" || status == "VOIDED" || status == "COMPLETED" || status == "PENDING" || status == "DECLINED" || status == "FAILED" || status == "REFUNDED" || status == "PARTIALLY_REFUNDED"
	case "creem":
		return status == "completed/paid" || strings.HasPrefix(status, "pending/") || strings.HasPrefix(status, "expired/") || strings.HasPrefix(status, "canceled/")
	case "clink":
		return status == "success" || strings.HasSuffix(status, "/paid") || strings.HasSuffix(status, "/unpaid") || strings.HasSuffix(status, "/pending")
	case "waffo":
		return status == "PAY_SUCCESS" || status == "PAY_FAILED" || status == "PAY_PENDING" || status == "PAY_CLOSED"
	case "waffo_pancake":
		return status == "succeeded" || status == "pending" || status == "failed" || status == "processing" || status == "canceled"
	case "nowpayments":
		return status == "finished" || status == "waiting" || status == "confirming" || status == "confirmed" || status == "sending" || status == "failed" || status == "expired" || status == "partially_paid" || status == "refunded"
	case "crypto":
		return status == "confirmed_on_chain"
	}
	return false
}

func queryPayPalReconciliationOrder(ctx context.Context, id string, r reconciliationCandidate) reconciliationProof {
	raw, err := service.QueryPayPalOrder(ctx, id)
	if err != nil {
		return reconciliationProof{problem: "official_query_failed"}
	}
	var p struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Units  []struct {
			CustomID string `json:"custom_id"`
			Amount   struct {
				Value    string `json:"value"`
				Currency string `json:"currency_code"`
			} `json:"amount"`
			Payments struct {
				Captures []struct {
					ID     string `json:"id"`
					Status string `json:"status"`
					Amount struct {
						Value    string `json:"value"`
						Currency string `json:"currency_code"`
					} `json:"amount"`
				} `json:"captures"`
			} `json:"payments"`
		} `json:"purchase_units"`
	}
	if common.Unmarshal(raw, &p) != nil || len(p.Units) != 1 {
		return reconciliationProof{problem: "official_query_failed"}
	}
	u := p.Units[0]
	proof := reconciliationProof{id: p.ID, status: p.Status, currency: u.Amount.Currency, amount: u.Amount.Value, known: true, problem: reconciliationIdentityProblem(u.CustomID, r.trade, p.ID, id)}
	if p.Status == "COMPLETED" {
		if len(u.Payments.Captures) != 1 {
			return reconciliationProof{problem: "missing_official_transaction_id"}
		}
		capture := u.Payments.Captures[0]
		proof.id = capture.ID
		proof.status = capture.Status
		proof.amount = capture.Amount.Value
		proof.currency = capture.Amount.Currency
		proof.paid = capture.Status == "COMPLETED"
	}
	return verifyPayPalReconciliationRefund(ctx, r, proof)
}

func verifyPayPalReconciliationRefund(ctx context.Context, r reconciliationCandidate, p reconciliationProof) reconciliationProof {
	if p.status != "REFUNDED" || p.problem != "" {
		return p
	}
	raw, err := service.QueryPayPalCapture(ctx, p.id)
	if err != nil {
		return p
	}
	var capture struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Updated string `json:"update_time"`
		Custom  string `json:"custom_id"`
		Amount  struct {
			Value    string `json:"value"`
			Currency string `json:"currency_code"`
		} `json:"amount"`
	}
	if common.Unmarshal(raw, &capture) != nil || capture.ID != p.id || capture.Status != "REFUNDED" || capture.Custom != r.trade {
		return p
	}
	amount, e := decimal.NewFromString(capture.Amount.Value)
	if e != nil {
		return p
	}
	p.amount = amount.String()
	p.currency = capture.Amount.Currency
	_, err = service.VerifyPayPalFullRefund(ctx, p.id, capture.Updated, p.amount, p.currency)
	if err != nil {
		return p
	}
	p.refundVerified = true
	if r.purpose == "subscription" {
		p.refundRecoveryVerified, err = model.VerifyPayPalSubscriptionRefundRecovery(r.trade, r.user, r.money)
		if err != nil {
			p.refundRecoveryVerified = false
		}
		return p
	}
	if r.top == nil {
		r.top = model.GetTopUpByTradeNo(r.trade)
	}
	p.refundUncreditedVerified, err = model.VerifyPayPalUncreditedTopUp(r.top)
	if err != nil {
		p.refundUncreditedVerified = false
	}
	p.refundRecoveryVerified, err = model.VerifyPayPalRefundRecovery(r.top)
	if err != nil {
		p.refundRecoveryVerified = false
	}
	return p
}

func ConfirmPaymentReconciliationItem(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid item id"})
		return
	}
	err = model.ConfirmPaymentReconciliationItem(id, c.GetInt("id"))
	if err != nil {
		status, message := http.StatusInternalServerError, "Could not confirm reconciliation"
		if errors.Is(err, model.ErrReconciliationReviewConflict) {
			status, message = http.StatusConflict, err.Error()
		}
		if errors.Is(err, model.ErrReconciliationReviewInvalid) {
			status, message = http.StatusBadRequest, err.Error()
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			status, message = http.StatusNotFound, "reconciliation item not found"
		}
		c.JSON(status, gin.H{"success": false, "message": message})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
