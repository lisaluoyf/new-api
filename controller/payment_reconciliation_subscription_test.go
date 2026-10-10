package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

func TestEpaySubscriptionReconciliationUsesFrozenCNYAmount(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionOrder{}, &model.PaymentQueryReference{}))
	day := time.Date(2026, 10, 9, 0, 0, 0, 0, reconciliationTimezone).Unix()
	snapshot := subscriptionEpayPaymentSnapshot{PayableUSD: 20, ExchangeRate: 7, ChargeAmount: "140.00", ChargeCurrency: "CNY"}
	payload := common.GetJsonString(map[string]any{"payment_snapshot": snapshot, "callback": map[string]string{"Money": "140.00"}})
	order := model.SubscriptionOrder{TradeNo: "subscription-cny", Money: 20, Status: "success", PaymentProvider: "epay", PaymentMethod: "alipay", CreateTime: day, CompleteTime: day + 1, ProviderPayload: payload}
	require.NoError(t, db.Create(&order).Error)
	require.NoError(t, db.Create(&model.TopUp{TradeNo: order.TradeNo, Money: 20, Status: "success", PaymentProvider: "epay", PaymentMethod: "alipay", CreateTime: day, CompleteTime: day + 1}).Error)
	rows, err := loadReconciliationCandidates(day, day+86400, "epay")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	proof := reconciliationProof{id: "official", known: true, paid: true, status: "TRADE_SUCCESS", currency: "CNY", amount: "140.00"}
	item := classifyReconciliationOrder(rows[0], proof)
	require.Equal(t, "matched", item.Result)
	require.Equal(t, "140", item.LocalAmount)
	require.Equal(t, "CNY", item.Currency)
	summary := model.SummarizePaymentReconciliation([]model.PaymentReconciliationItem{item})
	require.Equal(t, "140", summary.Totals[0].LocalAmount)
	require.Equal(t, "0", summary.Totals[0].Difference)
	// The statement-only path must use the same frozen amount.
	items, err := mergeReconciliationStatement("epay", nil, []service.StatementPayment{{ID: "official", TradeNo: order.TradeNo, Status: "TRADE_SUCCESS", Amount: "140.00", Currency: "CNY"}})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "official_paid_local_date_mismatch", items[0].Problem)
	require.Equal(t, "140", items[0].LocalAmount)
	// Real discrepancies must remain discrepancies.
	proof.amount = "141.00"
	require.Equal(t, "amount_mismatch", classifyReconciliationOrder(rows[0], proof).Problem)
	proof.amount, proof.currency = "140.00", "USD"
	require.Equal(t, "currency_mismatch", classifyReconciliationOrder(rows[0], proof).Problem)
	var stored model.SubscriptionOrder
	require.NoError(t, db.First(&stored, order.Id).Error)
	require.Equal(t, float64(20), stored.Money)
	require.Equal(t, payload, stored.ProviderPayload)
}

func TestEpaySubscriptionMissingOrInvalidSnapshotNeverUsesUSDAsCNY(t *testing.T) {
	base := reconciliationCandidate{provider: "epay", purpose: "subscription", status: "success", currency: "CNY", money: 20}
	proof := reconciliationProof{known: true, paid: true, status: "TRADE_SUCCESS", currency: "CNY", amount: "140"}
	for _, payload := range []string{
		"", "null", "{", `{"callback":{"Money":"140.00"}}`,
		`{"payable_usd":20,"exchange_rate":7,"charge_amount":"140.00","charge_currency":"USD"}`,
		`{"payable_usd":19,"exchange_rate":7,"charge_amount":"133.00","charge_currency":"CNY"}`,
		`{"payable_usd":20,"exchange_rate":7,"charge_amount":"141.00","charge_currency":"CNY"}`,
	} {
		base.payload = payload
		item := classifyReconciliationOrder(base, proof)
		require.Equal(t, "unverified", item.Result, payload)
		require.Equal(t, "missing_frozen_bill_amount", item.Problem, payload)
		require.Empty(t, item.LocalAmount, payload)
	}
	base.payload = `{"payable_usd":20,"exchange_rate":7,"charge_amount":"140.00","charge_currency":"CNY"}`
	require.Equal(t, "matched", classifyReconciliationOrder(base, proof).Result)
	base.purpose, base.money, base.payload = "wallet", 140, ""
	require.Equal(t, "matched", classifyReconciliationOrder(base, proof).Result)
}

func TestWaffoSubscriptionHistoricalPayloadIsOnlyAnUpstreamQueryHint(t *testing.T) {
	oldQuery := queryWaffoReconciliationPayment
	t.Cleanup(func() { queryWaffoReconciliationPayment = oldQuery })
	r := reconciliationCandidate{trade: "subscription", provider: "waffo_pancake", purpose: "subscription", status: "success", money: 200, currency: "USD", payload: `{"data":{"paymentId":"PAY_saved","orderId":"ORD_saved","currency":"USD","amount":"200.00"}}`}
	calls := 0
	queryWaffoReconciliationPayment = func(ctx context.Context, id, trade string) (*service.WaffoReconciliationPayment, error) {
		calls++
		require.Equal(t, "ORD_saved", id)
		require.Equal(t, r.trade, trade)
		return &service.WaffoReconciliationPayment{ID: "PAY_official", Status: "succeeded", Currency: "USD", Amount: "200.00"}, nil
	}
	p := queryReconciliationOrder(context.Background(), r)
	require.Equal(t, 1, calls)
	require.Equal(t, "PAY_official", p.id)
	require.Equal(t, "matched", classifyReconciliationOrder(r, p).Result)
	queryWaffoReconciliationPayment = func(context.Context, string, string) (*service.WaffoReconciliationPayment, error) {
		return nil, errors.New("official identity mismatch")
	}
	p = queryReconciliationOrder(context.Background(), r)
	require.Equal(t, "unverified", classifyReconciliationOrder(r, p).Result)
	require.Equal(t, "official_query_failed", p.problem)
	for _, payload := range []string{"", "{", `{"data":{"orderId":"PAY_wrong"}}`} {
		r.payload = payload
		require.Equal(t, "missing_official_transaction_id", queryReconciliationOrder(context.Background(), r).problem)
	}
	r.payload = `{"data":{"orderId":"ORD_saved"}}`
	r.purpose = "wallet"
	require.Equal(t, "missing_official_transaction_id", queryReconciliationOrder(context.Background(), r).problem)
	r.purpose, r.queryID = "subscription", "ORD_reference"
	queryWaffoReconciliationPayment = func(_ context.Context, id, _ string) (*service.WaffoReconciliationPayment, error) {
		require.Equal(t, "ORD_reference", id)
		return &service.WaffoReconciliationPayment{ID: "PAY_official", Status: "succeeded", Currency: "USD", Amount: "201.00"}, nil
	}
	require.Equal(t, "amount_mismatch", classifyReconciliationOrder(r, queryReconciliationOrder(context.Background(), r)).Problem)
}
