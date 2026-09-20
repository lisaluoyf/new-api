package service

import (
	"bytes"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestTopupInvoiceRenderCustomerProfiles(t *testing.T) {
	for _, name := range []string{"", "张三", "Мария Иванова", strings.Repeat("Long customer name ", 6), "Customer\n\x00😀"} {
		t.Run(name, func(t *testing.T) {
			content, err := RenderTopupInvoice(&model.TopupInvoice{
				ID: 123, UserID: 456, TradeNo: "SAMPLE-ORDER-123", PaidAt: 1789905600,
				CustomerName: name, CustomerEmail: "customer@example.com",
				Description: "API wallet credit", PaymentMethod: "stripe", AmountPaid: "USD 100.00", HasRefund: true,
			})
			require.NoError(t, err)
			require.True(t, bytes.HasPrefix(content, []byte("%PDF-")))
		})
	}
}
