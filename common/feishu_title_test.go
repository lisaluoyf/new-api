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
