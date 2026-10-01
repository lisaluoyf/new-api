package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	plategaBaseURL            = "https://app.platega.io"
	plategaCreatePath         = "/transaction/process"
	plategaPaymentMethodSBPQR = 2
	plategaHTTPTimeout        = 30 * time.Second
)

var plategaHTTPClient = &http.Client{Timeout: plategaHTTPTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("Platega redirect rejected") }}

type PlategaPaymentDetails struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

type PlategaCreateTransactionRequest struct {
	PaymentMethod  int                   `json:"paymentMethod"`
	PaymentDetails PlategaPaymentDetails `json:"paymentDetails"`
	Description    string                `json:"description"`
	Return         string                `json:"return"`
	FailedURL      string                `json:"failedUrl"`
	Payload        string                `json:"payload"`
}

type PlategaCreateTransactionResponse struct {
	ID             string  `json:"id,omitempty"`
	PaymentMethod  string  `json:"paymentMethod"`
	TransactionId  string  `json:"transactionId"`
	Redirect       string  `json:"redirect"`
	Return         string  `json:"return"`
	PaymentDetails string  `json:"paymentDetails"`
	Status         string  `json:"status"`
	ExpiresIn      string  `json:"expiresIn"`
	MerchantId     string  `json:"merchantId"`
	UsdtRate       float64 `json:"usdtRate"`
	CryptoAmount   float64 `json:"cryptoAmount"`
}

type PlategaTransactionStatusResponse struct {
	ID                   string                `json:"id"`
	TransactionId        string                `json:"transactionId"`
	Status               string                `json:"status"`
	Payload              string                `json:"payload"`
	Amount               float64               `json:"amount"`
	Currency             string                `json:"currency"`
	PaymentMethod        string                `json:"paymentMethod"`
	MerchantID           string                `json:"merchantId"`
	HistoricalMerchantID string                `json:"mechantId"`
	PaymentDetails       PlategaPaymentDetails `json:"paymentDetails"`
	Commission           *float64              `json:"comission"`
	RefundStatus         *string               `json:"refundStatus"`
	RawJSON              string                `json:"-"`
}

type PlategaCallbackPayload struct {
	ID             string          `json:"id,omitempty"`
	TransactionId  string          `json:"transactionId"`
	Status         string          `json:"status"`
	Payload        string          `json:"payload"`
	Amount         json.RawMessage `json:"amount"`
	Currency       string          `json:"currency"`
	PaymentMethod  json.RawMessage `json:"paymentMethod"` // Platega sends 2 (int) or "SBPQR" (string)
	PaymentDetails json.RawMessage `json:"paymentDetails"`
}

func PlategaMerchantID() string {
	return strings.TrimSpace(os.Getenv("PLATEGA_MERCHANT_ID"))
}

func PlategaSecret() string {
	return strings.TrimSpace(os.Getenv("PLATEGA_X_SECRET"))
}

func PlategaConfigured() bool {
	return PlategaMerchantID() != "" && PlategaSecret() != ""
}

func CreatePlategaTransaction(ctx context.Context, req *PlategaCreateTransactionRequest) (*PlategaCreateTransactionResponse, []byte, error) {
	if !PlategaConfigured() {
		return nil, nil, fmt.Errorf("platega credentials not configured")
	}
	if req == nil {
		return nil, nil, fmt.Errorf("missing platega request")
	}
	req.PaymentMethod = plategaPaymentMethodSBPQR
	if req.PaymentDetails.Currency == "" {
		req.PaymentDetails.Currency = "RUB"
	}
	if strings.TrimSpace(req.Description) == "" {
		req.Description = "APIMaster.ai balance top-up"
	}

	body, err := common.Marshal(req)
	if err != nil {
		return nil, nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, plategaBaseURL+plategaCreatePath, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-MerchantId", PlategaMerchantID())
	httpReq.Header.Set("X-Secret", PlategaSecret())

	resp, err := plategaHTTPClient.Do(httpReq)
	if err != nil {
		return nil, body, fmt.Errorf("platega create request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, body, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, body, fmt.Errorf("platega create error (%d)", resp.StatusCode)
	}

	var result PlategaCreateTransactionResponse
	if err := common.Unmarshal(respBody, &result); err != nil {
		return nil, body, fmt.Errorf("decode platega create response: %w", err)
	}
	if strings.TrimSpace(result.TransactionId) == "" || strings.TrimSpace(result.Redirect) == "" {
		id, idErr := ResolvePlategaID(result.ID, result.TransactionId)
		if idErr != nil || strings.TrimSpace(result.Redirect) == "" {
			return nil, body, fmt.Errorf("platega returned incomplete transaction response")
		}
		result.TransactionId = id
	}
	if _, err := ResolvePlategaID(result.ID, result.TransactionId); err != nil {
		return nil, body, err
	}
	return &result, body, nil
}

func GetPlategaTransactionStatus(ctx context.Context, transactionId string) (*PlategaTransactionStatusResponse, error) {
	if !PlategaConfigured() {
		return nil, fmt.Errorf("platega credentials not configured")
	}
	transactionId = strings.TrimSpace(transactionId)
	if _, err := ResolvePlategaID(transactionId, ""); err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/transaction/%s", plategaBaseURL, transactionId)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-MerchantId", PlategaMerchantID())
	httpReq.Header.Set("X-Secret", PlategaSecret())

	resp, err := plategaHTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("platega status request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("platega status error (%d)", resp.StatusCode)
	}

	var result PlategaTransactionStatusResponse
	if err := common.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode platega status response: %w", err)
	}
	result.RawJSON = string(respBody)
	if err := NormalizePlategaStatusResponse(&result, transactionId); err != nil {
		return nil, err
	}
	return &result, nil
}

func ParsePlategaCallbackAmount(raw json.RawMessage) (float64, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("missing amount")
	}
	return parsePlategaMoney(raw)
}

func PlategaAmountsMatch(expected, actual float64) bool {
	return expected > 0 && actual > 0 && !math.IsNaN(expected) && !math.IsNaN(actual) && !math.IsInf(expected, 0) && !math.IsInf(actual, 0) && math.Abs(expected-actual) < 0.000001
}
