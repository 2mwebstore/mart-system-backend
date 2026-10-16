package handlers

// QR table ordering. Staff set up tables per branch (Settings > Tables & QR)
// and print a QR per table; the QR holds only the table's random token. A
// customer who scans it gets that branch's menu with no sign-in, sends an
// order, and follows its status on their phone. The order appears on the POS
// for staff to accept, then to load into the cart and take payment —
// CreateSale with table_order_id marks it PAID in the same transaction.
// See docs/DECISIONS.md.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"com-mart/backend/internal/middleware"
	"com-mart/backend/internal/models"
	"com-mart/backend/internal/services"
	"com-mart/backend/internal/utils"
)

func randomToken(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// ---- Tables (staff) ---------------------------------------------------------

type diningTableDTO struct {
	ID         uint64 `json:"id"`
	BranchID   uint64 `json:"branch_id"`
	Name       string `json:"name"`
	Token      string `json:"token"`
	Active     bool   `json:"active"`
	OpenOrders int64  `json:"open_orders"`
}

// ListDiningTables lists a branch's tables in natural order (T2 before T10).
func (a *API) ListDiningTables(c *gin.Context) {
	branchID := branchParam(c)
	rows := []diningTableDTO{}
	err := a.DB.Table("dining_tables t").
		Select(`t.id, t.branch_id, t.name, t.token, t.active,
			(SELECT COUNT(*) FROM table_orders o WHERE o.table_id = t.id AND o.status IN ('NEW','ACCEPTED')) AS open_orders`).
		Where("t.branch_id = ?", branchID).
		Order("LENGTH(t.name), t.name").Scan(&rows).Error
	if err != nil {
		dbFail(c, err)
		return
	}
	utils.OK(c, http.StatusOK, rows)
}

type tableBoardDTO struct {
	ID     uint64 `json:"id"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

// ListTableBoard is the POS's list of a branch's tables for the Tables board:
// names only, no QR tokens (those stay with branch.manage). The board joins
// this with the open orders the POS already polls.
func (a *API) ListTableBoard(c *gin.Context) {
	branchID := branchParam(c)
	rows := []tableBoardDTO{}
	if err := a.DB.Model(&models.DiningTable{}).Select("id, name, active").
		Where("branch_id = ?", branchID).Order("LENGTH(name), name").Scan(&rows).Error; err != nil {
		dbFail(c, err)
		return
	}
	utils.OK(c, http.StatusOK, rows)
}

type diningTableReq struct {
	BranchID uint64 `json:"branch_id" binding:"required"`
	Name     string `json:"name" binding:"required,max=40"`
	Active   bool   `json:"active"`
	// A new token makes every printed QR for this table stop working.
	RotateToken bool `json:"rotate_token"`
}

func (a *API) tableNameTaken(branchID uint64, name string, exceptID uint64) bool {
	var n int64
	a.DB.Model(&models.DiningTable{}).Where("branch_id = ? AND name = ? AND id <> ?", branchID, name, exceptID).Count(&n)
	return n > 0
}

func (a *API) CreateDiningTable(c *gin.Context) {
	var req diningTableReq
	if !bind(c, &req) {
		return
	}
	if !canAccessBranch(c, req.BranchID) {
		fail(c, utils.ErrForbidden)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		invalid(c, "name", "Table name is required.")
		return
	}
	if a.tableNameTaken(req.BranchID, name, 0) {
		invalid(c, "name", "That table name is already used in this branch.")
		return
	}
	token, err := randomToken(12)
	if err != nil {
		dbFail(c, err)
		return
	}
	t := models.DiningTable{BranchID: req.BranchID, Name: name, Token: token, Active: req.Active}
	if err := a.DB.Create(&t).Error; err != nil {
		dbFail(c, err)
		return
	}
	utils.OK(c, http.StatusCreated, diningTableDTO{ID: t.ID, BranchID: t.BranchID, Name: t.Name, Token: t.Token, Active: t.Active})
}

type diningTableBulkReq struct {
	BranchID uint64 `json:"branch_id" binding:"required"`
	Prefix   string `json:"prefix" binding:"max=30"`
	From     int    `json:"from" binding:"min=1"`
	To       int    `json:"to" binding:"min=1"`
}

// CreateDiningTablesBulk adds "<prefix><n>" for n in from..to (at most 100),
// skipping names the branch already has. Returns how many were created.
func (a *API) CreateDiningTablesBulk(c *gin.Context) {
	var req diningTableBulkReq
	if !bind(c, &req) {
		return
	}
	if !canAccessBranch(c, req.BranchID) {
		fail(c, utils.ErrForbidden)
		return
	}
	if req.To < req.From || req.To-req.From >= 100 {
		invalid(c, "to", "Pick a range of 1 to 100 tables.")
		return
	}
	created := 0
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		for n := req.From; n <= req.To; n++ {
			name := fmt.Sprintf("%s%d", strings.TrimSpace(req.Prefix), n)
			var exists int64
			tx.Model(&models.DiningTable{}).Where("branch_id = ? AND name = ?", req.BranchID, name).Count(&exists)
			if exists > 0 {
				continue
			}
			token, err := randomToken(12)
			if err != nil {
				return err
			}
			if err := tx.Create(&models.DiningTable{BranchID: req.BranchID, Name: name, Token: token, Active: true}).Error; err != nil {
				return err
			}
			created++
		}
		return nil
	})
	if err != nil {
		dbFail(c, err)
		return
	}
	utils.OK(c, http.StatusCreated, gin.H{"created": created})
}

func (a *API) UpdateDiningTable(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req diningTableReq
	if !bind(c, &req) {
		return
	}
	var t models.DiningTable
	if err := a.DB.First(&t, id).Error; err != nil {
		dbFail(c, err)
		return
	}
	if !canAccessBranch(c, t.BranchID) {
		fail(c, utils.ErrForbidden)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		invalid(c, "name", "Table name is required.")
		return
	}
	if a.tableNameTaken(t.BranchID, name, t.ID) {
		invalid(c, "name", "That table name is already used in this branch.")
		return
	}
	updates := map[string]interface{}{"name": name, "active": req.Active}
	if req.RotateToken {
		token, err := randomToken(12)
		if err != nil {
			dbFail(c, err)
			return
		}
		updates["token"] = token
	}
	if err := a.DB.Model(&t).Updates(updates).Error; err != nil {
		dbFail(c, err)
		return
	}
	a.DB.First(&t, id)
	utils.OK(c, http.StatusOK, diningTableDTO{ID: t.ID, BranchID: t.BranchID, Name: t.Name, Token: t.Token, Active: t.Active})
}

// DeleteDiningTable removes a table; its past orders keep their table name
// (table_id is set to NULL by the foreign key).
func (a *API) DeleteDiningTable(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var t models.DiningTable
	if err := a.DB.First(&t, id).Error; err != nil {
		dbFail(c, err)
		return
	}
	if !canAccessBranch(c, t.BranchID) {
		fail(c, utils.ErrForbidden)
		return
	}
	if err := a.DB.Delete(&t).Error; err != nil {
		dbFail(c, err)
		return
	}
	utils.OK(c, http.StatusOK, gin.H{"deleted": true})
}

// ---- Public menu & ordering (no sign-in) -------------------------------------

type menuItemDTO struct {
	ID              uint64  `json:"id"`
	ParentProductID *uint64 `json:"parent_product_id"`
	VariantName     string  `json:"variant_name"`
	VariantCount    int64   `json:"variant_count"`
	NameEn          string  `json:"name_en"`
	NameKm          string  `json:"name_km"`
	CategoryID      *uint64 `json:"category_id"`
	CategoryName    *string `json:"category_name"`
	CategoryNameKm  *string `json:"category_name_km"`
	ImageURL        string  `json:"image_url"`
	Unit            string  `json:"unit"`
	PriceCents      int64   `json:"price_cents"`
	Available       bool    `json:"available"`
}

// activeTableByToken finds an active table (in an active branch) by its QR token.
func (a *API) activeTableByToken(token string) (*models.DiningTable, *models.Branch, error) {
	var t models.DiningTable
	if err := a.DB.Where("token = ? AND active = 1", token).First(&t).Error; err != nil {
		return nil, nil, err
	}
	var b models.Branch
	if err := a.DB.Where("id = ? AND active = 1", t.BranchID).First(&b).Error; err != nil {
		return nil, nil, err
	}
	return &t, &b, nil
}

func tableNotFound(c *gin.Context) {
	fail(c, utils.NewAppError(http.StatusNotFound, "TABLE_NOT_FOUND", "This table's QR code isn't active. Please ask a staff member."))
}

// menuItems is the branch's orderable catalog for customers: active products
// with their branch price and whether they can be ordered now. Cost and stock
// counts are deliberately not exposed.
func (a *API) menuItems(branchID uint64) ([]menuItemDTO, error) {
	var rows []productDTO
	if err := a.productQuery(branchID).Where("p.active = 1").
		Order("COALESCE(p.parent_product_id, p.id), (p.parent_product_id IS NOT NULL), p.id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	allowOOS := a.loadSettings().AllowOutOfStockSale
	out := make([]menuItemDTO, 0, len(rows))
	for _, p := range rows {
		available := p.ProductType == string(models.ProductService) || allowOOS || p.Qty > 0
		if !available && p.HideWhenOutOfStock {
			continue
		}
		out = append(out, menuItemDTO{
			ID: p.ID, ParentProductID: p.ParentProductID, VariantName: p.VariantName, VariantCount: p.VariantCount,
			NameEn: p.NameEn, NameKm: p.NameKm, CategoryID: p.CategoryID, CategoryName: p.CategoryName, CategoryNameKm: p.CategoryNameKm,
			ImageURL: p.ImageURL, Unit: p.Unit, PriceCents: p.PriceCents, Available: available,
		})
	}
	return out, nil
}

// PublicTableMenu backs the page a table's QR opens.
func (a *API) PublicTableMenu(c *gin.Context) {
	table, branch, err := a.activeTableByToken(c.Param("token"))
	if err != nil {
		tableNotFound(c)
		return
	}
	items, err := a.menuItems(branch.ID)
	if err != nil {
		dbFail(c, err)
		return
	}
	utils.OK(c, http.StatusOK, gin.H{
		"table":         gin.H{"name": table.Name},
		"branch":        gin.H{"name": branch.Name},
		"store_name":    a.loadSettings().ReceiptHeader,
		"exchange_rate": a.currentRate(), // riel per USD, for showing totals in both
		"items":         items,
	})
}

type publicOrderItemReq struct {
	ProductID uint64 `json:"product_id" binding:"required"`
	Qty       int64  `json:"qty" binding:"required,min=1,max=99"`
	Note      string `json:"note" binding:"max=120"`
}

type publicOrderReq struct {
	Items []publicOrderItemReq `json:"items" binding:"required,min=1,max=40,dive"`
	Note  string               `json:"note" binding:"max=255"`
}

type publicOrderItemDTO struct {
	NameEn         string `json:"name_en"`
	NameKm         string `json:"name_km"`
	Qty            int64  `json:"qty"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	Note           string `json:"note"`
}

// publicOrderDTO is what the customer's phone sees: no internal ids.
type publicOrderDTO struct {
	Token        string               `json:"token"`
	TableName    string               `json:"table_name"`
	Status       string               `json:"status"`
	Note         string               `json:"note"`
	TotalCents   int64                `json:"total_cents"`
	CancelReason string               `json:"cancel_reason"`
	CreatedAt    time.Time            `json:"created_at"`
	Items        []publicOrderItemDTO `json:"items"`
}

func (a *API) publicOrder(o models.TableOrder) (publicOrderDTO, error) {
	items := []publicOrderItemDTO{}
	err := a.DB.Model(&models.TableOrderItem{}).Select("name_en, name_km, qty, unit_price_cents, note").
		Where("table_order_id = ?", o.ID).Order("id").Scan(&items).Error
	return publicOrderDTO{
		Token: o.PublicToken, TableName: o.TableLabel, Status: string(o.Status), Note: o.Note, TotalCents: o.TotalCents,
		CancelReason: o.CancelReason, CreatedAt: o.CreatedAt, Items: items,
	}, err
}

// orderLimiter caps how fast one phone can send orders to one table — the QR
// is public, so this stops a stuck button or a prank from flooding the POS.
type orderLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

var publicOrderLimiter = &orderLimiter{hits: map[string][]time.Time{}}

const (
	publicOrderWindow = 5 * time.Minute
	publicOrderMax    = 5 // per table + client IP per window
	tableOpenOrderMax = 15
)

func (l *orderLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < publicOrderWindow {
			kept = append(kept, t)
		}
	}
	if len(kept) >= publicOrderMax {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	// Drop idle keys now and then so the map can't grow without bound.
	if len(l.hits) > 5000 {
		for k, v := range l.hits {
			if len(v) == 0 || now.Sub(v[len(v)-1]) >= publicOrderWindow {
				delete(l.hits, k)
			}
		}
	}
	return true
}

// PlacePublicTableOrder: the customer sends products, quantities and notes;
// names and prices come from the catalog, never from the request.
func (a *API) PlacePublicTableOrder(c *gin.Context) {
	token := c.Param("token")
	table, branch, err := a.activeTableByToken(token)
	if err != nil {
		tableNotFound(c)
		return
	}
	var req publicOrderReq
	if !bind(c, &req) {
		return
	}
	var open int64
	a.DB.Model(&models.TableOrder{}).Where("table_id = ? AND status = ?", table.ID, models.TableOrderNew).Count(&open)
	if open >= tableOpenOrderMax {
		fail(c, utils.NewAppError(http.StatusTooManyRequests, "TABLE_BUSY", "This table already has orders waiting. Please wait for staff."))
		return
	}
	if !publicOrderLimiter.allow(token+"|"+c.ClientIP(), time.Now()) {
		fail(c, utils.NewAppError(http.StatusTooManyRequests, "TOO_MANY_ORDERS", "Too many orders from this phone. Please wait a few minutes or ask staff."))
		return
	}

	// Same product twice (e.g. added again with another note) becomes one
	// line, the way the POS cart and the sale hold it; notes are joined.
	type line struct {
		qty   int64
		notes []string
	}
	merged := map[uint64]*line{}
	order := []uint64{}
	for _, it := range req.Items {
		l, ok := merged[it.ProductID]
		if !ok {
			l = &line{}
			merged[it.ProductID] = l
			order = append(order, it.ProductID)
		}
		l.qty += it.Qty
		if n := strings.TrimSpace(it.Note); n != "" {
			l.notes = append(l.notes, n)
		}
	}

	menu, err := a.menuItems(branch.ID)
	if err != nil {
		dbFail(c, err)
		return
	}
	byID := map[uint64]menuItemDTO{}
	for _, m := range menu {
		byID[m.ID] = m
	}
	var total int64
	items := make([]models.TableOrderItem, 0, len(order))
	for _, pid := range order {
		m, ok := byID[pid]
		// A family's parent isn't orderable once it has variants (same rule as the POS).
		if !ok || !m.Available || m.VariantCount > 0 {
			name := fmt.Sprintf("#%d", pid)
			if ok {
				name = m.NameEn
			}
			fail(c, utils.NewAppError(http.StatusUnprocessableEntity, "ITEM_UNAVAILABLE", fmt.Sprintf("%s isn't available right now.", name)))
			return
		}
		l := merged[pid]
		if l.qty > 99 {
			l.qty = 99
		}
		note := strings.Join(l.notes, "; ")
		if len(note) > 255 {
			note = note[:255]
		}
		total += l.qty * m.PriceCents
		items = append(items, models.TableOrderItem{ProductID: pid, NameEn: m.NameEn, NameKm: m.NameKm, Qty: l.qty, UnitPriceCents: m.PriceCents, Note: note})
	}

	publicToken, err := randomToken(16)
	if err != nil {
		dbFail(c, err)
		return
	}
	tableID := table.ID
	o := models.TableOrder{
		BranchID: branch.ID, TableID: &tableID, TableLabel: table.Name, PublicToken: publicToken,
		Status: models.TableOrderNew, Note: strings.TrimSpace(req.Note), TotalCents: total,
	}
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&o).Error; err != nil {
			return err
		}
		for i := range items {
			items[i].TableOrderID = o.ID
		}
		return tx.Create(&items).Error
	})
	if err != nil {
		dbFail(c, err)
		return
	}

	lines := make([]string, len(items))
	for i, it := range items {
		lines[i] = fmt.Sprintf("%d× %s", it.Qty, services.HTMLEscape(it.NameEn))
	}
	a.Notify.Send(services.AlertTableOrder, fmt.Sprintf("🍽 <b>New table order</b>\n%s · %s\n%s\nTotal: %s",
		services.HTMLEscape(table.Name), services.HTMLEscape(branch.Name), services.FormatItemLines(lines, 12), services.FormatUSD(total)))

	out, err := a.publicOrder(o)
	if err != nil {
		dbFail(c, err)
		return
	}
	utils.OK(c, http.StatusCreated, out)
}

// PublicTableOrderStatus is polled by the customer's phone.
func (a *API) PublicTableOrderStatus(c *gin.Context) {
	var o models.TableOrder
	if err := a.DB.Where("public_token = ?", c.Param("token")).First(&o).Error; err != nil {
		fail(c, utils.ErrNotFound)
		return
	}
	out, err := a.publicOrder(o)
	if err != nil {
		dbFail(c, err)
		return
	}
	utils.OK(c, http.StatusOK, out)
}

// ---- Orders (staff) -----------------------------------------------------------

type tableOrderItemDTO struct {
	TableOrderID   uint64 `json:"-"`
	ProductID      uint64 `json:"product_id"`
	NameEn         string `json:"name_en"`
	NameKm         string `json:"name_km"`
	Qty            int64  `json:"qty"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	Note           string `json:"note"`
}

type tableOrderDTO struct {
	ID           uint64              `json:"id"`
	BranchID     uint64              `json:"branch_id"`
	TableID      *uint64             `json:"table_id"`
	TableName    string              `json:"table_name"`
	Status       string              `json:"status"`
	Note         string              `json:"note"`
	TotalCents   int64               `json:"total_cents"`
	SaleID       *uint64             `json:"sale_id"`
	ReceiptNo    string              `json:"receipt_no"`
	HandledBy    string              `json:"handled_by"`
	CancelReason string              `json:"cancel_reason"`
	AcceptedAt   *time.Time          `json:"accepted_at"`
	ClosedAt     *time.Time          `json:"closed_at"`
	CreatedAt    time.Time           `json:"created_at"`
	Items        []tableOrderItemDTO `json:"items" gorm:"-"`
}

func (a *API) tableOrderQuery() *gorm.DB {
	return a.DB.Table("table_orders o").
		Select(`o.id, o.branch_id, o.table_id, o.table_name, o.status, o.note, o.total_cents, o.sale_id,
			COALESCE(s.receipt_no, '') AS receipt_no, COALESCE(u.full_name, '') AS handled_by, o.cancel_reason,
			o.accepted_at, o.closed_at, o.created_at`).
		Joins("LEFT JOIN sales s ON s.id = o.sale_id").
		Joins("LEFT JOIN users u ON u.id = o.handled_by")
}

func (a *API) attachTableOrderItems(orders []tableOrderDTO) error {
	if len(orders) == 0 {
		return nil
	}
	ids := make([]uint64, len(orders))
	idx := map[uint64]int{}
	for i, o := range orders {
		ids[i] = o.ID
		idx[o.ID] = i
		orders[i].Items = []tableOrderItemDTO{}
	}
	var items []tableOrderItemDTO
	if err := a.DB.Model(&models.TableOrderItem{}).
		Select("table_order_id, product_id, name_en, name_km, qty, unit_price_cents, note").
		Where("table_order_id IN ?", ids).Order("id").Scan(&items).Error; err != nil {
		return err
	}
	for _, it := range items {
		orders[idx[it.TableOrderID]].Items = append(orders[idx[it.TableOrderID]].Items, it)
	}
	return nil
}

// ListTableOrders: status=open (default: NEW + ACCEPTED, the POS queue), all,
// or one status; newest first, like the customer's "Your orders"; optional
// date_from / date_to. The meta
// summary carries the branch's new / open counts for the POS badge.
func (a *API) ListTableOrders(c *gin.Context) {
	branchID := branchParam(c)
	pg := pagerOf(c)
	q := a.tableOrderQuery().Where("o.branch_id = ?", branchID)
	order := "o.created_at DESC, o.id DESC"
	switch st := strings.ToUpper(c.DefaultQuery("status", "open")); st {
	case "OPEN":
		q = q.Where("o.status IN ?", []string{string(models.TableOrderNew), string(models.TableOrderAccepted)})
	case "ALL":
	default:
		q = q.Where("o.status = ?", st)
	}
	if from, to := rangeBounds(c); from != nil || to != nil {
		if from != nil {
			q = q.Where("o.created_at >= ?", *from)
		}
		if to != nil {
			q = q.Where("o.created_at < ?", *to)
		}
	}
	var total int64
	if err := q.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		dbFail(c, err)
		return
	}
	rows := []tableOrderDTO{}
	if err := pg.apply(q.Order(order)).Scan(&rows).Error; err != nil {
		dbFail(c, err)
		return
	}
	if err := a.attachTableOrderItems(rows); err != nil {
		dbFail(c, err)
		return
	}
	var counts struct {
		NewOrders  int64
		OpenOrders int64
	}
	a.DB.Table("table_orders").Where("branch_id = ?", branchID).
		Select("COALESCE(SUM(status = 'NEW'), 0) AS new_orders, COALESCE(SUM(status IN ('NEW','ACCEPTED')), 0) AS open_orders").Scan(&counts)
	pg.respond(c, rows, total, gin.H{"new_orders": counts.NewOrders, "open_orders": counts.OpenOrders})
}

func (a *API) loadTableOrder(id uint64) (*tableOrderDTO, error) {
	rows := []tableOrderDTO{}
	if err := a.tableOrderQuery().Where("o.id = ?", id).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	if err := a.attachTableOrderItems(rows); err != nil {
		return nil, err
	}
	return &rows[0], nil
}

// changeTableOrder moves an open order to `to` (ACCEPTED or CANCELLED).
func (a *API) changeTableOrder(c *gin.Context, to models.TableOrderStatus, reason string) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		var o models.TableOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&o, id).Error; err != nil {
			return err
		}
		if !canAccessBranch(c, o.BranchID) {
			return utils.ErrForbidden
		}
		allowed := o.Status == models.TableOrderNew || (to == models.TableOrderCancelled && o.Status == models.TableOrderAccepted)
		if !allowed {
			return utils.NewAppError(http.StatusConflict, "TABLE_ORDER_CLOSED", fmt.Sprintf("This table order is already %s.", strings.ToLower(string(o.Status))))
		}
		now := time.Now()
		userID := middleware.UserIDFrom(c)
		updates := map[string]interface{}{"status": to, "handled_by": userID}
		if to == models.TableOrderAccepted {
			updates["accepted_at"] = now
		} else {
			updates["closed_at"] = now
			updates["cancel_reason"] = reason
		}
		return tx.Model(&o).Updates(updates).Error
	})
	if err != nil {
		if ae, ok := err.(*utils.AppError); ok {
			fail(c, ae)
			return
		}
		dbFail(c, err)
		return
	}
	out, err := a.loadTableOrder(id)
	if err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "pos", "table_order."+strings.ToLower(string(to)), "table_order", id, nil, out)
	utils.OK(c, http.StatusOK, out)
}

func (a *API) AcceptTableOrder(c *gin.Context) {
	a.changeTableOrder(c, models.TableOrderAccepted, "")
}

type cancelTableOrderReq struct {
	Reason string `json:"reason" binding:"max=255"`
}

func (a *API) CancelTableOrder(c *gin.Context) {
	var req cancelTableOrderReq
	if !bind(c, &req) {
		return
	}
	a.changeTableOrder(c, models.TableOrderCancelled, strings.TrimSpace(req.Reason))
}

type moveTableOrdersReq struct {
	OrderIDs  []uint64 `json:"order_ids" binding:"required,min=1,max=100"`
	ToTableID uint64   `json:"to_table_id" binding:"required"`
}

// MoveTableOrders moves open orders (NEW / ACCEPTED) to another table in the
// same branch — the party changed seats, e.g. T1 to T2. The destination must
// be free: taking orders, with no open orders of its own (checked under the
// same transaction, so two staff can't seat two parties on one table). Paid
// and rejected orders never move; they're history for the table they were
// made at.
func (a *API) MoveTableOrders(c *gin.Context) {
	var req moveTableOrdersReq
	if !bind(c, &req) {
		return
	}
	var moved []uint64
	var from []string
	var to models.DiningTable
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		// Locking the destination row serialises moves onto the same table, so
		// the "is it free" check below can't race another till.
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&to, req.ToTableID).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return utils.NewAppError(http.StatusUnprocessableEntity, "TABLE_NOT_FOUND", "That table doesn't exist any more.")
			}
			return err
		}
		if !canAccessBranch(c, to.BranchID) {
			return utils.ErrForbidden
		}
		if !to.Active {
			return utils.NewAppError(http.StatusConflict, "TABLE_NOT_FREE", "That table isn't taking orders.")
		}
		var busy int64
		if err := tx.Model(&models.TableOrder{}).
			Where("table_id = ? AND status IN ? AND id NOT IN ?", to.ID, []string{string(models.TableOrderNew), string(models.TableOrderAccepted)}, req.OrderIDs).
			Count(&busy).Error; err != nil {
			return err
		}
		if busy > 0 {
			return utils.NewAppError(http.StatusConflict, "TABLE_NOT_FREE", "That table isn't free any more.")
		}
		var orders []models.TableOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", req.OrderIDs).Find(&orders).Error; err != nil {
			return err
		}
		if len(orders) != len(req.OrderIDs) {
			return utils.NewAppError(http.StatusUnprocessableEntity, "TABLE_ORDER_NOT_FOUND", "That table order isn't in this branch.")
		}
		seen := map[string]bool{}
		for _, o := range orders {
			if o.BranchID != to.BranchID {
				return utils.NewAppError(http.StatusUnprocessableEntity, "TABLE_OTHER_BRANCH", "Orders can only move to a table in the same branch.")
			}
			if o.Status != models.TableOrderNew && o.Status != models.TableOrderAccepted {
				return utils.NewAppError(http.StatusConflict, "TABLE_ORDER_CLOSED", fmt.Sprintf("This table order is already %s.", strings.ToLower(string(o.Status))))
			}
			if o.TableID != nil && *o.TableID == to.ID {
				continue // already there
			}
			if !seen[o.TableLabel] {
				seen[o.TableLabel] = true
				from = append(from, o.TableLabel)
			}
			moved = append(moved, o.ID)
		}
		if len(moved) == 0 {
			return nil
		}
		toID := to.ID
		return tx.Model(&models.TableOrder{}).Where("id IN ?", moved).
			Updates(map[string]interface{}{"table_id": toID, "table_name": to.Name}).Error
	})
	if err != nil {
		if ae, ok := err.(*utils.AppError); ok {
			fail(c, ae)
			return
		}
		dbFail(c, err)
		return
	}
	if len(moved) > 0 {
		a.audit(c, "pos", "table_order.move", "dining_table", to.ID,
			gin.H{"tables": from, "order_ids": moved}, gin.H{"table": to.Name, "order_ids": moved})
	}
	utils.OK(c, http.StatusOK, gin.H{"moved": len(moved), "to_table_id": to.ID, "to_table_name": to.Name})
}

// closeTableOrderForSale marks a table order PAID by the sale just created,
// inside CreateSale's transaction. The order must still be open and in the
// till's branch; the row is locked so two tills can't both take payment.
func closeTableOrderForSale(tx *gorm.DB, orderID, branchID, saleID, userID uint64) error {
	var o models.TableOrder
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND branch_id = ?", orderID, branchID).First(&o).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return utils.NewAppError(http.StatusUnprocessableEntity, "TABLE_ORDER_NOT_FOUND", "That table order isn't in this branch.")
		}
		return err
	}
	if o.Status != models.TableOrderNew && o.Status != models.TableOrderAccepted {
		return utils.NewAppError(http.StatusConflict, "TABLE_ORDER_CLOSED", fmt.Sprintf("This table order is already %s.", strings.ToLower(string(o.Status))))
	}
	now := time.Now()
	return tx.Model(&o).Updates(map[string]interface{}{
		"status": models.TableOrderPaid, "sale_id": saleID, "closed_at": now, "handled_by": userID,
	}).Error
}
