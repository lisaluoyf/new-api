package service

import (
	"math"
	"strconv"

	"github.com/shopspring/decimal"
)

// PaymentChargeAmount mirrors each gateway's checkout representation. It is
// also used for legacy quotes; never widen the tolerance to accept underpayment.
func PaymentChargeAmount(provider string, quote float64) float64 {
	if quote <= 0 || math.IsNaN(quote) || math.IsInf(quote, 0) {
		return math.NaN()
	}
	switch provider {
	case "paypal":
		amount, _ := strconv.ParseFloat(FormatPayPalAmount(quote), 64)
		return amount
	case "stripe":
		return math.Round(quote*100) / 100
	case "waffo_pancake", "clink":
		return decimal.NewFromFloat(quote).Round(2).InexactFloat64()
	default:
		return quote
	}
}

func PaymentChargeMatches(provider string, quote, paid float64) bool {
	if paid <= 0 || math.IsNaN(paid) || math.IsInf(paid, 0) {
		return false
	}
	return math.Abs(PaymentChargeAmount(provider, quote)-paid) <= 0.000001
}
