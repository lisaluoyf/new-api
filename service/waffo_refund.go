package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const waffoRefundAPIURL = "https://api.waffo.ai"

var waffoRefundSyncMu sync.Mutex
var waffoRefundHTTPClient = &http.Client{Timeout: 25 * time.Second}

type waffoRefundAmount struct {
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
}

type waffoRefundTicket struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	SubjectID   string `json:"subjectId"`
	Version     int    `json:"versionNumber"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
	VersionData struct {
		Reason          string             `json:"reason"`
		RequestedAmount *waffoRefundAmount `json:"requestedAmount"`
	} `json:"versionData"`
}

type waffoRefundPayment struct {
	ID                      string `json:"id"`
	OrderID                 string `json:"orderId"`
	Status                  string `json:"status"`
	TestMode                bool   `json:"testMode"`
	OrderMerchantExternalID string `json:"orderMerchantExternalId"`
	Snapshot                struct {
		Currency string `json:"currency"`
		Subtotal string `json:"subtotal"`
		Total    string `json:"total"`
	} `json:"snapshotAmountDetails"`
	OnetimeOrder *struct {
		ID       string `json:"id"`
		StoreID  string `json:"storeId"`
		Metadata string `json:"metadata"`
	} `json:"onetimeOrder"`
	Refunds []struct {
		TicketID        string             `json:"ticketId"`
		Status          string             `json:"status"`
		RequestedAmount *waffoRefundAmount `json:"requestedAmountDetails"`
	} `json:"refunds"`
}

func waffoRefundRequest(ctx context.Context, path string, payload any, out any) error {
	body, err := common.Marshal(payload)
	if err != nil {
		return err
	}
	key, err := normalizeRSAPrivateKey(setting.WaffoPancakePrivateKey)
	if err != nil {
		return err
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature, err := signWaffoPancakeRequest(http.MethodPost, path, timestamp, string(body), key)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, waffoRefundAPIURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Merchant-Id", setting.WaffoPancakeMerchantID)
	req.Header.Set("X-Timestamp", timestamp)
	req.Header.Set("X-Signature", signature)
	req.Header.Set("X-Environment", "prod")
	resp, err := waffoRefundHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := common.DecodeJson(io.LimitReader(resp.Body, 4<<20), &envelope); err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK || len(envelope.Errors) > 0 || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return fmt.Errorf("Waffo refund API failed: status=%d errors=%v", resp.StatusCode, envelope.Errors)
	}
	return common.Unmarshal(envelope.Data, out)
}

func waffoRefundQuery(ctx context.Context, query string, variables map[string]any, out any) error {
	return waffoRefundRequest(ctx, "/v1/graphql", map[string]any{"query": query, "variables": variables}, out)
}

// An explicit cutoff prevents a new deployment from re-debiting old refunds
// that operators may already have handled manually.
func waffoRefundCutoff() (time.Time, error) {
	raw := strings.TrimSpace(os.Getenv("WAFFO_REFUND_SYNC_SINCE"))
	if raw == "" {
		return time.Time{}, errors.New("WAFFO_REFUND_SYNC_SINCE is not configured")
	}
	return time.Parse(time.RFC3339, raw)
}

func StartWaffoRefundSyncTask() {
	if !common.IsMasterNode {
		return
	}
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			if os.Getenv("WAFFO_REFUND_SYNC_SINCE") != "" {
				ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
				if err := SyncWaffoRefunds(ctx); err != nil {
					common.SysLog("Waffo refund sync: " + err.Error())
				}
				cancel()
			}
			<-ticker.C
		}
	}()
}

func SyncWaffoRefunds(ctx context.Context) error {
	waffoRefundSyncMu.Lock()
	defer waffoRefundSyncMu.Unlock()
	cutoff, err := waffoRefundCutoff()
	if err != nil {
		return err
	}
	if setting.WaffoPancakeSandbox || setting.WaffoPancakeMerchantID == "" || setting.WaffoPancakeStoreID == "" {
		return errors.New("Waffo refund sync requires a configured production merchant")
	}
	var failures []error
	for offset := 0; ; offset += 100 {
		var result struct {
			Tickets []waffoRefundTicket `json:"refundTickets"`
		}
		err := waffoRefundQuery(ctx, `query($since:String!,$offset:Int!){refundTickets(limit:100,offset:$offset,filter:{createdAt:{gte:$since}}){id status subjectId versionNumber versionData{reason requestedAmount{currency amount}} createdAt updatedAt}}`, map[string]any{"since": cutoff.Format(time.RFC3339), "offset": offset}, &result)
		if err != nil {
			return errors.Join(append(failures, err)...)
		}
		for _, ticket := range result.Tickets {
			if err := syncWaffoRefundTicket(ctx, ticket); err != nil {
				failures = append(failures, fmt.Errorf("ticket %s: %w", ticket.ID, err))
			}
		}
		if len(result.Tickets) < 100 {
			break
		}
	}
	return errors.Join(failures...)
}

func syncWaffoRefundTicket(ctx context.Context, ticket waffoRefundTicket) error {
	var existing model.WaffoRefund
	lookup := model.DB.Where("ticket_id = ? AND store_id = ?", ticket.ID, setting.WaffoPancakeStoreID).First(&existing).Error
	if lookup != nil && !errors.Is(lookup, gorm.ErrRecordNotFound) {
		return lookup
	}
	if lookup == nil && existing.Status == "succeeded" {
		return model.InvalidateUserCache(existing.UserID)
	}
	var result struct {
		Payments []waffoRefundPayment `json:"payments"`
	}
	err := waffoRefundQuery(ctx, `query($id:String!){payments(filter:{id:{eq:$id}},limit:2){id orderId status testMode orderMerchantExternalId snapshotAmountDetails{currency subtotal total} onetimeOrder{id storeId metadata} refunds{ticketId status requestedAmountDetails{currency amount}}}}`, map[string]any{"id": ticket.SubjectID}, &result)
	if err != nil {
		return err
	}
	if len(result.Payments) != 1 {
		return errors.New("refund payment not uniquely found")
	}
	payment := result.Payments[0]
	if payment.ID != ticket.SubjectID || payment.TestMode || payment.Status != "succeeded" || payment.OnetimeOrder == nil || payment.OnetimeOrder.ID != payment.OrderID {
		return errors.New("refund does not reference a successful production wallet payment")
	}
	if payment.OnetimeOrder.StoreID != setting.WaffoPancakeStoreID {
		return nil
	}
	tradeNo, err := resolveWaffoRefundTradeNo(ctx, payment)
	if err != nil {
		return err
	}
	entry, err := makeWaffoRefundEntry(ticket, payment, tradeNo)
	if err != nil {
		return err
	}
	return model.ApplyWaffoRefund(entry)
}

func makeWaffoRefundEntry(ticket waffoRefundTicket, payment waffoRefundPayment, tradeNo string) (model.WaffoRefund, error) {
	entry := model.WaffoRefund{}
	if ticket.VersionData.RequestedAmount == nil {
		return entry, errors.New("missing requested refund amount")
	}
	updated, err := time.Parse(time.RFC3339Nano, ticket.UpdatedAt)
	if err != nil {
		return entry, err
	}
	amount := *ticket.VersionData.RequestedAmount
	if ticket.Status == "succeeded" {
		// The approved amount can differ from the requested amount. Only
		// actual successful refund records authorize the final debit.
		total := decimal.Zero
		for _, refund := range payment.Refunds {
			if refund.TicketID != ticket.ID || refund.Status != "succeeded" {
				continue
			}
			if refund.RequestedAmount == nil || refund.RequestedAmount.Currency != amount.Currency {
				return entry, errors.New("missing or inconsistent completed refund amount")
			}
			value, err := decimal.NewFromString(refund.RequestedAmount.Amount)
			if err != nil || !value.IsPositive() {
				return entry, errors.New("invalid completed refund amount")
			}
			total = total.Add(value)
		}
		if !total.IsPositive() {
			return entry, errors.New("completed refund is not yet visible; keep reservation")
		}
		amount.Amount = total.String()
	}
	refundAmount, err := strconv.ParseFloat(amount.Amount, 64)
	if err != nil {
		return entry, err
	}
	paid, err := strconv.ParseFloat(payment.Snapshot.Total, 64)
	if err != nil {
		return entry, err
	}
	if amount.Currency != payment.Snapshot.Currency {
		return entry, errors.New("refund currency differs from payment")
	}
	return model.WaffoRefund{TicketID: ticket.ID, PaymentID: payment.ID, OrderID: payment.OrderID, StoreID: payment.OnetimeOrder.StoreID, TradeNo: tradeNo, Status: ticket.Status, Amount: refundAmount, PaymentAmount: paid, Currency: amount.Currency, Version: ticket.Version, ProviderUpdatedAt: updated.UnixMilli(), Reason: ticket.VersionData.Reason}, nil
}

func resolveWaffoRefundTradeNo(ctx context.Context, payment waffoRefundPayment) (string, error) {
	var local model.TopUp
	if err := model.DB.Where("waffo_payment_id = ? AND waffo_order_id = ? AND payment_provider = ?", payment.ID, payment.OrderID, model.PaymentProviderWaffoPancake).First(&local).Error; err == nil {
		return local.TradeNo, nil
	}
	tradeNo := payment.OrderMerchantExternalID
	if tradeNo == "" && payment.OnetimeOrder.Metadata != "" {
		var metadata map[string]string
		if err := common.UnmarshalJsonStr(payment.OnetimeOrder.Metadata, &metadata); err != nil {
			return "", err
		}
		tradeNo = metadata["tradeNo"]
		if tradeNo == "" {
			tradeNo = metadata["orderId"]
		}
	}
	if tradeNo == "" {
		// Legacy orders expose their merchant reference only in the original
		// payment webhook. Never match refunds using buyer email or amount.
		var deliveries struct {
			Items []struct {
				Payload string `json:"payload"`
			} `json:"webhookDeliveries"`
		}
		err := waffoRefundQuery(ctx, `query($store:String!,$payment:String!){webhookDeliveries(storeId:$store,filter:{eventId:{eq:$payment},eventType:{eq:"order.completed"}},limit:100){payload}}`, map[string]any{"store": setting.WaffoPancakeStoreID, "payment": payment.ID}, &deliveries)
		if err != nil {
			return "", err
		}
		for _, delivery := range deliveries.Items {
			var event waffoPancakeWebhookEvent
			if err := common.UnmarshalJsonStr(delivery.Payload, &event); err != nil {
				return "", err
			}
			if event.Data.OrderID != payment.OrderID || event.StoreID != setting.WaffoPancakeStoreID || event.Mode != "prod" || (event.ID != payment.ID && event.EventID != payment.ID) {
				continue
			}
			candidate, err := ResolveWaffoPancakeTradeNo(&event)
			if err != nil {
				continue
			}
			if tradeNo != "" && candidate != tradeNo {
				return "", errors.New("conflicting Waffo merchant references")
			}
			tradeNo = candidate
		}
	}
	if tradeNo == "" {
		return "", errors.New("no exact local order reference for refund")
	}
	topUp := model.GetTopUpByTradeNo(tradeNo)
	subtotal, err := strconv.ParseFloat(payment.Snapshot.Subtotal, 64)
	if err != nil || math.IsNaN(subtotal) || math.IsInf(subtotal, 0) || topUp == nil || math.Abs(topUp.Money-subtotal) > 0.005 {
		return "", errors.New("local order amount differs from upstream payment")
	}
	if err := model.BindWaffoPayment(tradeNo, payment.ID, payment.OrderID); err != nil {
		return "", err
	}
	return tradeNo, nil
}
