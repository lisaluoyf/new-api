package setting

import "github.com/QuantumNous/new-api/common"

var (
	NowPaymentsEnabled   = common.GetEnvOrDefaultBool("NOWPAYMENTS_ENABLED", false)
	NowPaymentsAPIKey    = common.GetEnvOrDefaultString("NOWPAYMENTS_API_KEY", "")
	NowPaymentsIPNSecret = common.GetEnvOrDefaultString("NOWPAYMENTS_IPN_SECRET", "")
	NowPaymentsMinTopUp  = common.GetEnvOrDefault("NOWPAYMENTS_MIN_TOPUP", 1)
)
