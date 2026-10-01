package service

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
)

var plategaMoneyPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)
var plategaIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,100}$`)

func parsePlategaMoney(raw json.RawMessage) (float64, error) {
	s := strings.TrimSpace(string(raw))
	if strings.HasPrefix(s, `"`) {
		if err := common.Unmarshal(raw, &s); err != nil {
			return 0, errors.New("invalid money")
		}
	}
	if !plategaMoneyPattern.MatchString(s) {
		return 0, errors.New("invalid money: positive decimal with at most two fractional digits required")
	}
	d, err := decimal.NewFromString(s)
	if err != nil || !d.IsPositive() || !d.Equal(d.Truncate(2)) || d.GreaterThan(decimal.NewFromInt(1000000000)) {
		return 0, errors.New("money out of range or fractional kopecks")
	}
	return d.InexactFloat64(), nil
}

func ResolvePlategaID(id, legacy string) (string, error) {
	if id != "" && legacy != "" && id != legacy {
		return "", errors.New("conflicting transaction IDs")
	}
	if id == "" {
		id = legacy
	}
	if !plategaIDPattern.MatchString(id) {
		return "", errors.New("missing or invalid transaction ID")
	}
	return id, nil
}

func AuthenticatePlategaCallback(headers http.Header) error {
	for name, want := range map[string]string{"X-MerchantId": PlategaMerchantID(), "X-Secret": PlategaSecret()} {
		values := headers.Values(name)
		if want == "" || len(values) != 1 || subtle.ConstantTimeCompare([]byte(values[0]), []byte(want)) != 1 {
			return errors.New("invalid Platega callback authentication")
		}
	}
	return nil
}

func NormalizePlategaStatusResponse(r *PlategaTransactionStatusResponse, requested string) error {
	id, err := ResolvePlategaID(r.ID, r.TransactionId)
	if err != nil || id != requested {
		return errors.New("API transaction identity mismatch")
	}
	if r.MerchantID != "" && r.HistoricalMerchantID != "" && r.MerchantID != r.HistoricalMerchantID {
		return errors.New("API merchant identity conflict")
	}
	mid := r.MerchantID
	if mid == "" {
		mid = r.HistoricalMerchantID
	}
	if mid == "" || mid != PlategaMerchantID() {
		return errors.New("API merchant identity mismatch")
	}
	// Current API's paymentDetails contains the billed amount, including the
	// provider's recorded fee. Never manufacture a fee from a configured rate.
	amount := r.PaymentDetails.Amount
	currency := r.PaymentDetails.Currency
	if amount == 0 && currency == "" {
		amount = r.Amount
		currency = r.Currency
	}
	if r.Amount != 0 && !PlategaAmountsMatch(r.Amount, amount) {
		return errors.New("API amount fields conflict")
	}
	if r.Currency != "" && r.Currency != currency {
		return errors.New("API currency fields conflict")
	}
	b, _ := common.Marshal(amount)
	if _, err = parsePlategaMoney(b); err != nil {
		return err
	}
	if currency != "RUB" {
		return errors.New("invalid API currency")
	}
	if strings.TrimSpace(r.Payload) == "" {
		return errors.New("missing API order payload")
	}
	r.TransactionId = id
	r.Amount = amount
	r.Currency = currency
	return nil
}

// ExpectedPlategaBill checks both locally frozen base money and the invoice
// returned at creation. Legacy invoices lacking a verifiable billed amount
// require review rather than a permissive percentage range.
func ExpectedPlategaBill(order *model.PlategaOrder) (float64, error) {
	var req PlategaCreateTransactionRequest
	if err := common.UnmarshalJsonStr(order.CreateRequestJSON, &req); err != nil {
		return 0, errors.New("missing valid creation request")
	}
	if req.Payload != order.Payload || order.Payload != order.TradeNo || req.PaymentDetails.Currency != "RUB" || !PlategaAmountsMatch(req.PaymentDetails.Amount, order.RubAmount) {
		return 0, errors.New("local creation identity or base amount conflict")
	}
	var resp struct {
		ID             string          `json:"id"`
		TransactionID  string          `json:"transactionId"`
		MerchantID     string          `json:"merchantId"`
		PaymentDetails json.RawMessage `json:"paymentDetails"`
	}
	if err := common.UnmarshalJsonStr(order.CreateResponseJSON, &resp); err != nil {
		return 0, errors.New("missing creation invoice")
	}
	id, err := ResolvePlategaID(resp.ID, resp.TransactionID)
	if err != nil || id != order.PlategaTransactionId || resp.MerchantID != PlategaMerchantID() {
		return 0, errors.New("creation invoice identity conflict")
	}
	var invoice string
	if common.Unmarshal(resp.PaymentDetails, &invoice) == nil {
		parts := strings.Fields(invoice)
		if len(parts) != 2 || parts[1] != "RUB" {
			return 0, errors.New("invalid invoice currency")
		}
		amount, err := parsePlategaMoney(json.RawMessage(parts[0]))
		if err != nil || amount < order.RubAmount {
			return 0, errors.New("invalid invoice amount")
		}
		return amount, nil
	}
	var details PlategaPaymentDetails
	if err := common.Unmarshal(resp.PaymentDetails, &details); err != nil || details.Currency != "RUB" {
		return 0, errors.New("invalid invoice details")
	}
	b, _ := common.Marshal(details.Amount)
	if _, err := parsePlategaMoney(b); err != nil || details.Amount < order.RubAmount {
		return 0, errors.New("invalid invoice amount")
	}
	return details.Amount, nil
}

func ValidatePlategaCallbackOrder(p *PlategaCallbackPayload, o *model.PlategaOrder) error {
	id, err := ResolvePlategaID(p.ID, p.TransactionId)
	if err != nil {
		return err
	}
	if id != o.PlategaTransactionId || p.Payload != o.Payload || p.Payload != o.TradeNo {
		return errors.New("callback order identity mismatch")
	}
	amount, err := ParsePlategaCallbackAmount(p.Amount)
	if err != nil {
		return err
	}
	if p.Currency != "RUB" {
		return errors.New("callback currency mismatch")
	}
	expected, err := ExpectedPlategaBill(o)
	if err != nil {
		return err
	}
	if !PlategaAmountsMatch(expected, amount) {
		return fmt.Errorf("callback billed amount mismatch")
	}
	if len(p.PaymentDetails) > 0 {
		var d PlategaPaymentDetails
		if common.Unmarshal(p.PaymentDetails, &d) != nil || d.Currency != p.Currency || !PlategaAmountsMatch(d.Amount, amount) {
			return errors.New("callback money fields conflict")
		}
	}
	if string(p.PaymentMethod) != "2" && string(p.PaymentMethod) != `"SBPQR"` {
		return errors.New("callback payment method mismatch")
	}
	return nil
}

func ValidatePlategaAPIOrder(r *PlategaTransactionStatusResponse, o *model.PlategaOrder) error {
	if err := NormalizePlategaStatusResponse(r, o.PlategaTransactionId); err != nil {
		return err
	}
	if r.Payload != o.Payload || o.Payload != o.TradeNo {
		return errors.New("API order identity mismatch")
	}
	expected, err := ExpectedPlategaBill(o)
	if err != nil {
		return err
	}
	if !PlategaAmountsMatch(expected, r.Amount) || r.PaymentMethod != "SBPQR" {
		return errors.New("API billed amount or payment method mismatch")
	}
	return nil
}
