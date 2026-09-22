package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func TestLocalizeRelayClientMessageUsesRequestLanguage(t *testing.T) {
	if err := i18n.Init(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		language string
		want     string
	}{
		{language: i18n.LangEn, want: "Insufficient balance. Remaining balance: $0.000000"},
		{language: i18n.LangZhCN, want: "余额不足，剩余余额：$0.000000"},
		{language: i18n.LangZhTW, want: "餘額不足，剩餘餘額：$0.000000"},
		{language: i18n.LangJa, want: "残高が不足しています。残りの残高: $0.000000"},
	}

	for _, tt := range tests {
		t.Run(tt.language, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(nil)
			ctx.Set(string(constant.ContextKeyLanguage), tt.language)
			err := types.NewErrorWithStatusCode(
				errors.New("internal quota detail"),
				types.ErrorCodeInsufficientUserQuota,
				http.StatusForbidden,
				types.ErrOptionWithClientMessage(i18n.MsgQuotaInsufficientBalance, map[string]any{"Remaining": "$0.000000"}),
			)
			if got := localizeRelayClientMessage(ctx, err); got != tt.want {
				t.Fatalf("localized message = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLocalizeRelayClientMessageLeavesUpstreamErrorUntouched(t *testing.T) {
	if err := i18n.Init(); err != nil {
		t.Fatal(err)
	}
	ctx, _ := gin.CreateTestContext(nil)
	ctx.Set(string(constant.ContextKeyLanguage), i18n.LangEn)
	upstream := types.NewOpenAIError(errors.New("用户额度不足"), types.ErrorCodeInsufficientUserQuota, http.StatusForbidden)

	if got := localizeRelayClientMessage(ctx, upstream); got != "用户额度不足" {
		t.Fatalf("upstream message = %q, want original message", got)
	}

}

func TestRespondTaskErrorLocalizesBillingError(t *testing.T) {
	if err := i18n.Init(); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set(string(constant.ContextKeyLanguage), i18n.LangFr)
	apiErr := types.NewErrorWithStatusCode(
		errors.New("internal quota detail"),
		types.ErrorCodeInsufficientUserQuota,
		http.StatusForbidden,
		types.ErrOptionWithClientMessage(i18n.MsgQuotaInsufficientBalance, map[string]any{"Remaining": "$0.000000"}),
	)

	respondTaskError(ctx, service.TaskErrorFromAPIError(apiErr))

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != string(types.ErrorCodeInsufficientUserQuota) {
		t.Fatalf("code = %q", body.Code)
	}
	if body.Message != "Solde insuffisant. Solde restant : $0.000000" {
		t.Fatalf("message = %q", body.Message)
	}
}
