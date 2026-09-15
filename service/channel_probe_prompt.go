package service

import (
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

// ChannelCodeReviewProbe applies only to channel 82, whose upstream returns a
// fixed plaintext greeting for tiny probes. Keep the task used in live validation.
func ChannelCodeReviewProbe(channel *model.Channel) (string, uint, bool) {
	if channel == nil || channel.Id != 82 || channel.Type != constant.ChannelTypeOpenAI {
		return "", 0, false
	}
	return "请帮我审查下面这个 Python 函数是否正确处理空列表，并给出一个修复版本和两个简单测试用例。请用中文简洁解释原因，代码保持可运行。不要调用外部工具。\n\ndef average(values):\n    return sum(values) / len(values)\n\n业务要求：输入为空时返回 None；输入非空时返回算术平均值；不要修改调用者传入的列表。请检查 average([]) 和 average([2, 4, 6]) 的预期结果。", 1024, true
}
