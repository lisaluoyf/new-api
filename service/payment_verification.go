package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// Dedicated clients cannot follow redirects with merchant credentials. Errors
// deliberately exclude URLs and response bodies (legacy Epay puts a key in URL).
var paymentVerificationClient = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("payment API redirect rejected") }}

func verifiedPaymentJSON(req *http.Request, out any) error {
	resp, err := paymentVerificationClient.Do(req)
	if err != nil {
		return errors.New("payment API request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return errors.New("payment API returned unsuccessful status")
	}
	if err := common.DecodeJson(http.MaxBytesReader(nil, resp.Body, 1<<20), out); err != nil {
		return errors.New("invalid payment API response")
	}
	return nil
}

func QueryCreemCheckout(ctx context.Context, id string) (json.RawMessage, error) {
	if id == "" || setting.CreemApiKey == "" {
		return nil, errors.New("missing checkout identity or API credential")
	}
	base := "https://api.creem.io"
	if setting.CreemTestMode {
		base = "https://test-api.creem.io"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/checkouts?checkout_id="+url.QueryEscape(id), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", setting.CreemApiKey)
	var result json.RawMessage
	err = verifiedPaymentJSON(req, &result)
	return result, err
}

func QueryPayPalCapture(ctx context.Context, id string) (json.RawMessage, error) {
	if id == "" {
		return nil, errors.New("missing capture identity")
	}
	token, err := getPayPalAccessToken()
	if err != nil {
		return nil, errors.New("PayPal API authentication failed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, payPalBaseURL()+"/v2/payments/captures/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	var result json.RawMessage
	err = verifiedPaymentJSON(req, &result)
	return result, err
}

type EpayVerifiedOrder struct {
	Currency       string          `json:"currency"`
	ProviderStatus string          `json:"provider_status"`
	VerifiedVia    string          `json:"verified_via"`
	Code           int             `json:"code"`
	PID            dto.StringValue `json:"pid"`
	TradeNo        string          `json:"trade_no"`
	MerchantOrder  string          `json:"out_trade_no"`
	Money          dto.StringValue `json:"money"`
	Status         dto.StringValue `json:"status"`
	Type           string          `json:"type"`
}

func QueryEpayOrder(ctx context.Context, trade string) (*EpayVerifiedOrder, error) {
	r, err := QueryEpayOrderState(ctx, trade)
	if err != nil {
		return nil, err
	}
	if string(r.Status) != "1" {
		return nil, errors.New("Epay official order is not verified paid")
	}
	return r, nil
}

func QueryEpayOrderState(ctx context.Context, trade string) (*EpayVerifiedOrder, error) {
	if trade == "" || operation_setting.EpayId == "" || operation_setting.EpayKey == "" {
		return nil, errors.New("missing Epay configuration or order")
	}
	base := strings.TrimSpace(os.Getenv("EPAY_QUERY_BASE_URL"))
	custom := base != ""
	if !custom {
		base = operation_setting.PayAddress
	}
	path := "/api.php"
	if custom {
		path = "/api/order/query"
	}
	u, err := url.Parse(strings.TrimRight(base, "/") + path)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("Epay query requires HTTPS")
	}
	q := url.Values{"act": {"order"}, "pid": {operation_setting.EpayId}, "key": {operation_setting.EpayKey}, "out_trade_no": {trade}}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("invalid Epay query configuration")
	}
	if custom {
		payload, _ := common.Marshal(map[string]string{"pid": operation_setting.EpayId, "out_trade_no": trade})
		u.RawQuery = ""
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
		if err != nil {
			return nil, errors.New("invalid Epay query request")
		}
		req.Header.Set("Authorization", "Bearer "+operation_setting.EpayKey)
		req.Header.Set("Content-Type", "application/json")
	}
	var result EpayVerifiedOrder
	if err := verifiedPaymentJSON(req, &result); err != nil {
		return nil, err
	}
	if result.Code != 1 || string(result.PID) != operation_setting.EpayId || result.MerchantOrder != trade || result.TradeNo == "" {
		return nil, errors.New("Epay official order is not verified paid")
	}
	if custom && (result.VerifiedVia != "official_provider_api" || result.Currency != "CNY") {
		return nil, errors.New("Epay verified query provenance or currency mismatch")
	}
	return &result, nil
}

func GetClinkOrder(ctx context.Context, id string) (*ClinkOrderWebhookData, error) {
	if id == "" {
		return nil, errors.New("missing Clink order ID")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ClinkBaseURL()+"/order/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	if err := applyClinkAuthHeaders(req); err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := verifiedPaymentJSON(req, &raw); err != nil {
		return nil, err
	}
	var order ClinkOrderWebhookData
	if err := decodeClinkAPIEnvelope(raw, &order); err != nil {
		return nil, err
	}
	if order.OrderID != id || order.Status != "success" || order.MerchantReferenceID == "" {
		return nil, errors.New("Clink official order identity or status mismatch")
	}
	return &order, nil
}

func QueryPayPalOrder(ctx context.Context, id string) (json.RawMessage, error) {
	if id == "" {
		return nil, errors.New("missing PayPal order")
	}
	token, err := getPayPalAccessToken()
	if err != nil {
		return nil, errors.New("PayPal authentication unavailable")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, payPalBaseURL()+"/v2/checkout/orders/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	var result json.RawMessage
	err = verifiedPaymentJSON(req, &result)
	return result, err
}
