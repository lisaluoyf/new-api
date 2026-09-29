package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
)

const (
	nowPaymentsRecoveryInterval = 10 * time.Minute
	nowPaymentsRecoveryWindow   = 7 * 24 * time.Hour
)

func StartNowPaymentsRecoveryTask() {
	if !common.IsMasterNode || !setting.NowPaymentsEnabled || strings.TrimSpace(setting.NowPaymentsAPIKey) == "" {
		return
	}
	go func() {
		timer := time.NewTimer(time.Minute)
		defer timer.Stop()
		<-timer.C
		runNowPaymentsRecovery()
		ticker := time.NewTicker(nowPaymentsRecoveryInterval)
		defer ticker.Stop()
		for range ticker.C {
			runNowPaymentsRecovery()
		}
	}()
}

func runNowPaymentsRecovery() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := runNowPaymentsRecoveryOnce(ctx); err != nil {
		logger.LogError(ctx, fmt.Sprintf("NOWPayments recovery failed: %v", err))
		if common.FeishuOpsChatID() == "" || common.FeishuAppID() == "" || common.FeishuAppSecret() == "" {
			return
		}
		now := common.GetTimestamp()
		claimed, claimErr := model.ClaimNowPaymentsReconcileAlert(now)
		if claimErr != nil {
			logger.LogError(ctx, fmt.Sprintf("NOWPayments recovery alert claim failed: %v", claimErr))
			return
		}
		if claimed {
			sendErr := sendNowPaymentsDuplicateAlert(common.FeishuOpsChatID(), common.FeishuNotificationTitle("NOWPayments 自动对账异常"), []string{"单笔付款查询或回调补漏不可用，请人工核对近 7 天的 NOWPayments 充值。", "具体错误请查看 New API worker 日志。"})
			deliveredAt := int64(0)
			if sendErr == nil {
				deliveredAt = common.GetTimestamp()
			}
			if completeErr := model.CompleteNowPaymentsReconcileAlert(now+120, deliveredAt); completeErr != nil {
				logger.LogError(ctx, fmt.Sprintf("NOWPayments recovery alert completion failed: %v", completeErr))
			}
			if sendErr != nil {
				logger.LogError(ctx, fmt.Sprintf("NOWPayments recovery alert failed: %v", sendErr))
			}
		}
	}
}

func runNowPaymentsRecoveryOnce(ctx context.Context) error {
	alerts, err := model.ListPendingNowPaymentsAlerts(100)
	if err != nil {
		return err
	}
	for _, attempt := range alerts {
		if err := deliverNowPaymentsAlert(attempt.Id); err != nil {
			logger.LogError(ctx, fmt.Sprintf("NOWPayments duplicate alert retry failed payment_id=%s: %v", attempt.PaymentID, err))
		}
	}
	now := common.GetTimestamp()
	claimed, err := model.ClaimNowPaymentsScan(now)
	if err != nil || !claimed {
		return err
	}
	defer func() {
		if err := model.ReleaseNowPaymentsScan(now + 180); err != nil {
			logger.LogError(ctx, fmt.Sprintf("NOWPayments scan lease release failed: %v", err))
		}
	}()
	payments, err := model.ListRecentNowPaymentsPayments(time.Now().Add(-nowPaymentsRecoveryWindow).Unix())
	if err != nil {
		return err
	}
	var paymentFailure error
	for _, local := range payments {
		if err := ctx.Err(); err != nil {
			return err
		}
		primaryID := strings.TrimSpace(local.PaymentID)
		remote, err := getNowPaymentsPayment(ctx, primaryID)
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("NOWPayments payment reconcile failed order=%s payment_id=%s: %v", local.TopUpTradeNo, primaryID, err))
			paymentFailure = fmt.Errorf("one or more NOWPayments payments could not be verified")
			continue
		}
		if err := settleNowPaymentsPaymentResponse(&local, remote, primaryID, ""); err != nil {
			logger.LogError(ctx, fmt.Sprintf("NOWPayments payment reconcile failed order=%s payment_id=%s: %v", local.TopUpTradeNo, primaryID, err))
			paymentFailure = fmt.Errorf("one or more NOWPayments payments could not be verified")
			continue
		}
		seen := map[string]struct{}{primaryID: {}}
		for _, extraIDValue := range remote.PaymentExtraIDs {
			extraID := strings.TrimSpace(string(extraIDValue))
			if extraID == "" || extraID == primaryID {
				continue
			}
			if _, exists := seen[extraID]; exists {
				continue
			}
			seen[extraID] = struct{}{}
			extra, extraErr := getNowPaymentsPayment(ctx, extraID)
			if extraErr != nil {
				logger.LogError(ctx, fmt.Sprintf("NOWPayments extra payment lookup failed order=%s payment_id=%s: %v", local.TopUpTradeNo, extraID, extraErr))
				paymentFailure = fmt.Errorf("one or more NOWPayments payments could not be verified")
				continue
			}
			if extraErr = settleNowPaymentsPaymentResponse(&local, extra, extraID, ""); extraErr != nil {
				logger.LogError(ctx, fmt.Sprintf("NOWPayments extra payment reconcile failed order=%s payment_id=%s: %v", local.TopUpTradeNo, extraID, extraErr))
				paymentFailure = fmt.Errorf("one or more NOWPayments payments could not be verified")
			}
		}
	}
	return paymentFailure
}
