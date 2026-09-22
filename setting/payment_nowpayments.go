package setting

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

func getNowPaymentsPaymentShortfallPercent() float64 {
	value := strings.TrimSpace(common.GetEnvOrDefaultString("NOWPAYMENTS_PAYMENT_SHORTFALL_PERCENT", "3"))
	percent, err := strconv.ParseFloat(value, 64)
	if err != nil || percent < 0 || percent > 100 {
		return 3
	}
	return percent
}

var (
	NowPaymentsEnabled                 = common.GetEnvOrDefaultBool("NOWPAYMENTS_ENABLED", false)
	NowPaymentsAPIKey                  = common.GetEnvOrDefaultString("NOWPAYMENTS_API_KEY", "")
	NowPaymentsIPNSecret               = common.GetEnvOrDefaultString("NOWPAYMENTS_IPN_SECRET", "")
	NowPaymentsMinTopUp                = common.GetEnvOrDefault("NOWPAYMENTS_MIN_TOPUP", 1)
	NowPaymentsPaymentShortfallPercent = getNowPaymentsPaymentShortfallPercent()
)
