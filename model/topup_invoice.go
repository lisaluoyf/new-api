package model

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

var ErrInvoiceUnavailable = errors.New("invoice is only available for successful paid orders")

// TopupInvoice contains only fields needed for the payment document.
type TopupInvoice struct {
	ID            int
	UserID        int
	CustomerName  string
	CustomerEmail string
	TradeNo       string
	PaidAt        int64
	Description   string
	PaymentMethod string
	AmountPaid    string
	HasRefund     bool
}

// GetTopupInvoice scopes the database lookup to the authenticated customer.
// Only the separately protected admin route may request another user's order.
func GetTopupInvoice(id, userID int, admin bool) (*TopupInvoice, error) {
	query := DB.Where("id = ?", id)
	if !admin {
		query = query.Where("user_id = ?", userID)
	}
	var topup TopUp
	if err := query.First(&topup).Error; err != nil {
		return nil, err
	}
	if topup.Status != common.TopUpStatusSuccess || topup.PaymentMethod == PaymentMethodFree ||
		topup.Money <= 0 || math.IsNaN(topup.Money) || math.IsInf(topup.Money, 0) {
		return nil, ErrInvoiceUnavailable
	}

	invoice := &TopupInvoice{
		ID: topup.Id, UserID: topup.UserId, TradeNo: topup.TradeNo,
		PaidAt: topup.CompleteTime, Description: "API wallet credit",
		PaymentMethod: topup.PaymentMethod,
		AmountPaid:    FormatTopupPaidAmount(topup.Money, topup.PaymentMethod),
		HasRefund:     topup.RefundedAmount > 0 || topup.RefundedQuota > 0,
	}
	// Always use the order owner's profile, including for admin downloads.
	var customer User
	if err := DB.Select("id", "display_name", "username", "email").Where("id = ?", topup.UserId).First(&customer).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	} else {
		invoice.CustomerName = strings.TrimSpace(customer.DisplayName)
		if invoice.CustomerName == "" {
			invoice.CustomerName = strings.TrimSpace(customer.Username)
		}
		invoice.CustomerEmail = strings.TrimSpace(customer.Email)
	}
	// Subscription top-up rows store USD, even when the provider charged CNY
	// or RUB. Use the original payment snapshot rather than today's FX rate.
	var order SubscriptionOrder
	err := DB.Where("trade_no = ? AND user_id = ?", topup.TradeNo, topup.UserId).First(&order).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if err == nil {
		invoice.Description = "API subscription"
		invoice.AmountPaid = FormatTopupPaidAmount(topup.Money, PaymentMethodStripe)
		switch order.PaymentProvider {
		case PaymentProviderEpay:
			invoice.AmountPaid, err = invoiceSnapshotAmount(order.ProviderPayload)
			if err != nil {
				return nil, err
			}
		case PaymentProviderPlatega:
			var payment PlategaOrder
			if err := DB.Where("trade_no = ?", topup.TradeNo).First(&payment).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil, ErrInvoiceUnavailable
				}
				return nil, err
			}
			if payment.RubAmount <= 0 {
				return nil, ErrInvoiceUnavailable
			}
			invoice.AmountPaid = fmt.Sprintf("RUB %.2f", payment.RubAmount)
		}
	}
	invoice.AmountPaid = strings.NewReplacer("$", "USD ", "¥", "CNY ", "₽", "RUB ", "€", "EUR ").Replace(invoice.AmountPaid)
	return invoice, nil
}

// Unlike display-only currency formatting, invoices must never default an
// unknown or missing currency to USD.
func invoiceSnapshotAmount(payload string) (string, error) {
	type charge struct {
		Amount   string `json:"charge_amount"`
		Currency string `json:"charge_currency"`
	}
	var snapshot struct {
		charge
		PaymentSnapshot charge `json:"payment_snapshot"`
	}
	if err := common.UnmarshalJsonStr(payload, &snapshot); err != nil {
		return "", ErrInvoiceUnavailable
	}
	actual := snapshot.PaymentSnapshot
	if actual.Amount == "" {
		actual = snapshot.charge
	}
	currency := strings.ToUpper(strings.TrimSpace(actual.Currency))
	if len(currency) != 3 || strings.IndexFunc(currency, func(r rune) bool { return r < 'A' || r > 'Z' }) >= 0 {
		return "", ErrInvoiceUnavailable
	}
	amount, err := decimal.NewFromString(strings.TrimSpace(actual.Amount))
	if err != nil || !amount.IsPositive() {
		return "", ErrInvoiceUnavailable
	}
	return currency + " " + amount.StringFixed(2), nil
}
