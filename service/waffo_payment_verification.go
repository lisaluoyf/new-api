package service

import (
	"context"
	"errors"
	"strconv"

	"github.com/QuantumNous/new-api/setting"
)

func VerifyWaffoPancakePaidOrder(ctx context.Context, event *waffoPancakeWebhookEvent, trade string) (float64, string, error) {
	if event == nil || event.Data.OrderID == "" || event.StoreID != setting.WaffoPancakeStoreID {
		return 0, "", errors.New("invalid Waffo order or store")
	}
	expectedMode := "prod"
	if setting.WaffoPancakeSandbox {
		expectedMode = "test"
	}
	if event.Mode != expectedMode {
		return 0, "", errors.New("Waffo environment mismatch")
	}
	var result struct {
		Payments []waffoRefundPayment `json:"payments"`
	}
	err := waffoRefundQuery(ctx, `query($id:String!){payments(filter:{orderId:{eq:$id},status:{eq:"succeeded"}},limit:2){id orderId status testMode orderMerchantExternalId snapshotAmountDetails{currency subtotal total} onetimeOrder{id storeId metadata}}}`, map[string]any{"id": event.Data.OrderID}, &result)
	if err != nil {
		return 0, "", err
	}
	if len(result.Payments) != 1 {
		return 0, "", errors.New("Waffo paid order not uniquely found")
	}
	payment := result.Payments[0]
	if payment.OrderID != event.Data.OrderID || payment.Status != "succeeded" || payment.TestMode != setting.WaffoPancakeSandbox || payment.OnetimeOrder == nil || payment.OnetimeOrder.ID != payment.OrderID || payment.OnetimeOrder.StoreID != setting.WaffoPancakeStoreID || (event.Data.PaymentID != "" && payment.ID != event.Data.PaymentID) {
		return 0, "", errors.New("Waffo official payment identity mismatch")
	}
	reference, err := resolveVerifiedWaffoTradeNo(ctx, payment, false)
	if err != nil || reference != trade {
		return 0, "", errors.New("Waffo official merchant order mismatch")
	}
	amount, err := strconv.ParseFloat(payment.Snapshot.Subtotal, 64)
	if err != nil {
		return 0, "", err
	}
	return amount, payment.Snapshot.Currency, nil
}

// QueryWaffoReconciliationPayment is read-only and never binds or settles an order.
type WaffoReconciliationPayment struct{ ID, Status, Currency, Amount string }

func QueryWaffoReconciliationPayment(ctx context.Context, orderID, trade string) (*WaffoReconciliationPayment, error) {
	var result struct {
		Payments []waffoRefundPayment `json:"payments"`
	}
	err := waffoRefundQuery(ctx, `query($id:String!){payments(filter:{orderId:{eq:$id}},limit:2){id orderId status testMode orderMerchantExternalId snapshotAmountDetails{currency subtotal total} onetimeOrder{id storeId metadata}}}`, map[string]any{"id": orderID}, &result)
	if err != nil {
		return nil, err
	}
	if len(result.Payments) != 1 {
		return nil, errors.New("Waffo payment not uniquely identified")
	}
	p := result.Payments[0]
	if p.OrderID != orderID || p.TestMode != setting.WaffoPancakeSandbox || p.OnetimeOrder == nil || p.OnetimeOrder.ID != orderID || p.OnetimeOrder.StoreID != setting.WaffoPancakeStoreID {
		return nil, errors.New("Waffo identity mismatch")
	}
	reference, err := resolveVerifiedWaffoTradeNo(ctx, p, false)
	if err != nil || reference != trade {
		return nil, errors.New("Waffo merchant order mismatch")
	}
	return &WaffoReconciliationPayment{ID: p.ID, Status: p.Status, Currency: p.Snapshot.Currency, Amount: p.Snapshot.Subtotal}, nil
}
