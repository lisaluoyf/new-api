package service

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
)

const referralGPTRewardReconcileInterval = 10 * time.Minute

func StartReferralGPTRewardReconcileTask() {
	go func() {
		timer := time.NewTimer(time.Minute)
		defer timer.Stop()
		<-timer.C
		runReferralGPTRewardReconcile()

		ticker := time.NewTicker(referralGPTRewardReconcileInterval)
		defer ticker.Stop()
		for range ticker.C {
			runReferralGPTRewardReconcile()
		}
	}()
}

func runReferralGPTRewardReconcile() {
	ctx := context.Background()
	if err := model.ReconcileReferralGPTRewards(1000); err != nil {
		logger.LogWarn(ctx, "referral GPT reward reconcile failed: "+err.Error())
	}
	if err := model.RetryPendingReferralGPTRewardNotifications(100); err != nil {
		logger.LogWarn(ctx, "referral GPT reward Feishu retry failed: "+err.Error())
	}
}
