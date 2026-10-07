package service

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stripe/stripe-go/v81"
	stripeevent "github.com/stripe/stripe-go/v81/event"
)

// StatementPayment is an independently discovered provider payment. No customer
// profile or raw statement is persisted. Missing merchant references stay visible.
type StatementPayment struct {
	ID, TradeNo, Status, Amount, Currency string
	PaidAt                                int64
	RefundOnly                            bool
	CreatedAt                             int64
}

func ListReconciliationPayments(ctx context.Context, provider string, start, end time.Time) ([]StatementPayment, error) {
	switch provider {
	case "epay":
		return listEpayStatement(ctx, start)
	case "platega":
		return listPlategaStatement(ctx, start, end)
	case "stripe":
		return listStripeStatement(ctx, start, end)
	case "paypal":
		return listPayPalStatement(ctx, start, end)
	case "waffo_pancake":
		return listWaffoStatement(ctx, start, end)
	case "clink":
		return listClinkStatement(ctx, start, end)
	case "creem":
		return listCreemStatement(ctx, start, end)
	case "nowpayments":
		return listNowPaymentsStatement(ctx, start, end)
	default:
		return nil, errors.New("provider statement enumeration unavailable")
	}
}

func listNowPaymentsStatement(ctx context.Context, start, end time.Time) ([]StatementPayment, error) {
	// NOWPayments list access requires reporting authentication in addition to
	// the existing payment API key. Never infer full coverage from that key alone.
	email, password := os.Getenv("NOWPAYMENTS_REPORTING_EMAIL"), os.Getenv("NOWPAYMENTS_REPORTING_PASSWORD")
	if email == "" || password == "" || setting.NowPaymentsAPIKey == "" {
		return nil, errors.New("reporting credentials unavailable")
	}
	body, _ := common.Marshal(map[string]string{"email": email, "password": password})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, nowPaymentsAPIBaseURL+"/auth", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	var auth struct {
		Token string `json:"token"`
	}
	if verifiedPaymentJSON(req, &auth) != nil || auth.Token == "" {
		return nil, errors.New("reporting authentication failed")
	}
	out := []StatementPayment{}
	seen := map[string]bool{}
	for page := 0; page < 1000; page++ {
		q := url.Values{"limit": {"500"}, "page": {strconv.Itoa(page)}, "sortBy": {"created_at"}, "orderBy": {"asc"}, "dateFrom": {start.UTC().Format(time.RFC3339)}, "dateTo": {end.UTC().Add(-time.Millisecond).Format(time.RFC3339Nano)}}
		req, _ = http.NewRequestWithContext(ctx, http.MethodGet, nowPaymentsAPIBaseURL+"/payment/?"+q.Encode(), nil)
		req.Header.Set("x-api-key", setting.NowPaymentsAPIKey)
		req.Header.Set("Authorization", "Bearer "+auth.Token)
		var response struct {
			Data  []NowPaymentsPaymentResponse `json:"data"`
			Pages *int                         `json:"pagesCount"`
		}
		if verifiedPaymentJSON(req, &response) != nil || response.Pages == nil {
			return out, errors.New("NOWPayments statement unavailable")
		}
		for _, p := range response.Data {
			if p.PaymentStatus != "finished" {
				continue
			}
			id := string(p.PaymentID)
			if id == "" || seen[id] || float64(p.PriceAmount) <= 0 || p.PriceCurrency == "" {
				return out, errors.New("invalid payment statement")
			}
			seen[id] = true
			out = append(out, StatementPayment{ID: id, TradeNo: p.OrderID, Status: p.PaymentStatus, Amount: decimal.NewFromFloat(float64(p.PriceAmount)).String(), Currency: strings.ToUpper(p.PriceCurrency)})
		}
		if page+1 >= *response.Pages {
			return out, nil
		}
	}
	return out, errors.New("statement pagination exceeded safe bound")
}

func listCreemStatement(ctx context.Context, start, end time.Time) ([]StatementPayment, error) {
	if setting.CreemApiKey == "" {
		return nil, errors.New("statement credentials unavailable")
	}
	base := "https://api.creem.io"
	mode := "prod"
	if setting.CreemTestMode {
		base = "https://test-api.creem.io"
		mode = "test"
	}
	out := []StatementPayment{}
	seen := map[string]bool{}
	for page := 1; page <= 1000; page++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/transactions/search?page_number="+strconv.Itoa(page)+"&page_size=100", nil)
		req.Header.Set("x-api-key", setting.CreemApiKey)
		var result struct {
			Items []struct {
				ID        string `json:"id"`
				Mode      string `json:"mode"`
				Status    string `json:"status"`
				Amount    int64  `json:"amount"`
				Currency  string `json:"currency"`
				CreatedAt int64  `json:"created_at"`
			} `json:"items"`
			Pagination struct {
				TotalPages *int `json:"total_pages"`
			} `json:"pagination"`
		}
		if verifiedPaymentJSON(req, &result) != nil || result.Pagination.TotalPages == nil {
			return out, errors.New("Creem statement unavailable")
		}
		for _, r := range result.Items {
			if seen[r.ID] {
				return out, errors.New("statement pagination conflict")
			}
			seen[r.ID] = true
			if r.Status != "paid" || r.CreatedAt < start.UnixMilli() || r.CreatedAt >= end.UnixMilli() {
				continue
			}
			if r.ID == "" || r.Mode != mode || r.Currency == "" || r.Amount <= 0 {
				return out, errors.New("invalid Creem statement")
			}
			out = append(out, StatementPayment{ID: r.ID, Status: "completed/paid", Amount: decimal.NewFromInt(r.Amount).Div(decimal.NewFromInt(100)).String(), Currency: strings.ToUpper(r.Currency), PaidAt: r.CreatedAt / 1000})
		}
		if page >= *result.Pagination.TotalPages {
			return out, nil
		}
	}
	return out, errors.New("statement pagination exceeded safe bound")
}

func listClinkStatement(ctx context.Context, start, end time.Time) ([]StatementPayment, error) {
	out := []StatementPayment{}
	seen := map[string]bool{}
	read := 0
	for page := 1; page <= 1000; page++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ClinkBaseURL()+"/order?pageNum="+strconv.Itoa(page)+"&pageSize=50", nil)
		if applyClinkAuthHeaders(req) != nil {
			return out, errors.New("statement authentication unavailable")
		}
		var response struct {
			Code  int  `json:"code"`
			Total *int `json:"total"`
			Rows  []struct {
				ID          string          `json:"orderId"`
				Trade       string          `json:"merchantReferenceId"`
				Status      string          `json:"status"`
				Amount      decimal.Decimal `json:"amountSubtotal"`
				Currency    string          `json:"originalCurrency"`
				PaymentTime int64           `json:"paymentTime"`
			} `json:"rows"`
		}
		if verifiedPaymentJSON(req, &response) != nil || response.Code != 200 || response.Total == nil {
			return out, errors.New("Clink statement unavailable")
		}
		for _, r := range response.Rows {
			if seen[r.ID] {
				return out, errors.New("statement pagination conflict")
			}
			seen[r.ID] = true
			if r.Status != "success" || r.PaymentTime < start.UnixMilli() || r.PaymentTime >= end.UnixMilli() {
				continue
			}
			if r.ID == "" || r.Currency == "" || r.Amount.Sign() <= 0 {
				return out, errors.New("invalid Clink statement")
			}
			out = append(out, StatementPayment{ID: r.ID, TradeNo: r.Trade, Status: "complete/paid", Amount: r.Amount.String(), Currency: strings.ToUpper(r.Currency), PaidAt: r.PaymentTime / 1000})
		}
		read += len(response.Rows)
		if read >= *response.Total {
			return out, nil
		}
		if len(response.Rows) == 0 {
			return out, errors.New("statement truncated")
		}
	}
	return out, errors.New("statement pagination exceeded safe bound")
}

func listWaffoStatement(ctx context.Context, start, end time.Time) ([]StatementPayment, error) {
	if setting.WaffoPancakeStoreID == "" || setting.WaffoPancakePrivateKey == "" {
		return nil, errors.New("statement credentials unavailable")
	}
	out := []StatementPayment{}
	seen := map[string]bool{}
	for offset := 0; offset < 100000; offset += 100 {
		var result struct {
			Payments []waffoRefundPayment `json:"payments"`
		}
		err := waffoRefundQuery(ctx, `query($store:String!,$start:String!,$end:String!,$offset:Int!){payments(storeId:$store,filter:{createdAt:{gte:$start,lt:$end},status:{eq:"succeeded"}},orderBy:[created_at_asc],limit:100,offset:$offset){id orderId status testMode orderMerchantExternalId snapshotAmountDetails{currency subtotal total} onetimeOrder{id storeId metadata}}}`, map[string]any{"store": setting.WaffoPancakeStoreID, "start": start.UTC().Format(time.RFC3339), "end": end.UTC().Format(time.RFC3339), "offset": offset}, &result)
		if err != nil {
			return out, errors.New("Waffo statement unavailable")
		}
		for _, p := range result.Payments {
			if p.TestMode != setting.WaffoPancakeSandbox || p.OnetimeOrder == nil || p.OnetimeOrder.StoreID != setting.WaffoPancakeStoreID || p.Status != "succeeded" || p.ID == "" {
				return out, errors.New("statement identity mismatch")
			}
			if seen[p.ID] {
				return out, errors.New("duplicate statement payment")
			}
			seen[p.ID] = true
			trade, refErr := resolveWaffoMerchantReference(ctx, p)
			if refErr != nil {
				trade = ""
			}

			out = append(out, StatementPayment{ID: p.ID, TradeNo: trade, Status: p.Status, Amount: p.Snapshot.Subtotal, Currency: strings.ToUpper(p.Snapshot.Currency)})
		}
		if len(result.Payments) < 100 {
			return out, nil
		}
	}
	return out, errors.New("statement pagination exceeded safe bound")
}

func listEpayStatement(ctx context.Context, start time.Time) ([]StatementPayment, error) {
	base := strings.TrimRight(os.Getenv("EPAY_QUERY_BASE_URL"), "/")
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("official statement interface unavailable")
	}
	body, _ := common.Marshal(map[string]string{"pid": operation_setting.EpayId, "day": start.Format("2006-01-02")})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/order/statement", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+operation_setting.EpayKey)
	req.Header.Set("Content-Type", "application/json")
	var response struct {
		PID      string `json:"pid"`
		Day      string `json:"day"`
		Complete bool   `json:"complete"`
		Via      string `json:"verified_via"`
		Payments []struct {
			ID       string `json:"id"`
			Trade    string `json:"trade_no"`
			Status   string `json:"status"`
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		} `json:"payments"`
	}
	if err = verifiedEpayJSON(req, &response); err != nil {
		return nil, err
	}
	if response.PID != operation_setting.EpayId || response.Day != start.Format("2006-01-02") || response.Via != "official_provider_statement" {
		return nil, errors.New("statement identity mismatch")
	}
	out := []StatementPayment{}
	for _, p := range response.Payments {
		out = append(out, StatementPayment{ID: p.ID, TradeNo: p.Trade, Status: p.Status, Amount: p.Amount, Currency: p.Currency})
	}
	if !response.Complete {
		return out, errors.New("partial statement coverage")
	}
	return out, nil
}

func listPlategaStatement(ctx context.Context, start, end time.Time) ([]StatementPayment, error) {
	if !PlategaConfigured() {
		return nil, errors.New("statement credentials unavailable")
	}
	body, _ := common.Marshal(map[string]any{"from": start.UTC().Format(time.RFC3339), "to": end.UTC().Add(-time.Millisecond).Format(time.RFC3339Nano), "timeZoneId": "UTC", "statuses": []string{}, "paymentMethods": []string{}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, plategaBaseURL+"/transaction/export/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-MerchantId", PlategaMerchantID())
	req.Header.Set("X-Secret", PlategaSecret())
	req.Header.Set("Content-Type", "application/json")
	var raw []struct {
		CreatedAt string          `json:"createdAt"`
		ID        string          `json:"recordId"`
		Trade     string          `json:"payload"`
		Status    string          `json:"status"`
		Amount    decimal.Decimal `json:"amount"`
		Currency  string          `json:"currencyCode"`
	}
	// The documented JSON endpoint returns transaction rows, unlike CSV export.
	if err = verifiedPaymentJSON(req, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, errors.New("missing statement rows")
	}
	out := []StatementPayment{}
	for _, r := range raw {
		if r.Status != "CONFIRMED" {
			continue
		}
		if r.ID == "" || r.Amount.Sign() <= 0 || r.Currency == "" {
			return nil, errors.New("invalid provider statement")
		}
		created, err := time.ParseInLocation("2006-01-02 15:04:05", r.CreatedAt, time.UTC)
		if err != nil || created.Before(start) || !created.Before(end) {
			return nil, errors.New("invalid provider statement creation date")
		}
		out = append(out, StatementPayment{ID: r.ID, TradeNo: r.Trade, Status: r.Status, Amount: r.Amount.String(), Currency: strings.ToUpper(r.Currency), CreatedAt: created.Unix()})
	}
	return out, nil
}

func listStripeStatement(ctx context.Context, start, end time.Time) ([]StatementPayment, error) {
	if start.Before(time.Now().AddDate(0, 0, -30)) {
		return nil, errors.New("Stripe event history unavailable for this date")
	}
	if setting.StripeApiSecret == "" {
		return nil, errors.New("statement credentials unavailable")
	}
	p := &stripe.EventListParams{CreatedRange: &stripe.RangeQueryParams{GreaterThanOrEqual: start.Unix(), LesserThan: end.Unix()}, Types: stripe.StringSlice([]string{"checkout.session.completed", "checkout.session.async_payment_succeeded"})}
	p.Context = ctx
	p.Limit = stripe.Int64(100)
	// A per-request key prevents mixing credentials with another SDK user.
	p.SetStripeAccount("")
	backend := stripe.GetBackend(stripe.APIBackend)
	client := stripeevent.Client{B: backend, Key: setting.StripeApiSecret}
	it := client.List(p)
	out := []StatementPayment{}
	seen := map[string]bool{}
	for it.Next() {
		ev := it.Event()
		var s stripe.CheckoutSession
		if common.Unmarshal(ev.Data.Raw, &s) != nil {
			return nil, errors.New("invalid Stripe statement event")
		}
		if s.PaymentStatus != stripe.CheckoutSessionPaymentStatusPaid {
			continue
		}
		if seen[s.ID] {
			continue
		}
		seen[s.ID] = true
		out = append(out, StatementPayment{ID: s.ID, TradeNo: s.ClientReferenceID, Status: "complete/paid", Amount: decimal.NewFromInt(s.AmountSubtotal).Div(decimal.NewFromInt(100)).String(), Currency: strings.ToUpper(string(s.Currency)), PaidAt: ev.Created})
	}
	if it.Err() != nil {
		return nil, errors.New("Stripe statement query failed")
	}
	return out, nil
}

func listPayPalStatement(ctx context.Context, start, end time.Time) ([]StatementPayment, error) {
	token, err := getPayPalAccessToken()
	if err != nil {
		return nil, errors.New("statement authentication failed")
	}
	out := []StatementPayment{}
	seen := map[string]bool{}
	refunds := map[string]StatementPayment{}
	for page := 1; page <= 1000; page++ {
		q := url.Values{"start_date": {start.UTC().Format(time.RFC3339)}, "end_date": {end.UTC().Add(-time.Second).Format(time.RFC3339)}, "fields": {"transaction_info"}, "page_size": {"500"}, "page": {strconv.Itoa(page)}, "balance_affecting_records_only": {"Y"}}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, payPalBaseURL()+"/v1/reporting/transactions?"+q.Encode(), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		var response struct {
			TotalPages *int `json:"total_pages"`
			Details    []struct {
				Info struct {
					ID            string `json:"transaction_id"`
					Status        string `json:"transaction_status"`
					Code          string `json:"transaction_event_code"`
					Reference     string `json:"invoice_id"`
					Custom        string `json:"custom_field"`
					ReferenceID   string `json:"paypal_reference_id"`
					ReferenceType string `json:"paypal_reference_id_type"`
					Amount        struct {
						Value    string `json:"value"`
						Currency string `json:"currency_code"`
					} `json:"transaction_amount"`
				} `json:"transaction_info"`
			} `json:"transaction_details"`
		}
		if err = verifiedPaymentJSON(req, &response); err != nil {
			return nil, err
		}
		for _, r := range response.Details {
			v := r.Info
			a, e := decimal.NewFromString(v.Amount.Value)
			if e != nil {
				return nil, errors.New("invalid PayPal statement amount")
			}
			if v.Status == "S" && isPayPalRefundStatementCode(v.Code) && a.Sign() < 0 && v.ReferenceType == "TXN" && v.ReferenceID != "" {
				trade := v.Custom
				if trade == "" {
					trade = v.Reference
				}
				refunds[v.ReferenceID] = StatementPayment{ID: v.ReferenceID, TradeNo: trade, Status: "REFUNDED", Amount: a.Abs().String(), Currency: strings.ToUpper(v.Amount.Currency), RefundOnly: true}
				continue
			}
			if v.Status != "S" || !strings.HasPrefix(v.Code, "T00") || a.Sign() <= 0 {
				continue
			}
			if v.ID == "" || v.Amount.Currency == "" {
				return nil, errors.New("invalid PayPal statement identity")
			}
			if seen[v.ID] {
				return nil, errors.New("duplicate statement transaction")
			}
			seen[v.ID] = true
			trade := v.Custom
			if trade == "" {
				trade = v.Reference
			}
			out = append(out, StatementPayment{ID: v.ID, TradeNo: trade, Status: "COMPLETED", Amount: a.String(), Currency: strings.ToUpper(v.Amount.Currency)})
		}
		if response.TotalPages == nil {
			return out, errors.New("invalid statement pagination")
		}
		if *response.TotalPages <= page {
			for id, p := range refunds {
				if !seen[id] {
					out = append(out, p)
				}
			}
			return out, nil
		}
	}
	return nil, errors.New("statement pagination exceeded safe bound")
}
