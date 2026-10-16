package handlers

import (
	"testing"

	"com-mart/backend/internal/models"
)

func TestInvoicePaymentStatus(t *testing.T) {
	cases := []struct {
		total, paid int64
		want        models.InvoicePaymentStatus
	}{
		{1000, 0, models.InvoiceDue},
		{1000, 1, models.InvoicePending},
		{1000, 999, models.InvoicePending},
		{1000, 1000, models.InvoicePaid},
		{0, 0, models.InvoicePaid}, // nothing to pay
	}
	for _, c := range cases {
		if got := invoicePaymentStatus(c.total, c.paid); got != c.want {
			t.Errorf("total %d paid %d: got %s, want %s", c.total, c.paid, got, c.want)
		}
	}
}

func TestValidAttachment(t *testing.T) {
	for s, want := range map[string]bool{
		"https://bank.example/receipt/1": true,
		"data:image/jpeg;base64,AAAA":    true,
		"javascript:alert(1)":            false,
		"data:text/html;base64,AAAA":     false,
	} {
		if got := validAttachment(s); got != want {
			t.Errorf("validAttachment(%q) = %v, want %v", s, got, want)
		}
	}
}
