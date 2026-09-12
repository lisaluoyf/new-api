package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

// UpstreamFalseSuccessSetting 控制「HTTP 200 假成功」识别与 fallback。
// 关闭后，HTTP 200 响应不再被改写为可重试错误，也不再产生假成功诊断日志与通知。
type UpstreamFalseSuccessSetting struct {
	Enabled bool `json:"enabled"`
}

var upstreamFalseSuccessSetting = UpstreamFalseSuccessSetting{
	Enabled: true,
}

func init() {
	config.GlobalConfig.Register("upstream_false_success_setting", &upstreamFalseSuccessSetting)
}

func GetUpstreamFalseSuccessSetting() *UpstreamFalseSuccessSetting {
	return &upstreamFalseSuccessSetting
}

// IsUpstreamFalseSuccessEnabled 是否启用 HTTP 200 假成功识别与 fallback。
func IsUpstreamFalseSuccessEnabled() bool {
	return upstreamFalseSuccessSetting.Enabled
}

// SetUpstreamFalseSuccessEnabled 供测试与内部开关使用。
func SetUpstreamFalseSuccessEnabled(enabled bool) {
	upstreamFalseSuccessSetting.Enabled = enabled
}
