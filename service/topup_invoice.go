package service

import (
	"bytes"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/go-pdf/fpdf"
	"golang.org/x/image/font/gofont/goregular"
)

func RenderTopupInvoice(invoice *model.TopupInvoice) ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(22, 22, 22)
	pdf.SetAutoPageBreak(true, 22)
	pdf.AddUTF8FontFromBytes("Go", "", goregular.TTF)
	pdf.SetTitle(fmt.Sprintf("Invoice APIM-%d", invoice.ID), false)
	pdf.SetAuthor("APIMaster", false)
	pdf.AddPage()
	pdf.SetTextColor(24, 36, 56)
	pdf.SetFont("Go", "", 26)
	pdf.CellFormat(0, 14, "APIMaster", "", 1, "L", false, 0, "")
	pdf.SetFont("Go", "", 18)
	pdf.CellFormat(0, 14, "INVOICE", "", 1, "L", false, 0, "")
	pdf.SetFont("Go", "", 11)
	line := func(label, value string) {
		pdf.SetTextColor(90, 100, 115)
		pdf.CellFormat(0, 7, label, "", 1, "L", false, 0, "")
		pdf.SetTextColor(24, 36, 56)
		pdf.MultiCell(0, 7, value, "", "L", false)
		pdf.Ln(3)
	}
	line("Invoice number", fmt.Sprintf("APIM-%d", invoice.ID))
	line("Order number", invoice.TradeNo)
	line("Billed to", fmt.Sprintf("Customer account #%d", invoice.UserID))
	paidAt := "Not recorded"
	if invoice.PaidAt > 0 {
		paidAt = time.Unix(invoice.PaidAt, 0).UTC().Format("2006-01-02 15:04:05 UTC")
	}
	line("Payment date", paidAt)
	line("Payment method", invoice.PaymentMethod)
	pdf.Ln(3)
	pdf.SetDrawColor(220, 225, 232)
	pdf.Line(22, pdf.GetY(), 188, pdf.GetY())
	pdf.Ln(6)
	line("Description", invoice.Description)
	pdf.SetFont("Go", "", 16)
	line("Total paid", invoice.AmountPaid)
	pdf.SetFont("Go", "", 11)
	line("Payment status", "PAID")
	if invoice.HasRefund {
		pdf.MultiCell(0, 6, "Refunds have been recorded for this order. The total above is the original payment before refunds.", "", "L", false)
	}
	pdf.Ln(8)
	pdf.SetTextColor(90, 100, 115)
	pdf.SetFont("Go", "", 10)
	pdf.MultiCell(0, 6, "Thank you for using APIMaster.", "", "L", false)
	var output bytes.Buffer
	if err := pdf.Output(&output); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
