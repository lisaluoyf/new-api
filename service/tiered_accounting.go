package service

import (
	"fmt"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func applyTieredAccountingContext(input *ConsumeAccountingInput, info *relaycommon.RelayInfo, usage *dto.Usage) {
	input.ProcurementTieredSnapshot = info.ProcurementTieredBillingSnapshot
	input.WalletTieredSnapshot = info.WalletTieredBillingSnapshot
	input.TieredRequestInput = info.BillingRequestInput
	input.TieredUsage = usage
}

func tieredAccountingAmount(input ConsumeAccountingInput, snapshot *billingexpr.BillingSnapshot) (float64, *billingexpr.TieredResult, error) {
	if snapshot == nil {
		return 0, nil, nil
	}
	usage := input.TieredUsage
	if usage == nil {
		usage = &dto.Usage{PromptTokens: input.InputTokens, CompletionTokens: input.OutputTokens}
		usage.PromptTokensDetails.CachedTokens = input.CacheReadTokens
		usage.PromptTokensDetails.CachedCreationTokens = input.CacheWriteTokens
		if input.CacheWriteTokens5m+input.CacheWriteTokens1h > 0 {
			usage.UsageSemantic = "anthropic"
			usage.ClaudeCacheCreation5mTokens = input.CacheWriteTokens5m
			usage.ClaudeCacheCreation1hTokens = input.CacheWriteTokens1h
		}
	}
	request := billingexpr.RequestInput{}
	if input.TieredRequestInput != nil {
		request = *input.TieredRequestInput
	}
	result, err := billingexpr.ComputeTieredQuotaWithRequest(snapshot, BuildTieredTokenParams(usage, input.InputTokensIncludeCache, billingexpr.UsedVars(snapshot.ExprString)), request)
	if err != nil {
		return 0, nil, err
	}
	if snapshot.QuotaPerUnit <= 0 {
		return 0, nil, fmt.Errorf("invalid tiered quota unit")
	}
	return result.ActualQuotaBeforeGroup / snapshot.QuotaPerUnit, &result, nil
}

// Apply exact tier amounts after the legacy unit-price metadata is assembled.
// The metadata remains useful, but no longer masquerades as the actual amount
// of a request that crossed a tier. Retail settlement is not repeated here.
func applyTieredAccountingFields(input ConsumeAccountingInput, fields *model.AccountingLogFields, snap *consumeAccountingSnapshot) error {
	if normalizedAccountingBillingMode(input) != accountingBillingModeToken {
		return nil
	}
	procurement := input.ProcurementTieredSnapshot
	if procurement == nil {
		var err error
		procurement, err = ResolveTokenBillingExpression(input.ChannelId, input.ModelName, true, input.BillingAt)
		if err != nil {
			return err
		}
	}
	if procurement != nil {
		amount, result, err := tieredAccountingAmount(input, procurement)
		if err != nil {
			return err
		}
		fields.ChannelCostAmountUSD = amount
		snap.AmountsUSD["channel_cost"] = amount
		snap.Prices["channel_tiered_rule"] = procurement
		snap.Prices["channel_matched_tier"] = result.MatchedTier
		snap.AccountingAmountVersion = "tiered_accounting_v3"
	}
	if input.WalletTieredSnapshot != nil {
		amount, result, err := tieredAccountingAmount(input, input.WalletTieredSnapshot)
		if err != nil {
			return err
		}
		fields.UserPriceAmountUSD = amount
		if !input.UseQuotaForUserAmounts {
			fields.UserFinalAmountUSD = amount * input.GroupRatio
		}
		if input.ZeroUserCharge {
			fields.UserPriceAmountUSD = 0
			fields.UserFinalAmountUSD = 0
		}
		snap.AmountsUSD["user_price"] = fields.UserPriceAmountUSD
		snap.AmountsUSD["user_final"] = fields.UserFinalAmountUSD
		snap.Prices["wallet_tiered_rule"] = input.WalletTieredSnapshot
		snap.Prices["wallet_matched_tier"] = result.MatchedTier
	}
	if fields.ResellerUserId > 0 && fields.ResellerRuleId > 0 {
		if expression, ok := GlobalTokenBillingExpression(input.ModelName); ok {
			s := &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: expression, ExprHash: billingexpr.ExprHashString(expression), QuotaPerUnit: 1, GroupRatio: 1, EvaluatedAtUnix: input.BillingAt.Unix()}
			amount, _, err := tieredAccountingAmount(input, s)
			if err != nil {
				return err
			}
			fields.ResellerCostAmountUSD = amount * fields.ResellerDiscountRatio
			snap.AmountsUSD["reseller_cost"] = fields.ResellerCostAmountUSD
		}
	}
	return nil
}
