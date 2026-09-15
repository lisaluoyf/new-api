package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting"
)

var nowPaymentsAPIBaseURL = "https://api.nowpayments.io/v1"

func nowPaymentsAPIKeyMetadata() (int, string) {
	apiKey := strings.TrimSpace(setting.NowPaymentsAPIKey)
	if len(apiKey) <= 4 {
		return len(apiKey), apiKey
	}
	return len(apiKey), apiKey[len(apiKey)-4:]
}

func truncateNowPaymentsResponse(body []byte) string {
	const maxLength = 1000
	value := strings.TrimSpace(string(body))
	if len(value) <= maxLength {
		return value
	}
	return value[:maxLength] + "..."
}

type NowPaymentsCreateInvoiceRequest struct {
	PriceAmount      float64 `json:"price_amount"`
	PriceCurrency    string  `json:"price_currency"`
	IPNCallbackURL   string  `json:"ipn_callback_url"`
	OrderID          string  `json:"order_id"`
	OrderDescription string  `json:"order_description"`
	SuccessURL       string  `json:"success_url"`
	CancelURL        string  `json:"cancel_url"`
	PartiallyPaidURL string  `json:"partially_paid_url"`
	IsFixedRate      bool    `json:"is_fixed_rate"`
	IsFeePaidByUser  bool    `json:"is_fee_paid_by_user"`
}

type NowPaymentsInvoiceResponse struct {
	ID            dto.StringValue `json:"id"`
	OrderID       string          `json:"order_id"`
	PriceAmount   float64         `json:"price_amount"`
	PriceCurrency string          `json:"price_currency"`
	InvoiceURL    string          `json:"invoice_url"`
	SuccessURL    string          `json:"success_url"`
	CancelURL     string          `json:"cancel_url"`
	CreatedAt     string          `json:"created_at"`
	UpdatedAt     string          `json:"updated_at"`
}

type NowPaymentsPaymentResponse struct {
	PaymentID              dto.StringValue `json:"payment_id"`
	ParentPaymentID        dto.StringValue `json:"parent_payment_id"`
	InvoiceID              dto.StringValue `json:"invoice_id"`
	PaymentStatus          string          `json:"payment_status"`
	PayAddress             string          `json:"pay_address"`
	PayinExtraID           string          `json:"payin_extra_id"`
	PriceAmount            float64         `json:"price_amount"`
	PriceCurrency          string          `json:"price_currency"`
	PayAmount              float64         `json:"pay_amount"`
	PayCurrency            string          `json:"pay_currency"`
	ActuallyPaid           float64         `json:"actually_paid"`
	PayinHash              string          `json:"payin_hash"`
	OrderID                string          `json:"order_id"`
	Network                string          `json:"network"`
	ExpirationEstimateDate string          `json:"expiration_estimate_date"`
}

func CreateNowPaymentsInvoice(ctx context.Context, params *NowPaymentsCreateInvoiceRequest) (*NowPaymentsInvoiceResponse, error) {
	if params == nil || params.PriceAmount <= 0 || strings.TrimSpace(params.OrderID) == "" {
		return nil, fmt.Errorf("invalid NOWPayments invoice parameters")
	}
	body, err := common.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal NOWPayments invoice: %w", err)
	}
	var result NowPaymentsInvoiceResponse
	if err := requestNowPayments(ctx, http.MethodPost, "/invoice", bytes.NewReader(body), &result); err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(result.ID)) == "" || strings.TrimSpace(result.InvoiceURL) == "" {
		return nil, fmt.Errorf("NOWPayments returned an incomplete invoice")
	}
	return &result, nil
}

func GetNowPaymentsPayment(ctx context.Context, paymentID string) (*NowPaymentsPaymentResponse, error) {
	if strings.TrimSpace(paymentID) == "" {
		return nil, fmt.Errorf("missing NOWPayments payment id")
	}
	var result NowPaymentsPaymentResponse
	if err := requestNowPayments(ctx, http.MethodGet, "/payment/"+paymentID, nil, &result); err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(result.PaymentID)) == "" {
		return nil, fmt.Errorf("NOWPayments returned empty payment id")
	}
	return &result, nil
}

func requestNowPayments(ctx context.Context, method, path string, body io.Reader, result any) error {
	apiKey := strings.TrimSpace(setting.NowPaymentsAPIKey)
	if apiKey == "" {
		return fmt.Errorf("NOWPayments API key is not configured")
	}
	keyLength, keySuffix := nowPaymentsAPIKeyMetadata()
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(nowPaymentsAPIBaseURL, "/")+path, body)
	if err != nil {
		return fmt.Errorf("build NOWPayments request: %w", err)
	}
	req.Header.Set("x-api-key", apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request NOWPayments method=%s path=%s key_len=%d key_suffix=%s: %w", method, path, keyLength, keySuffix, err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read NOWPayments response: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf(
			"NOWPayments request failed method=%s path=%s status=%d key_len=%d key_suffix=%s response=%s",
			method, path, resp.StatusCode, keyLength, keySuffix, truncateNowPaymentsResponse(responseBody),
		)
	}
	if err := common.Unmarshal(responseBody, result); err != nil {
		return fmt.Errorf("decode NOWPayments response: %w", err)
	}
	return nil
}

func VerifyNowPaymentsIPN(payload []byte, signature string) bool {
	secret := strings.TrimSpace(setting.NowPaymentsIPNSecret)
	decodedSignature, err := hex.DecodeString(strings.TrimSpace(signature))
	if secret == "" || err != nil || len(decodedSignature) == 0 {
		return false
	}
	var parsed any
	if err := common.Unmarshal(payload, &parsed); err != nil {
		return false
	}
	canonicalPayload, err := common.Marshal(parsed)
	if err != nil {
		return false
	}
	mac := hmac.New(sha512.New, []byte(secret))
	_, _ = mac.Write(canonicalPayload)
	return hmac.Equal(decodedSignature, mac.Sum(nil))
}

func ParseNowPaymentsExpiry(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		parsed, err = time.Parse("2006-01-02T15:04:05.999Z", value)
	}
	if err != nil {
		return 0
	}
	return parsed.Unix()
}
