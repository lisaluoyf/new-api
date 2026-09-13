package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/require"
)

type waffoRefundTestTransport struct{ target *url.URL }

func (r waffoRefundTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = r.target.Scheme
	req.URL.Host = r.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

func TestWaffoRefundSyncLifecycleAndUpstreamFailure(t *testing.T) {
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMerchant, oldKey, oldStore, oldSandbox := setting.WaffoPancakeMerchantID, setting.WaffoPancakePrivateKey, setting.WaffoPancakeStoreID, setting.WaffoPancakeSandbox
	oldClient := waffoRefundHTTPClient
	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		setting.WaffoPancakeMerchantID = oldMerchant
		setting.WaffoPancakePrivateKey = oldKey
		setting.WaffoPancakeStoreID = oldStore
		setting.WaffoPancakeSandbox = oldSandbox
		waffoRefundHTTPClient = oldClient
	})
	db := setupWaffoPancakeTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.WaffoRefund{}))
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "refund-sync", Quota: 10 * int(common.QuotaPerUnit)}).Error)
	require.NoError(t, db.Create(&model.TopUp{UserId: 1, TradeNo: "trade", Amount: 10, Money: 7.5, PaymentProvider: model.PaymentProviderWaffoPancake, PaymentMethod: model.PaymentMethodWaffoPancake, Status: common.TopUpStatusSuccess}).Error)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	setting.WaffoPancakePrivateKey = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	setting.WaffoPancakeMerchantID = "MER_test"
	setting.WaffoPancakeStoreID = "STO_test"
	setting.WaffoPancakeSandbox = false
	t.Setenv("WAFFO_REFUND_SYNC_SINCE", "2026-09-13T00:00:00Z")
	status := "processing"
	version := 1
	updated := "2026-09-13T18:01:01Z"
	upstreamFails := false
	refundVisible := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if upstreamFails {
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unavailable"}]}`))
			return
		}
		if r.Header.Get("X-Environment") != "prod" || r.Header.Get("X-Signature") == "" {
			w.WriteHeader(401)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var request struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := common.Unmarshal(body, &request); err != nil {
			w.WriteHeader(400)
			return
		}
		var data any
		switch {
		case strings.Contains(request.Query, "refundTickets("):
			data = map[string]any{"refundTickets": []any{map[string]any{"id": "TKT_test", "status": status, "subjectId": "PAY_test", "versionNumber": version, "createdAt": "2026-09-13T18:01:00Z", "updatedAt": updated, "versionData": map[string]any{"reason": "customer requested", "requestedAmount": map[string]string{"currency": "USD", "amount": "3.00"}}}}}
		case strings.Contains(request.Query, "payments("):
			refunds := []any{}
			if refundVisible {
				refunds = append(refunds, map[string]any{"ticketId": "TKT_test", "status": "succeeded", "requestedAmountDetails": map[string]string{"currency": "USD", "amount": "3.00"}})
			}
			data = map[string]any{"payments": []any{map[string]any{"id": "PAY_test", "orderId": "ORD_test", "status": "succeeded", "testMode": false, "snapshotAmountDetails": map[string]string{"currency": "USD", "subtotal": "7.50", "total": "7.50"}, "onetimeOrder": map[string]any{"id": "ORD_test", "storeId": "STO_test", "metadata": nil}, "refunds": refunds}}}
		case strings.Contains(request.Query, "webhookDeliveries("):
			payload := `{"id":"PAY_test","eventId":"PAY_test","storeId":"STO_test","mode":"prod","eventType":"order.completed","data":{"orderId":"ORD_test","orderMetadata":{"tradeNo":"trade"}}}`
			data = map[string]any{"webhookDeliveries": []any{map[string]string{"payload": payload}}}
		default:
			w.WriteHeader(400)
			return
		}
		encoded, _ := common.Marshal(map[string]any{"data": data})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(encoded)
	}))
	defer server.Close()
	target, err := url.Parse(server.URL)
	require.NoError(t, err)
	waffoRefundHTTPClient = &http.Client{Transport: waffoRefundTestTransport{target}}
	checkBalance := func(want float64) {
		t.Helper()
		var user model.User
		require.NoError(t, db.First(&user, 1).Error)
		require.Equal(t, int(want*common.QuotaPerUnit), user.Quota)
	}
	require.NoError(t, SyncWaffoRefunds(context.Background()))
	checkBalance(6)
	require.NoError(t, SyncWaffoRefunds(context.Background()))
	checkBalance(6)
	upstreamFails = true
	require.Error(t, SyncWaffoRefunds(context.Background()))
	checkBalance(6)
	upstreamFails = false
	status = "failed"
	updated = "2026-09-13T18:02:00Z"
	require.NoError(t, SyncWaffoRefunds(context.Background()))
	checkBalance(10)
	status = "processing"
	version = 2
	updated = "2026-09-13T18:03:00Z"
	require.NoError(t, SyncWaffoRefunds(context.Background()))
	checkBalance(6)
	status = "succeeded"
	updated = "2026-09-13T18:04:00Z"
	require.ErrorContains(t, SyncWaffoRefunds(context.Background()), "keep reservation")
	checkBalance(6)
	refundVisible = true
	require.NoError(t, SyncWaffoRefunds(context.Background()))
	checkBalance(6)
	var order model.TopUp
	require.NoError(t, db.Where("trade_no = ?", "trade").First(&order).Error)
	require.Equal(t, 0, order.RefundFrozenQuota)
	require.Equal(t, 4*int(common.QuotaPerUnit), order.RefundedQuota)
	require.NoError(t, SyncWaffoRefunds(context.Background()))
	checkBalance(6)
}

func TestWaffoRefundCutoffMustBeExplicit(t *testing.T) {
	t.Setenv("WAFFO_REFUND_SYNC_SINCE", "")
	_, err := waffoRefundCutoff()
	require.Error(t, err)
	t.Setenv("WAFFO_REFUND_SYNC_SINCE", "not-a-time")
	_, err = waffoRefundCutoff()
	require.Error(t, err)
}
