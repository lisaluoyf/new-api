package billingexpr

// quotaConversion converts raw expression output to quota based on the
// expression version. This is the central dispatch point for future versions
// that may use a different conversion formula.
func quotaConversion(exprOutput float64, snap *BillingSnapshot) float64 {
	switch snap.ExprVersion {
	default: // v1: coefficients are $/1M tokens prices
		return exprOutput / 1_000_000 * snap.QuotaPerUnit
	}
}

// ComputeTieredQuota runs the Expr from a frozen BillingSnapshot against
// actual token counts and returns the settlement result.
func ComputeTieredQuota(snap *BillingSnapshot, params TokenParams) (TieredResult, error) {
	return ComputeTieredQuotaWithRequest(snap, params, RequestInput{})
}

func ComputeTieredQuotaWithRequest(snap *BillingSnapshot, params TokenParams, request RequestInput) (TieredResult, error) {
	params = ApplyTokenPriceScale(params, snap.PriceScale)
	cost, trace, err := RunExprByHashWithRequest(snap.ExprString, snap.ExprHash, params, request)
	if err != nil {
		return TieredResult{}, err
	}

	quotaBeforeGroup := quotaConversion(cost, snap)
	afterGroup := QuotaRound(quotaBeforeGroup * snap.GroupRatio)
	crossed := trace.MatchedTier != snap.EstimatedTier

	return TieredResult{
		ActualQuotaBeforeGroup: quotaBeforeGroup,
		ActualQuotaAfterGroup:  afterGroup,
		MatchedTier:            trace.MatchedTier,
		CrossedTier:            crossed,
	}, nil
}

func ApplyTokenPriceScale(params TokenParams, scale TokenPriceScale) TokenParams {
	if !scale.Enabled {
		return params
	}
	inputScale := scale.Input
	outputScale := scale.Output
	params.P *= inputScale
	params.Img *= inputScale
	params.AI *= inputScale
	params.C *= outputScale
	params.ImgO *= outputScale
	params.AO *= outputScale
	params.CR *= scale.CacheRead
	cacheWriteScale := scale.CacheWrite
	params.CC *= cacheWriteScale
	params.CC1h *= cacheWriteScale
	return params
}
