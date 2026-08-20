package controller

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestIsTelegramGroupMember(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		isMember bool
		want     bool
	}{
		{name: "member", status: "member", want: true},
		{name: "administrator", status: "administrator", want: true},
		{name: "creator", status: "creator", want: true},
		{name: "restricted member", status: "restricted", isMember: true, want: true},
		{name: "restricted non member", status: "restricted", want: false},
		{name: "left", status: "left", want: false},
		{name: "kicked", status: "kicked", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTelegramGroupMember(tt.status, tt.isMember); got != tt.want {
				t.Fatalf("isTelegramGroupMember(%q, %t) = %t, want %t", tt.status, tt.isMember, got, tt.want)
			}
		})
	}
}

func TestCheckTelegramAuthorizationIgnoresResponseControls(t *testing.T) {
	token := "test-token"
	dataCheckString := "auth_date=1787198400\nfirst_name=Lisa\nid=12345"
	secret := sha256.Sum256([]byte(token))
	signature := hmac.New(sha256.New, secret[:])
	_, _ = signature.Write([]byte(dataCheckString))
	hash := hex.EncodeToString(signature.Sum(nil))

	params := map[string][]string{
		"auth_date":  {"1787198400"},
		"first_name": {"Lisa"},
		"format":     {"json"},
		"hash":       {hash},
		"id":         {"12345"},
		"redirect":   {"/_panel/profile"},
	}
	if !checkTelegramAuthorization(params, token) {
		t.Fatal("expected valid Telegram signature with response-only controls")
	}
}

func TestCheckTelegramAuthorizationRejectsMissingValues(t *testing.T) {
	if checkTelegramAuthorization(map[string][]string{"hash": {}}, "token") {
		t.Fatal("expected malformed Telegram parameters to be rejected")
	}
}
