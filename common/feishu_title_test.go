package common

import "testing"

func TestFeishuNotificationTitlePrefixesNodeName(t *testing.T) {
	original := NodeName
	NodeName = "apimaster-new-api"
	t.Cleanup(func() { NodeName = original })

	if got := FeishuNotificationTitle("HTTP 200 假成功拦截"); got != "[apimaster] HTTP 200 假成功拦截" {
		t.Fatalf("unexpected title: %q", got)
	}
}

func TestFeishuNotificationTitleUsesUnknownNodeFallback(t *testing.T) {
	original := NodeName
	NodeName = ""
	t.Setenv("NODE_NAME", "")
	t.Cleanup(func() { NodeName = original })

	if got := FeishuNotificationTitle("alert"); got != "[unknown-node] alert" {
		t.Fatalf("unexpected title: %q", got)
	}
}

func TestFeishuFalseSuccessChatIDIsDedicatedWithLegacyFallback(t *testing.T) {
	t.Setenv("FEISHU_NEWAPI_LOG_CHAT_ID", "oc_legacy")
	t.Setenv("FEISHU_FALSE_SUCCESS_CHAT_ID", "oc_false_success")
	if got := FeishuFalseSuccessChatID(); got != "oc_false_success" {
		t.Fatalf("unexpected dedicated chat id: %q", got)
	}
	t.Setenv("FEISHU_FALSE_SUCCESS_CHAT_ID", "")
	if got := FeishuFalseSuccessChatID(); got != "oc_legacy" {
		t.Fatalf("unexpected legacy fallback chat id: %q", got)
	}
}
