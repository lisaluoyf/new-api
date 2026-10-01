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
)

var reconciliationTimezone = time.FixedZone("UTC+8", 8*3600)
var reconciliationOnce sync.Once

type reconciliationCandidate struct {
	trade, provider, method, purpose, status, queryID, currency, payload string
	user                                                                 int
	money                                                                float64
	top                                                                  *model.TopUp
}
type reconciliationProof struct {
	id, status, currency, amount, problem string
	paid, known                           bool
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
		return append(append([]string{}, model.PaymentReconciliationProviders...), "unknown"), nil
	}
	for _, p := range append(append([]string{}, model.PaymentReconciliationProviders...), "unknown") {
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
		if model.DB.Model(&model.PaymentReconciliationItem{}).Where("run_id IN ? AND result <> ?", ids, "matched").Count(&itemsTotal).Error != nil {
			common.ApiErrorMsg(c, "Could not count differences")
			return
		}
		// Paginated issues; summaries always cover every checked order.
		if model.DB.Where("run_id IN ? AND result <> ?", ids, "matched").Order("id ASC").Offset((page-1)*100).Limit(100).Find(&items).Error != nil {
			common.ApiErrorMsg(c, "Could not load differences")
			return
		}
	}
	truncated := int64(page*100) < itemsTotal
	for i := range runs {
		runs[i].Totals = []model.PaymentReconciliationTotal{}
		_ = common.UnmarshalJsonStr(runs[i].TotalsJSON, &runs[i].Totals)
		sort.Slice(runs[i].Totals, func(a, b int) bool { return runs[i].Totals[a].Currency < runs[i].Totals[b].Currency })
	}
	c.JSON(200, gin.H{"success": true, "jobs": jobs, "runs": runs, "items": items, "items_truncated": truncated, "items_total": itemsTotal, "page": page, "timezone": "UTC+8", "schedule": "08:30", "scope": "orders_created_or_completed_on_selected_day", "coverage": "local_orders_queried_against_provider"})
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
		for _, p := range append(append([]string{}, model.PaymentReconciliationProviders...), "unknown") {
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
	return items, nil
}
func normalizeReconciliationProvider(provider, method string) string {
	if provider != "" {
		return provider
	}
	if method == "alipay" || method == "wxpay" {
		return "epay"
	}
	for _, p := range model.PaymentReconciliationProviders {
		if p == method {
			return p
		}
	}
	return "unknown"
}
func loadReconciliationCandidates(start, end int64, provider string) ([]reconciliationCandidate, error) {
	var tops []model.TopUp
	var subs []model.SubscriptionOrder
	scope := "(create_time >= ? AND create_time < ?) OR (complete_time >= ? AND complete_time < ?)"
	if err := model.DB.Where(scope, start, end, start, end).Find(&tops).Error; err != nil {
		return nil, err
	}
	if err := model.DB.Where(scope, start, end, start, end).Find(&subs).Error; err != nil {
		return nil, err
	}
	rows := map[string]reconciliationCandidate{}
	for i := range tops {
		t := tops[i]
		if t.Status == "success" && t.CompleteTime > 0 && (t.CompleteTime < start || t.CompleteTime >= end) {
			continue
		}
		p := normalizeReconciliationProvider(t.PaymentProvider, t.PaymentMethod)
		if p == "free" {
			continue
		}
		rows[t.TradeNo] = reconciliationCandidate{trade: t.TradeNo, provider: p, method: t.PaymentMethod, purpose: "wallet", status: t.Status, user: t.UserId, money: t.Money, top: &t}
	}
	for _, s := range subs {
		if s.Status == "success" && s.CompleteTime > 0 && (s.CompleteTime < start || s.CompleteTime >= end) {
			continue
		}
		p := normalizeReconciliationProvider(s.PaymentProvider, s.PaymentMethod)
		if p == "free" {
			continue
		}
		r := reconciliationCandidate{trade: s.TradeNo, provider: p, method: s.PaymentMethod, purpose: "subscription", status: s.Status, user: s.UserId, money: s.Money, payload: s.ProviderPayload}
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
	i := model.PaymentReconciliationItem{TradeNo: r.trade, UserID: r.user, Purpose: r.purpose, LocalStatus: r.status, LocalPaid: r.status == "success", LocalAmount: decimal.NewFromFloat(r.money).String(), Currency: r.currency, OfficialID: p.id, OfficialStatus: p.status, OfficialPaid: p.paid, OfficialAmount: p.amount, OfficialCurrency: p.currency, Result: "matched", CheckedAt: time.Now().Unix()}
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
	if p.amount != "" {
		a, e := decimal.NewFromString(p.amount)
		b := decimal.NewFromFloat(r.money)
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
			return queryPayPalReconciliationOrder(ctx, strings.TrimPrefix(id, "order:"), r.trade)
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
		return reconciliationProof{id: p.ID, status: p.Status, currency: p.Amount.Currency, amount: p.Amount.Value, paid: p.Status == "COMPLETED", known: true, problem: reconciliationIdentityProblem(p.CustomID, r.trade, p.ID, id)}
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
		if id == "" {
			return missing
		}
		p, e := service.QueryWaffoReconciliationPayment(ctx, id, r.trade)
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
		return strings.HasSuffix(status, "/paid") || strings.HasSuffix(status, "/unpaid") || strings.HasSuffix(status, "/pending")
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

func queryPayPalReconciliationOrder(ctx context.Context, id, trade string) reconciliationProof {
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
	proof := reconciliationProof{id: p.ID, status: p.Status, currency: u.Amount.Currency, amount: u.Amount.Value, known: true, problem: reconciliationIdentityProblem(u.CustomID, trade, p.ID, id)}
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
	return proof
}
