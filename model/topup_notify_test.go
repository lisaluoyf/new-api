package model

import "testing"

func TestFormatPaymentMethodLabel(t *testing.T) {
	tests := []struct {
		method string
		want   string
	}{
		{PaymentMethodPayPal, "PayPal"},
		{PaymentMethodStripe, "Stripe"},
		{"alipay", "支付宝"},
		{"wxpay", "微信支付"},
		{PaymentMethodCreem, "Creem"},
		{"epay", "易支付"},
		{"", "未知"},
		{"custom_gateway", "custom_gateway"},
	}
	for _, tt := range tests {
		if got := FormatPaymentMethodLabel(tt.method); got != tt.want {
			t.Errorf("FormatPaymentMethodLabel(%q) = %q, want %q", tt.method, got, tt.want)
		}
	}
}

func TestFormatTopupPaidAmount(t *testing.T) {
	tests := []struct {
		name   string
		money  float64
		method string
		want   string
	}{
		{name: "alipay CNY", money: 52.5, method: "alipay", want: "¥52.50"},
		{name: "wechat CNY", money: 70, method: "wxpay", want: "¥70.00"},
		{name: "custom epay CNY", money: 35, method: "custom1", want: "¥35.00"},
		{name: "platega RUB", money: 900, method: PaymentMethodPlatega, want: "₽900.00"},
		{name: "clink USD", money: 10, method: PaymentMethodClink, want: "$10.00"},
		{name: "paypal USD", money: 9.5, method: PaymentMethodPayPal, want: "$9.50"},
		{name: "crypto USD value", money: 12.345, method: "crypto", want: "$12.35"},
		{name: "missing amount", money: 0, method: "alipay", want: "—"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatTopupPaidAmount(tt.money, tt.method); got != tt.want {
				t.Fatalf("FormatTopupPaidAmount(%v, %q) = %q, want %q", tt.money, tt.method, got, tt.want)
			}
		})
	}
}
