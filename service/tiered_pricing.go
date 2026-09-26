package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/billing_setting"
)

// ResolveTokenBillingExpression preserves a supplier's complete token schedule.
// Media/task expressions are settled by their dedicated billing paths, not by
// text token settlement. Global model schedules are the fallback for channels
// that only expose a base price tuple (including manually priced channels).
func ResolveTokenBillingExpression(channelID int, modelName string, procurement bool, at time.Time) (*billingexpr.BillingSnapshot, error) {
	if at.IsZero() {
		at = time.Now()
	}
	var row *ChannelPricingLookupRow
	var ch channelPricingResolveContext
	if channelID > 0 && model.DB != nil {
		var err error
		ch, err = loadChannelPricingResolveContext(channelID)
		if err != nil {
			return nil, err
		}
		row, _ = resolveChannelPricingRow(channelID, modelName, ch)
	}
	if procurement && row == nil {
		return nil, nil
	}
	newSnapshot := func(expression, source string) *billingexpr.BillingSnapshot {
		return &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ModelName: modelName, ExprString: expression,
			ExprHash: billingexpr.ExprHashString(expression), ExprVersion: billingexpr.ExprVersion(expression),
			GroupRatio: 1, QuotaPerUnit: common.QuotaPerUnit, PricingChannelID: channelID, EvaluatedAtUnix: at.Unix(), Source: source}
	}
	if row != nil && row.BillingMode == "tiered_expr" && strings.TrimSpace(row.BillingExpr) != "" {
		// Check token identifiers before compiling: expressions for images/video
		// may use a different upstream dialect (e.g. fixed()).
		if IsTokenBillingExpression(row.BillingExpr) {
			if _, err := billingexpr.CompileFromCache(row.BillingExpr); err != nil {
				return nil, fmt.Errorf("channel token expression: %w", err)
			}
			snap := newSnapshot(row.BillingExpr, "channel_expression")
			multiplier := row.GroupRatio * ch.RechargeRate
			if !procurement {
				multiplier *= ch.EffectivePriceRatio(modelName)
			}
			snap.AmountMultiplier = &multiplier
			return snap, nil
		}
	}
	expression, ok := GlobalTokenBillingExpression(modelName)
	if !ok {
		return nil, nil
	}
	if _, err := billingexpr.CompileFromCache(expression); err != nil {
		return nil, err
	}
	snap := newSnapshot(expression, "global_expression")
	if row == nil {
		// A procurement amount requires channel price evidence. Never turn the
		// official expression into a guessed procurement price.
		if procurement {
			return nil, nil
		}
		return snap, nil
	}
	i, o, cr, cc, ok := GlobalModelPricingUSDAt(modelName, at)
	if !ok || i <= 0 {
		return nil, fmt.Errorf("tiered base price unavailable for %s", modelName)
	}
	multiplier := ch.RechargeRate
	if !procurement {
		multiplier *= ch.EffectivePriceRatio(modelName)
	}
	inputScale := row.InputPrice * multiplier / i
	scale := func(price, base float64) float64 {
		if base > 0 {
			return price * multiplier / base
		}
		return inputScale
	}
	snap.PriceScale = billingexpr.TokenPriceScale{Enabled: true, Input: inputScale, Output: scale(row.OutputPrice, o), CacheRead: scale(row.CachePrice, cr), CacheWrite: scale(row.CacheCreationPrice, cc)}
	return snap, nil
}

func GlobalTokenBillingExpression(modelName string) (string, bool) {
	for _, name := range ModelNameCandidates(modelName) {
		if billing_setting.GetBillingMode(name) == billing_setting.BillingModeTieredExpr {
			expression, ok := billing_setting.GetBillingExpr(name)
			if ok && strings.TrimSpace(expression) != "" {
				return expression, true
			}
		}
	}
	return "", false
}

// Lex identifiers rather than matching model names. Quoted strings (request
// paths, tier names) must not accidentally turn a media expression into tokens.
func IsTokenBillingExpression(expression string) bool {
	quote := rune(0)
	escaped := false
	word := ""
	token := func(s string) bool {
		switch s {
		case "p", "c", "cr", "cc", "cc1h", "len", "ai", "ao", "img", "img_o":
			return true
		}
		return false
	}
	for _, c := range expression {
		if quote != 0 {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			if token(word) {
				return true
			}
			word = ""
			quote = c
			continue
		}
		if c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			word += string(c)
		} else {
			if token(word) {
				return true
			}
			word = ""
		}
	}
	return token(word)
}
