package tests

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// telegram stands in for api.telegram.org: it records each message (which
// bot sent it, and its text) instead of sending it.
type telegram struct {
	mu   sync.Mutex
	sent []telegramMessage
}

type telegramMessage struct{ bot, text string }

func (tg *telegram) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "api.telegram.org" {
		return http.DefaultTransport.RoundTrip(req)
	}
	body, _ := io.ReadAll(req.Body)
	var m struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(body, &m)
	bot := strings.TrimPrefix(strings.SplitN(strings.TrimPrefix(req.URL.Path, "/"), "/", 2)[0], "bot")
	tg.mu.Lock()
	tg.sent = append(tg.sent, telegramMessage{bot: bot, text: m.Text})
	tg.mu.Unlock()
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Request: req}, nil
}

// listen sends Telegram messages to a new recorder until the test ends.
func listen(t *testing.T) *telegram {
	t.Helper()
	tg := &telegram{}
	before := http.DefaultClient.Transport
	http.DefaultClient.Transport = tg
	t.Cleanup(func() { http.DefaultClient.Transport = before })
	return tg
}

// expect waits for a message whose text has heading, sent since mark, and
// wants every message since mark to be from bot. Alerts are sent in the
// background, so it gives them a moment.
func (tg *telegram) expect(t *testing.T, mark int, bot, heading string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		tg.mu.Lock()
		sent := append([]telegramMessage(nil), tg.sent[mark:]...)
		tg.mu.Unlock()
		found := false
		for _, m := range sent {
			if m.bot != bot {
				t.Fatalf("%q went to bot %q, want %q", m.text, m.bot, bot)
			}
			if strings.Contains(m.text, heading) {
				found = true
			}
		}
		if found {
			return mark + len(sent)
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %q alert reached bot %q; sent: %+v", heading, bot, sent)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Each company's alerts go to its own Telegram chat, never another's.
func TestAlertsGoToTheCompanysOwnChat(t *testing.T) {
	e := setup(t)
	tg := listen(t)
	sides := bothSides(t, e)
	bots := map[uint64]string{}
	for _, s := range sides {
		bots[s.company] = unique("bot")
		body := gin.H{"enabled": true, "bot_token": bots[s.company], "chat_id": "1", "alert_types": []string{
			"low_stock", "shift_opened", "shift_closed", "sale_completed", "void_refund", "expense_added", "po_received"}}
		wantStatus(t, call(t, http.MethodPut, "/settings/notifications", s.token, body), http.StatusOK)
		token := s.token
		t.Cleanup(func() {
			body["enabled"] = false
			call(t, http.MethodPut, "/settings/notifications", token, body)
		})
	}
	type own struct{ branch, customer, product, supplier uint64 }
	rows := map[uint64]own{
		e.companyA: {e.branchTK, e.customer, e.product, e.supplier},
		e.b.id:     {e.b.branch, e.b.customer, e.b.product, e.b.supplier},
	}

	for _, s := range sides {
		t.Run(s.name, func(t *testing.T) {
			o, bot := rows[s.company], bots[s.company]
			// lowStockNext makes the product's next sale of 2 cross its
			// reorder point.
			lowStockNext := func() {
				e.db.Exec("UPDATE branch_stock SET qty = 6, reorder_point = 5 WHERE branch_id = ? AND product_id = ?", o.branch, o.product)
			}
			t.Cleanup(func() {
				e.db.Exec("UPDATE branch_stock SET qty = 1000, reorder_point = 0 WHERE branch_id = ? AND product_id = ?", o.branch, o.product)
			})
			mark := len(tg.sent)

			wantStatus(t, call(t, http.MethodPost, "/expenses", s.token, gin.H{"branch_id": o.branch, "category": "OTHER", "amount_cents": 100, "expense_date": today()}), http.StatusCreated)
			mark = tg.expect(t, mark, bot, "Expense added")

			po := call(t, http.MethodPost, "/purchase-orders", s.token, gin.H{"branch_id": o.branch, "supplier_id": o.supplier,
				"items": []gin.H{{"product_id": o.product, "qty": 1, "unit_cost_cents": 100}}})
			wantStatus(t, po, http.StatusCreated)
			wantStatus(t, call(t, http.MethodPost, "/purchase-orders/"+id(po.id(t))+"/receive", s.token, gin.H{}), http.StatusOK)
			mark = tg.expect(t, mark, bot, "Purchase order received")

			till := newTill(t, o.branch)
			shift := call(t, http.MethodPost, "/shifts/open", s.token, gin.H{"device_key": till, "opening_usd_cents": 0, "opening_khr_riel": 0})
			wantStatus(t, shift, http.StatusCreated)
			mark = tg.expect(t, mark, bot, "Shift opened")

			lowStockNext()
			body := saleBody(till, unique("key"), o.product)
			body["items"] = []gin.H{{"product_id": o.product, "qty": 2}}
			sale := call(t, http.MethodPost, "/sales", s.token, body)
			wantStatus(t, sale, http.StatusCreated)
			tg.expect(t, mark, bot, "Low stock")
			mark = tg.expect(t, mark, bot, "Sale completed")

			lowStockNext()
			edit := call(t, http.MethodPut, "/sales/"+id(sale.id(t)), s.token, gin.H{"items": []gin.H{{"product_id": o.product, "qty": 4}}, "discount_cents": 0})
			wantStatus(t, edit, http.StatusOK)
			mark = tg.expect(t, mark, bot, "Low stock")

			wantStatus(t, call(t, http.MethodPost, "/sales/"+id(sale.id(t))+"/void", s.token, gin.H{"reason": "test"}), http.StatusOK)
			mark = tg.expect(t, mark, bot, "Sale voided")

			lowStockNext()
			invoiceBody := gin.H{"branch_id": o.branch, "customer_id": o.customer, "invoice_date": today(),
				"items": []gin.H{{"product_id": o.product, "qty": 2, "unit_price_cents": 100}}}
			invoice := call(t, http.MethodPost, "/invoices", s.token, invoiceBody)
			wantStatus(t, invoice, http.StatusCreated)
			mark = tg.expect(t, mark, bot, "Low stock")

			lowStockNext()
			invoiceBody["items"] = []gin.H{{"product_id": o.product, "qty": 4, "unit_price_cents": 100}}
			wantStatus(t, call(t, http.MethodPut, "/invoices/"+id(invoice.id(t)), s.token, invoiceBody), http.StatusOK)
			mark = tg.expect(t, mark, bot, "Low stock")

			wantStatus(t, call(t, http.MethodPost, "/shifts/"+id(shift.id(t))+"/close", s.token, gin.H{"counted_usd_cents": 0, "counted_khr_riel": 0}), http.StatusOK)
			tg.expect(t, mark, bot, "Shift closed")
		})
	}
}
