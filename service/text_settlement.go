package service

import (
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func (s *BillingSession) settleText(c *gin.Context, log *model.Log, accounting model.AccountingLogFields) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settled {
		return nil
	}
	if s.refunded || s.fundingSettled {
		return fmt.Errorf("text settlement reservation already resolved")
	}
	if s.funding.Source() == BillingSourceSubscription {
		snapshot := *s.relayInfo
		snapshot.SubscriptionPostDelta = int64(log.Quota - s.preConsumedQuota)
		other, err := common.StrToMap(log.Other)
		if err != nil {
			return err
		}
		appendBillingInfo(&snapshot, other)
		log.Other = common.MapToJsonStr(other)
	}
	item := &model.TextSettlement{RequestId: log.RequestId, UserId: s.relayInfo.UserId,
		TokenId: s.relayInfo.TokenId, ChannelId: log.ChannelId, Quota: log.Quota,
		PreConsumedQuota: s.preConsumedQuota, TokenConsumed: s.tokenConsumed,
		FundingSource: s.funding.Source(), IsPlayground: s.relayInfo.IsPlayground}
	if sub, ok := s.funding.(*SubscriptionFunding); ok {
		item.SubscriptionId = sub.subscriptionId
	}
	if err := model.FreezeTextSettlement(item, log, accounting); err != nil {
		return err
	}
	// Once authoritative usage is durable, late relay cleanup must not refund
	// the reservation while another process retries the corresponding debit.
	s.holdRefund = true
	s.durableSettlementID = item.Id
	result, err := model.ApplyTextSettlement(item.Id)
	if result != nil && result.Status == "settled" {
		s.settled, s.fundingSettled = true, true
		if s.funding.Source() == BillingSourceSubscription {
			s.relayInfo.SubscriptionPostDelta += int64(log.Quota - s.preConsumedQuota)
		}
	}
	if err != nil {
		common.SysLog(fmt.Sprintf("text billing pending request=%s settlement=%d: %s", item.RequestId, item.Id, err.Error()))
		if logErr := model.PublishTextSettlementLog(item.Id); logErr != nil {
			common.SysLog("text settlement log pending: " + logErr.Error())
		}
	}
	// A durable pending debit is not an upstream failure: replaying the model
	// request would create an additional supplier charge for the same output.
	c.Set("text_usage_settled", true)
	return nil
}

var textSettlementOnce sync.Once

func StartTextSettlementTask() {
	if !common.IsMasterNode {
		return
	}
	textSettlementOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				if err := model.RetryTextSettlements(common.GetTimestamp()); err != nil {
					common.SysLog("text settlement retry failed: " + err.Error())
				}
				<-ticker.C
			}
		}()
	})
}
