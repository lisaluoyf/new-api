package service

import (
	"bytes"
	_ "embed"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/QuantumNous/new-api/model"
	"github.com/go-pdf/fpdf"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
)

// WenQuanYi provides CJK glyphs for customer-provided names.
//
//go:embed invoicefonts/wqy-zenhei.ttf
var invoiceCustomerFont []byte

func RenderTopupInvoice(invoice *model.TopupInvoice) ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(14, 14, 14)
	pdf.SetAutoPageBreak(true, 20)
	pdf.AddUTF8FontFromBytes("Go", "", goregular.TTF)
	pdf.AddUTF8FontFromBytes("Go", "B", gobold.TTF)
	pdf.AddUTF8FontFromBytes("Customer", "", invoiceCustomerFont)
	pdf.SetTitle(fmt.Sprintf("Invoice APIM-%d", invoice.ID), false)
	pdf.SetAuthor("APIMaster", false)
	pdf.AliasNbPages("")
	pdf.SetFooterFunc(func() {
		pdf.SetDrawColor(230, 230, 230)
		pdf.Line(14, 279, 196, 279)
		pdf.SetXY(14, 282)
		pdf.SetFont("Go", "", 8)
		pdf.SetTextColor(100, 100, 100)
		pdf.CellFormat(182, 5, fmt.Sprintf("Page %d of {nb}", pdf.PageNo()), "", 0, "R", false, 0, "")
	})
	pdf.AddPage()
	pdf.SetTextColor(22, 22, 22)
	pdf.SetFillColor(22, 22, 22)
	pdf.Rect(0, 0, 210, 1.5, "F")
	pdf.SetXY(14, 14)
	pdf.SetFont("Go", "B", 22)
	pdf.CellFormat(90, 10, "Invoice", "", 0, "L", false, 0, "")
	pdf.SetFont("Go", "B", 18)
	pdf.CellFormat(92, 10, "APIMaster", "", 1, "R", false, 0, "")

	paidDate := "Not recorded"
	if invoice.PaidAt > 0 {
		paidDate = time.Unix(invoice.PaidAt, 0).UTC().Format("January 2, 2006")
	}
	pdf.SetY(32)
	for _, row := range [][2]string{
		{"Invoice number", fmt.Sprintf("APIM-%d", invoice.ID)},
		{"Payment date", paidDate + " (UTC)"},
	} {
		pdf.SetFont("Go", "B", 9)
		pdf.CellFormat(33, 5, row[0], "", 0, "L", false, 0, "")
		pdf.SetFont("Go", "", 9)
		pdf.CellFormat(149, 5, row[1], "", 1, "L", false, 0, "")
	}

	pdf.SetXY(14, 51)
	pdf.SetFont("Go", "B", 10)
	pdf.CellFormat(24, 6, "APIMaster", "", 0, "L", false, 0, "")
	pdf.SetFont("Go", "", 9)
	pdf.SetTextColor(100, 100, 100)
	pdf.CellFormat(55, 6, "@apimaster", "", 1, "L", false, 0, "")
	pdf.SetTextColor(22, 22, 22)
	pdf.SetX(14)
	pdf.MultiCell(88, 5, "213 Market Street, Suite 100\nSan Francisco, California 94105\nUnited States\nsupport@apimaster.ai", "", "L", false)
	merchantBottom := pdf.GetY()
	pdf.SetXY(112, 51)
	pdf.SetFont("Go", "B", 10)
	pdf.CellFormat(84, 6, "Bill to", "", 1, "L", false, 0, "")
	for _, value := range []string{invoice.CustomerName, invoice.CustomerEmail} {
		value = invoiceProfileText(value)
		if value == "" {
			continue
		}
		pdf.SetX(112)
		pdf.SetFont("Customer", "", 10)
		pdf.MultiCell(84, 5, value, "", "L", false)
	}
	pdf.SetX(112)
	pdf.SetFont("Go", "", 9)
	pdf.CellFormat(84, 5, fmt.Sprintf("Customer account #%d", invoice.UserID), "", 1, "L", false, 0, "")
	if pdf.GetY() < merchantBottom {
		pdf.SetY(merchantBottom)
	}
	pdf.Ln(10)
	pdf.SetFont("Go", "B", 16)
	pdf.MultiCell(182, 8, invoice.AmountPaid+" paid", "", "L", false)
	pdf.SetFont("Go", "", 9)
	pdf.SetTextColor(90, 90, 90)
	pdf.MultiCell(182, 5, "PAID  |  Payment method: "+invoiceProfileText(invoice.PaymentMethod), "", "L", false)
	pdf.MultiCell(182, 5, "Order number: "+invoiceProfileText(invoice.TradeNo), "", "L", false)
	pdf.Ln(12)

	pdf.SetTextColor(22, 22, 22)
	pdf.SetFont("Go", "", 9)
	pdf.CellFormat(104, 7, "Description", "", 0, "L", false, 0, "")
	pdf.CellFormat(14, 7, "Qty", "", 0, "R", false, 0, "")
	pdf.CellFormat(32, 7, "Unit price", "", 0, "R", false, 0, "")
	pdf.CellFormat(32, 7, "Amount", "", 1, "R", false, 0, "")
	pdf.SetDrawColor(140, 140, 140)
	pdf.Line(14, pdf.GetY(), 196, pdf.GetY())
	rowY := pdf.GetY() + 3
	pdf.SetY(rowY)
	pdf.MultiCell(104, 6, invoice.Description, "", "L", false)
	rowBottom := pdf.GetY() + 5
	pdf.SetXY(118, rowY)
	pdf.CellFormat(14, 6, "1", "", 0, "R", false, 0, "")
	pdf.CellFormat(32, 6, invoice.AmountPaid, "", 0, "R", false, 0, "")
	pdf.CellFormat(32, 6, invoice.AmountPaid, "", 1, "R", false, 0, "")
	pdf.SetDrawColor(230, 230, 230)
	pdf.Line(14, rowBottom, 196, rowBottom)
	pdf.SetY(rowBottom + 5)
	for _, label := range []string{"Subtotal", "Total", "Amount paid"} {
		style := ""
		if label == "Amount paid" {
			style = "B"
		}
		pdf.SetFont("Go", style, 10)
		pdf.SetX(112)
		pdf.CellFormat(42, 8, label, "", 0, "L", false, 0, "")
		pdf.CellFormat(42, 8, invoice.AmountPaid, "", 1, "R", false, 0, "")
		pdf.Line(112, pdf.GetY(), 196, pdf.GetY())
	}
	pdf.Ln(10)
	pdf.SetFont("Go", "", 9)
	pdf.SetTextColor(90, 90, 90)
	if invoice.HasRefund {
		pdf.MultiCell(182, 5, "Refunds have been recorded for this order. The amounts above show the original payment before refunds.", "", "L", false)
		pdf.Ln(3)
	}
	pdf.MultiCell(182, 5, "Thank you for using APIMaster.", "", "L", false)
	var output bytes.Buffer
	if err := pdf.Output(&output); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// Core invoice fonts support BMP text. Remove control characters and emoji
// rather than allowing malformed user input to break PDF generation.
func invoiceProfileText(value string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r > 0xFFFF {
			return ' '
		}
		return r
	}, value)), " ")
}
