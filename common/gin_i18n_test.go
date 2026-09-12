package common

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestLocalizeLegacyClientMessageDoesNotTreatJapaneseAsChinese(t *testing.T) {
	originalTranslateMessage := TranslateMessage
	t.Cleanup(func() { TranslateMessage = originalTranslateMessage })
	context, _ := gin.CreateTestContext(nil)

	TranslateMessage = func(_ *gin.Context, _ string, _ ...map[string]any) string {
		return "操作に失敗しました"
	}
	if got := localizeLegacyClientMessage(context, "保存失败"); got != "操作に失敗しました" {
		t.Fatalf("Japanese fallback = %q", got)
	}

	TranslateMessage = func(_ *gin.Context, _ string, _ ...map[string]any) string {
		return "操作失败"
	}
	if got := localizeLegacyClientMessage(context, "保存失败"); got != "保存失败" {
		t.Fatalf("Chinese fallback = %q", got)
	}
}
