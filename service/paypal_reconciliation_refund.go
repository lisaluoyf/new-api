package service

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// VerifyPayPalFullRefund reads reporting and refund resources independently.
// A capture's REFUNDED state alone cannot prove the refund amount or identity.
// Partial/multiple refunds remain outside the full-refund reconciliation scope.
func VerifyPayPalFullRefund(ctx context.Context, captureID, updated, amount, currency string) (string, error) {
	stamp, err := time.Parse(time.RFC3339, updated)
	if err != nil {
		return "", errors.New("refund date unavailable")
	}
	expected, err := decimal.NewFromString(amount)
	if err != nil || expected.Sign() <= 0 {
		return "", errors.New("invalid capture amount")
	}
	token, err := getPayPalAccessToken()
	if err != nil {
		return "", errors.New("refund authentication unavailable")
	}
	start := stamp.UTC().Truncate(24 * time.Hour).Add(-24 * time.Hour)
	end := start.Add(72 * time.Hour)
	if now := time.Now().UTC(); end.After(now) {
		end = now
	}
	var refundIDs []string
	seen := map[string]bool{}
	for page := 1; page <= 1000; page++ {
		q := url.Values{"start_date": {start.Format(time.RFC3339)}, "end_date": {end.Add(-time.Second).Format(time.RFC3339)}, "fields": {"transaction_info"}, "page_size": {"500"}, "page": {decimal.NewFromInt(int64(page)).String()}, "balance_affecting_records_only": {"Y"}}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, payPalBaseURL()+"/v1/reporting/transactions?"+q.Encode(), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		var response struct {
			Pages   *int `json:"total_pages"`
			Details []struct {
				Info struct {
					ID            string `json:"transaction_id"`
					Reference     string `json:"paypal_reference_id"`
					ReferenceType string `json:"paypal_reference_id_type"`
					Code          string `json:"transaction_event_code"`
					Status        string `json:"transaction_status"`
					Amount        struct {
						Value    string `json:"value"`
						Currency string `json:"currency_code"`
					} `json:"transaction_amount"`
				} `json:"transaction_info"`
			} `json:"transaction_details"`
		}
		if verifiedPaymentJSON(req, &response) != nil || response.Pages == nil {
			return "", errors.New("refund report incomplete")
		}
		for _, row := range response.Details {
			v := row.Info
			if v.Reference != captureID || v.ReferenceType != "TXN" || v.Code != "T1107" || v.Status != "S" {
				continue
			}
			if v.ID == "" || seen[v.ID] {
				return "", errors.New("invalid refund report identity")
			}
			a, e := decimal.NewFromString(v.Amount.Value)
			if e != nil || a.Sign() >= 0 || !a.Abs().Equal(expected) || v.Amount.Currency != currency {
				return "", errors.New("partial or inconsistent refund report")
			}
			seen[v.ID] = true
			refundIDs = append(refundIDs, v.ID)
		}
		if page >= *response.Pages {
			break
		}
		if page == 1000 {
			return "", errors.New("refund report truncated")
		}
	}
	if len(refundIDs) != 1 {
		return "", errors.New("single full refund not verified")
	}
	id := refundIDs[0]
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, payPalBaseURL()+"/v2/payments/refunds/"+url.PathEscape(id), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	var refund struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Amount struct {
			Value    string `json:"value"`
			Currency string `json:"currency_code"`
		} `json:"amount"`
		Links []struct {
			Rel  string `json:"rel"`
			Href string `json:"href"`
		} `json:"links"`
	}
	if verifiedPaymentJSON(req, &refund) != nil || refund.ID != id || refund.Status != "COMPLETED" || refund.Amount.Currency != currency {
		return "", errors.New("refund not completed or identity mismatch")
	}
	actual, err := decimal.NewFromString(refund.Amount.Value)
	if err != nil || !actual.Equal(expected) {
		return "", errors.New("refund amount mismatch")
	}
	for _, link := range refund.Links {
		u, e := url.Parse(link.Href)
		if e == nil && link.Rel == "up" && u.Scheme == "https" && (u.Host == "api.paypal.com" || u.Host == "api-m.paypal.com" || u.Host == "api.sandbox.paypal.com" || u.Host == "api-m.sandbox.paypal.com") && strings.TrimSuffix(u.Path, "/") == "/v2/payments/captures/"+captureID {
			return id, nil
		}
	}
	return "", errors.New("refund capture association unavailable")
}
