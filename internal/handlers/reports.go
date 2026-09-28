package handlers

import (
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"com-mart/backend/internal/utils"
)

// Every report counts PAID sales only: a voided sale is reversed out of
// stock, loyalty and the drawer, so it must not appear in revenue either.

type dailyPoint struct {
	Date          string `json:"date"`
	NetSalesCents int64  `json:"net_sales_cents"`
	Transactions  int64  `json:"transactions"`
	CostCents     int64  `json:"cost_cents"`
}

// dailySeries returns one point per calendar day in [from, to) with zeros for
// days that had no sales, so charts get a continuous axis.
func (a *API) dailySeries(branchID uint64, from, to time.Time) ([]dailyPoint, error) {
	var rows []dailyPoint
	err := a.DB.Table("sales").
		Select("DATE_FORMAT(sold_at,'%Y-%m-%d') AS date, SUM(total_cents) AS net_sales_cents, COUNT(*) AS transactions, SUM(cost_total_cents) AS cost_cents").
		Where("branch_id = ? AND status = 'PAID' AND sold_at >= ? AND sold_at < ?", branchID, from, to).
		Group("DATE_FORMAT(sold_at,'%Y-%m-%d')").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	byDate := map[string]dailyPoint{}
	for _, r := range rows {
		byDate[r.Date] = r
	}
	var out []dailyPoint
	for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		p, ok := byDate[key]
		if !ok {
			p = dailyPoint{Date: key}
		}
		out = append(out, p)
	}
	return out, nil
}

// ---- Dashboard ------------------------------------------------------------------

func (a *API) Dashboard(c *gin.Context) {
	branchID := branchParam(c)
	day := today()
	next := day.AddDate(0, 0, 1)
	lastWeek := day.AddDate(0, 0, -7)

	var todayAgg struct {
		Net   int64
		Count int64
	}
	if err := a.DB.Table("sales").Select("COALESCE(SUM(total_cents),0) AS net, COUNT(*) AS count").
		Where("branch_id = ? AND status = 'PAID' AND sold_at >= ? AND sold_at < ?", branchID, day, next).Scan(&todayAgg).Error; err != nil {
		dbFail(c, err)
		return
	}
	var lastWeekCount int64
	a.DB.Table("sales").Where("branch_id = ? AND status = 'PAID' AND sold_at >= ? AND sold_at < ?", branchID, lastWeek, lastWeek.AddDate(0, 0, 1)).Count(&lastWeekCount)

	type lowRow struct {
		ID           uint64 `json:"id"`
		NameEn       string `json:"name_en"`
		NameKm       string `json:"name_km"`
		Qty          int64  `json:"qty"`
		ReorderPoint int64  `json:"reorder_point"`
	}
	var low []lowRow
	a.DB.Table("branch_stock bs").
		Select("p.id, p.name_en, p.name_km, bs.qty, bs.reorder_point").
		Joins("JOIN products p ON p.id = bs.product_id AND p.deleted_at IS NULL AND p.active = 1").
		Where("bs.branch_id = ? AND bs.qty <= bs.reorder_point AND bs.reorder_point > 0", branchID).
		Order("bs.qty / bs.reorder_point ASC, p.id").Limit(10).Scan(&low)
	var lowCount int64
	a.DB.Table("branch_stock bs").Joins("JOIN products p ON p.id = bs.product_id AND p.deleted_at IS NULL AND p.active = 1").
		Where("bs.branch_id = ? AND bs.qty <= bs.reorder_point AND bs.reorder_point > 0", branchID).Count(&lowCount)

	type hourRow struct {
		Hour int   `json:"hour"`
		Qty  int64 `json:"qty"`
	}
	var hourRows []hourRow
	a.DB.Table("sales").Select("HOUR(sold_at) AS hour, COUNT(*) AS qty").
		Where("branch_id = ? AND status = 'PAID' AND sold_at >= ? AND sold_at < ?", branchID, day, next).Group("HOUR(sold_at)").Scan(&hourRows)
	hourly := make([]hourRow, 24)
	for i := range hourly {
		hourly[i].Hour = i
	}
	for _, h := range hourRows {
		if h.Hour >= 0 && h.Hour < 24 {
			hourly[h.Hour].Qty = h.Qty
		}
	}

	type topRow struct {
		ProductID    uint64 `json:"product_id"`
		Name         string `json:"name"`
		NameKm       string `json:"name_km"`
		Qty          int64  `json:"qty"`
		RevenueCents int64  `json:"revenue_cents"`
	}
	var top []topRow
	a.DB.Table("sale_items si").
		Select("si.product_id, MAX(si.name_snapshot) AS name, COALESCE(MAX(p.name_km),'') AS name_km, SUM(si.qty) AS qty, SUM(si.line_total_cents) AS revenue_cents").
		Joins("JOIN sales s ON s.id = si.sale_id AND s.status = 'PAID'").
		Joins("LEFT JOIN products p ON p.id = si.product_id").
		Where("s.branch_id = ? AND s.sold_at >= ? AND s.sold_at < ?", branchID, day, next).
		Group("si.product_id").Order("qty DESC, revenue_cents DESC").Limit(5).Scan(&top)

	type mixRow struct {
		Method      string `json:"method"`
		AmountCents int64  `json:"amount_cents"`
	}
	var mix []mixRow
	a.DB.Table("payments p").Select("p.method, SUM(p.amount_cents) AS amount_cents").
		Joins("JOIN sales s ON s.id = p.sale_id AND s.status = 'PAID'").
		Where("s.branch_id = ? AND s.sold_at >= ? AND s.sold_at < ?", branchID, day, next).Group("p.method").Scan(&mix)

	avg := int64(0)
	if todayAgg.Count > 0 {
		avg = todayAgg.Net / todayAgg.Count
	}
	utils.OK(c, http.StatusOK, gin.H{
		"date":                   day.Format("2006-01-02"),
		"exchange_rate":          a.currentRate(),
		"net_sales_cents":        todayAgg.Net,
		"transactions":           todayAgg.Count,
		"last_week_transactions": lastWeekCount,
		"avg_basket_cents":       avg,
		"low_stock_count":        lowCount,
		"low_stock":              low,
		"hourly":                 hourly,
		"top_products":           top,
		"payment_mix":            mix,
	})
}

// ---- Reports & Staff --------------------------------------------------------------

func (a *API) StaffReport(c *gin.Context) {
	branchID := branchParam(c)
	from, to := dateRange(c)
	series, err := a.dailySeries(branchID, from, to)
	if err != nil {
		dbFail(c, err)
		return
	}
	var total int64
	for _, p := range series {
		total += p.NetSalesCents
	}
	dailyAvg := int64(0)
	if len(series) > 0 {
		dailyAvg = int64(math.Round(float64(total) / float64(len(series))))
	}

	type catRow struct {
		Name       string  `json:"name"`
		NameKm     string  `json:"name_km"`
		SalesCents int64   `json:"sales_cents"`
		Share      float64 `json:"share"`
	}
	var cats []catRow
	a.DB.Table("sale_items si").
		Select("COALESCE(c.name_en,'Uncategorised') AS name, COALESCE(MAX(c.name_km),'') AS name_km, SUM(si.line_total_cents) AS sales_cents").
		Joins("JOIN sales s ON s.id = si.sale_id AND s.status = 'PAID'").
		Joins("LEFT JOIN products p ON p.id = si.product_id").
		Joins("LEFT JOIN categories c ON c.id = p.category_id").
		Where("s.branch_id = ? AND s.sold_at >= ? AND s.sold_at < ?", branchID, from, to).
		Group("COALESCE(c.name_en,'Uncategorised')").Order("sales_cents DESC").Scan(&cats)
	var catTotal int64
	for _, r := range cats {
		catTotal += r.SalesCents
	}
	for i := range cats {
		if catTotal > 0 {
			cats[i].Share = float64(cats[i].SalesCents) / float64(catTotal)
		}
	}

	type staffRow struct {
		ID              uint64 `json:"id"`
		FullName        string `json:"full_name"`
		RoleName        string `json:"role_name"`
		Active          bool   `json:"active"`
		ShiftStatus     string `json:"shift_status"`
		SalesTodayCents int64  `json:"sales_today_cents"`
	}
	var staff []staffRow
	a.DB.Raw(`SELECT u.id, u.full_name, r.name AS role_name, u.active,
			CASE WHEN u.active = 0 THEN 'Off'
			     WHEN EXISTS (SELECT 1 FROM shifts sh WHERE sh.user_id = u.id AND sh.branch_id = ? AND sh.status = 'OPEN') THEN 'Open'
			     ELSE 'Closed' END AS shift_status,
			COALESCE((SELECT SUM(s.total_cents) FROM sales s WHERE s.cashier_id = u.id AND s.branch_id = ? AND s.status = 'PAID' AND s.sold_at >= ? AND s.sold_at < ?),0) AS sales_today_cents
		FROM users u JOIN roles r ON r.id = u.role_id
		WHERE u.deleted_at IS NULL AND r.name IN ('Cashier','Manager')
		  AND EXISTS (SELECT 1 FROM user_branches ub WHERE ub.user_id = u.id AND ub.branch_id = ?)
		ORDER BY u.id`, branchID, branchID, today(), today().AddDate(0, 0, 1), branchID).Scan(&staff)

	roles, err := a.rolesSummary()
	if err != nil {
		dbFail(c, err)
		return
	}
	utils.OK(c, http.StatusOK, gin.H{
		"series": series, "total_net_sales_cents": total, "daily_average_cents": dailyAvg,
		"categories": cats, "staff": staff, "roles": roles,
	})
}

type groupCount struct {
	Group   string `json:"group"`
	Granted int    `json:"granted"`
	Total   int    `json:"total"`
}

type roleSummary struct {
	ID          uint64       `json:"id"`
	Name        string       `json:"name"`
	GroupCounts []groupCount `json:"group_counts"`
}

// rolesSummary is the permission-coverage table on the Reports & Staff page:
// per role, how many of each permission group it holds. Computed here (not
// by the page calling /roles) so staff who can see reports but can't manage
// roles still get it.
func (a *API) rolesSummary() ([]roleSummary, error) {
	roles, err := a.loadRoles(0)
	if err != nil {
		return nil, err
	}
	var perms []struct{ Key, Group string }
	if err := a.DB.Table("permissions").Select("`key`, `group`").Order("id").Scan(&perms).Error; err != nil {
		return nil, err
	}
	var groupOrder []string
	totals := map[string]int{}
	for _, p := range perms {
		if _, ok := totals[p.Group]; !ok {
			groupOrder = append(groupOrder, p.Group)
		}
		totals[p.Group]++
	}
	groupOf := map[string]string{}
	for _, p := range perms {
		groupOf[p.Key] = p.Group
	}
	out := make([]roleSummary, len(roles))
	for i, r := range roles {
		granted := map[string]int{}
		for _, k := range r.Permissions {
			granted[groupOf[k]]++
		}
		rs := roleSummary{ID: r.ID, Name: r.Name}
		for _, g := range groupOrder {
			rs.GroupCounts = append(rs.GroupCounts, groupCount{Group: g, Granted: granted[g], Total: totals[g]})
		}
		out[i] = rs
	}
	return out, nil
}

// ---- Profit & Loss --------------------------------------------------------------------

func (a *API) ProfitLoss(c *gin.Context) {
	branchID := branchParam(c)
	from, to := dateRange(c)

	var agg struct {
		Gross    int64
		Discount int64
		Net      int64
		Cogs     int64
	}
	if err := a.DB.Table("sales").Select(`COALESCE(SUM(subtotal_cents),0) AS gross, COALESCE(SUM(discount_cents),0) AS discount,
			COALESCE(SUM(total_cents),0) AS net, COALESCE(SUM(cost_total_cents),0) AS cogs`).
		Where("branch_id = ? AND status = 'PAID' AND sold_at >= ? AND sold_at < ?", branchID, from, to).Scan(&agg).Error; err != nil {
		dbFail(c, err)
		return
	}
	var returns int64
	a.DB.Table("voids_refunds vr").Select("COALESCE(SUM(vr.amount_cents),0)").
		Joins("JOIN sales s ON s.id = vr.sale_id").
		Where("vr.type = 'REFUND' AND s.branch_id = ? AND vr.created_at >= ? AND vr.created_at < ?", branchID, from, to).Scan(&returns)

	type expRow struct {
		Category    string `json:"category"`
		AmountCents int64  `json:"amount_cents"`
	}
	var exp []expRow
	a.DB.Table("expenses").Select("category, SUM(amount_cents) AS amount_cents").
		Where("branch_id = ? AND deleted_at IS NULL AND expense_date >= ? AND expense_date < ?", branchID, from, to).
		Group("category").Order("amount_cents DESC").Scan(&exp)
	var totalExp int64
	for _, e := range exp {
		totalExp += e.AmountCents
	}

	type profRow struct {
		Name        string  `json:"name"`
		NameKm      string  `json:"name_km"`
		ProfitCents int64   `json:"profit_cents"`
		Share       float64 `json:"share"`
	}
	var byCat []profRow
	a.DB.Table("sale_items si").
		Select("COALESCE(c.name_en,'Uncategorised') AS name, COALESCE(MAX(c.name_km),'') AS name_km, SUM(si.line_total_cents - si.unit_cost_cents * si.qty) AS profit_cents").
		Joins("JOIN sales s ON s.id = si.sale_id AND s.status = 'PAID'").
		Joins("LEFT JOIN products p ON p.id = si.product_id").
		Joins("LEFT JOIN categories c ON c.id = p.category_id").
		Where("s.branch_id = ? AND s.sold_at >= ? AND s.sold_at < ?", branchID, from, to).
		Group("COALESCE(c.name_en,'Uncategorised')").Order("profit_cents DESC").Scan(&byCat)
	var posTotal int64
	for _, r := range byCat {
		if r.ProfitCents > 0 {
			posTotal += r.ProfitCents
		}
	}
	for i := range byCat {
		if posTotal > 0 && byCat[i].ProfitCents > 0 {
			byCat[i].Share = float64(byCat[i].ProfitCents) / float64(posTotal)
		}
	}

	series, err := a.dailySeries(branchID, from, to)
	if err != nil {
		dbFail(c, err)
		return
	}
	var dayExp []struct {
		Date        string
		AmountCents int64
	}
	a.DB.Table("expenses").Select("DATE_FORMAT(expense_date,'%Y-%m-%d') AS date, SUM(amount_cents) AS amount_cents").
		Where("branch_id = ? AND deleted_at IS NULL AND expense_date >= ? AND expense_date < ?", branchID, from, to).
		Group("DATE_FORMAT(expense_date,'%Y-%m-%d')").Scan(&dayExp)
	expByDay := map[string]int64{}
	for _, e := range dayExp {
		expByDay[e.Date] = e.AmountCents
	}
	type netDay struct {
		Date           string `json:"date"`
		NetProfitCents int64  `json:"net_profit_cents"`
	}
	netByDay := make([]netDay, len(series))
	for i, p := range series {
		netByDay[i] = netDay{p.Date, p.NetSalesCents - p.CostCents - expByDay[p.Date]}
	}

	grossProfit := agg.Net - returns - agg.Cogs
	utils.OK(c, http.StatusOK, gin.H{
		"exchange_rate": a.currentRate(), "gross_sales_cents": agg.Gross, "discount_cents": agg.Discount, "returns_cents": returns,
		"net_sales_cents": agg.Net - returns, "cogs_cents": agg.Cogs, "gross_profit_cents": grossProfit,
		"expenses_by_category": exp, "total_expenses_cents": totalExp, "net_profit_cents": grossProfit - totalExp,
		"profit_by_category": byCat, "net_profit_by_day": netByDay,
	})
}

// ---- Report details ----------------------------------------------------------------------

type transactionRow struct {
	ID            uint64    `json:"id"`
	ReceiptNo     string    `json:"receipt_no"`
	SoldAt        time.Time `json:"sold_at"`
	Cashier       string    `json:"cashier"`
	Till          string    `json:"till"`
	Items         int64     `json:"items"`
	PaymentMethod string    `json:"payment_method"`
	TotalCents    int64     `json:"total_cents"`
	CostCents     int64     `json:"cost_cents"`
	Status        string    `json:"status"`
}

func (a *API) TransactionsReport(c *gin.Context) {
	branchID := branchParam(c)
	from, to := dateRange(c)
	p := utils.ParsePageParams(c)

	build := func() *gorm.DB {
		q := a.DB.Table("sales s").
			Joins("LEFT JOIN users u ON u.id = s.cashier_id").
			Joins("LEFT JOIN devices d ON d.id = s.device_id").
			Joins("LEFT JOIN payments pay ON pay.sale_id = s.id").
			Where("s.branch_id = ? AND s.sold_at >= ? AND s.sold_at < ?", branchID, from, to)
		if v := c.Query("cashier_id"); v != "" {
			q = q.Where("s.cashier_id = ?", v)
		}
		if v := c.Query("payment"); v != "" {
			q = q.Where("pay.method = ?", strings.ToUpper(v))
		}
		if v := strings.TrimSpace(c.Query("q")); v != "" {
			q = q.Where("s.receipt_no LIKE ?", "%"+v+"%")
		}
		return q
	}

	var summary struct {
		Count    int64
		Total    int64
		Cost     int64
		Refunded int64
	}
	build().Select(`COUNT(*) AS count, COALESCE(SUM(CASE WHEN s.status='PAID' THEN s.total_cents END),0) AS total,
		COALESCE(SUM(CASE WHEN s.status='PAID' THEN s.cost_total_cents END),0) AS cost,
		COALESCE(SUM(s.status IN ('REFUNDED','PARTIAL_REFUND')),0) AS refunded`).Scan(&summary)
	var paid int64
	build().Where("s.status = 'PAID'").Count(&paid)

	var rows []transactionRow
	err := build().Select(`s.id, s.receipt_no, s.sold_at, COALESCE(u.full_name,'') AS cashier, COALESCE(d.name,'') AS till,
			(SELECT COALESCE(SUM(si.qty),0) FROM sale_items si WHERE si.sale_id = s.id) AS items,
			COALESCE(pay.method,'') AS payment_method, s.total_cents, s.cost_total_cents AS cost_cents, s.status`).
		Order("s.sold_at DESC, s.id DESC").Limit(p.PerPage).Offset(p.Offset()).Scan(&rows).Error
	if err != nil {
		dbFail(c, err)
		return
	}
	var cashiers []struct {
		ID   uint64 `json:"id"`
		Name string `json:"name"`
	}
	a.DB.Table("users u").Select("u.id, u.full_name AS name").
		Where("u.id IN (SELECT DISTINCT cashier_id FROM sales WHERE branch_id = ?)", branchID).Order("u.full_name").Scan(&cashiers)

	avg := int64(0)
	if paid > 0 {
		avg = summary.Total / paid
	}
	utils.OKWithMeta(c, http.StatusOK, gin.H{
		"rows": rows, "cashiers": cashiers,
		"summary": gin.H{"count": summary.Count, "total_cents": summary.Total, "cost_cents": summary.Cost, "avg_basket_cents": avg, "refunded": summary.Refunded},
	}, pageOf(c, summary.Count, p))
}

func (a *API) ByProductReport(c *gin.Context) {
	branchID := branchParam(c)
	pg := pagerOf(c)
	from, to := dateRange(c)
	type row struct {
		SKU          string `json:"sku"`
		Name         string `json:"name"`
		NameKm       string `json:"name_km"`
		Qty          int64  `json:"qty"`
		RevenueCents int64  `json:"revenue_cents"`
		CostCents    int64  `json:"cost_cents"`
		ProfitCents  int64  `json:"profit_cents"`
		MarginPct    int64  `json:"margin_pct"`
	}
	var rows []row
	err := a.DB.Table("sale_items si").
		Select(`COALESCE(p.sku,'') AS sku, MAX(si.name_snapshot) AS name, COALESCE(MAX(p.name_km),'') AS name_km, SUM(si.qty) AS qty, SUM(si.line_total_cents) AS revenue_cents,
			SUM(si.unit_cost_cents * si.qty) AS cost_cents`).
		Joins("JOIN sales s ON s.id = si.sale_id AND s.status = 'PAID'").
		Joins("LEFT JOIN products p ON p.id = si.product_id").
		Where("s.branch_id = ? AND s.sold_at >= ? AND s.sold_at < ?", branchID, from, to).
		Group("si.product_id, p.sku").Order("revenue_cents DESC").Scan(&rows).Error
	if err != nil {
		dbFail(c, err)
		return
	}
	sum := gin.H{"products": len(rows)}
	var units, revenue, profit int64
	for i := range rows {
		rows[i].ProfitCents = rows[i].RevenueCents - rows[i].CostCents
		if rows[i].RevenueCents > 0 {
			rows[i].MarginPct = int64(math.Round(float64(rows[i].ProfitCents) * 100 / float64(rows[i].RevenueCents)))
		}
		units += rows[i].Qty
		revenue += rows[i].RevenueCents
		profit += rows[i].ProfitCents
	}
	sum["units"], sum["revenue_cents"], sum["profit_cents"] = units, revenue, profit
	pg.respond(c, slicePage(pg, rows), int64(len(rows)), sum)
}

func (a *API) ByCashierReport(c *gin.Context) {
	branchID := branchParam(c)
	pg := pagerOf(c)
	from, to := dateRange(c)
	type row struct {
		Name           string `json:"name"`
		Shifts         int64  `json:"shifts"`
		SalesCents     int64  `json:"sales_cents"`
		Transactions   int64  `json:"transactions"`
		AvgBasketCents int64  `json:"avg_basket_cents"`
		Voids          int64  `json:"voids"`
		Refunds        int64  `json:"refunds"`
		CashDiffCents  int64  `json:"cash_diff_cents"`
	}
	var rows []row
	err := a.DB.Raw(`SELECT u.full_name AS name,
			(SELECT COUNT(*) FROM shifts sh WHERE sh.user_id = u.id AND sh.branch_id = ? AND sh.opened_at >= ? AND sh.opened_at < ?) AS shifts,
			COALESCE((SELECT SUM(s.total_cents) FROM sales s WHERE s.cashier_id = u.id AND s.branch_id = ? AND s.status='PAID' AND s.sold_at >= ? AND s.sold_at < ?),0) AS sales_cents,
			(SELECT COUNT(*) FROM sales s WHERE s.cashier_id = u.id AND s.branch_id = ? AND s.status='PAID' AND s.sold_at >= ? AND s.sold_at < ?) AS transactions,
			(SELECT COUNT(*) FROM voids_refunds vr JOIN sales s ON s.id = vr.sale_id WHERE vr.cashier_id = u.id AND vr.type='VOID' AND s.branch_id = ? AND vr.created_at >= ? AND vr.created_at < ?) AS voids,
			(SELECT COUNT(*) FROM voids_refunds vr JOIN sales s ON s.id = vr.sale_id WHERE vr.cashier_id = u.id AND vr.type='REFUND' AND s.branch_id = ? AND vr.created_at >= ? AND vr.created_at < ?) AS refunds,
			COALESCE((SELECT SUM(sh.diff_usd_cents + ROUND(sh.diff_khr_riel * 100 / NULLIF(sh.exchange_rate,0))) FROM shifts sh WHERE sh.user_id = u.id AND sh.branch_id = ? AND sh.status='CLOSED' AND sh.opened_at >= ? AND sh.opened_at < ?),0) AS cash_diff_cents
		FROM users u
		WHERE u.deleted_at IS NULL AND u.id IN (SELECT cashier_id FROM sales WHERE branch_id = ?
			UNION SELECT user_id FROM shifts WHERE branch_id = ?)
		ORDER BY sales_cents DESC`,
		branchID, from, to, branchID, from, to, branchID, from, to, branchID, from, to, branchID, from, to, branchID, from, to, branchID, branchID).Scan(&rows).Error
	if err != nil {
		dbFail(c, err)
		return
	}
	var sales, voids, refunds int64
	for i := range rows {
		if rows[i].Transactions > 0 {
			rows[i].AvgBasketCents = rows[i].SalesCents / rows[i].Transactions
		}
		sales += rows[i].SalesCents
		voids += rows[i].Voids
		refunds += rows[i].Refunds
	}
	pg.respond(c, slicePage(pg, rows), int64(len(rows)), gin.H{"cashiers": len(rows), "sales_cents": sales, "voids": voids, "refunds": refunds})
}

func (a *API) PaymentMethodsReport(c *gin.Context) {
	branchID := branchParam(c)
	from, to := dateRange(c)
	type row struct {
		Method     string  `json:"method"`
		Count      int64   `json:"count"`
		TotalCents int64   `json:"total_cents"`
		Share      float64 `json:"share"`
	}
	var rows []row
	err := a.DB.Table("payments p").Select("p.method, COUNT(*) AS count, SUM(p.amount_cents) AS total_cents").
		Joins("JOIN sales s ON s.id = p.sale_id AND s.status = 'PAID'").
		Where("s.branch_id = ? AND s.sold_at >= ? AND s.sold_at < ?", branchID, from, to).Group("p.method").Order("total_cents DESC").Scan(&rows).Error
	if err != nil {
		dbFail(c, err)
		return
	}
	var total int64
	for _, r := range rows {
		total += r.TotalCents
	}
	for i := range rows {
		if total > 0 {
			rows[i].Share = float64(rows[i].TotalCents) / float64(total)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].TotalCents > rows[j].TotalCents })
	utils.OK(c, http.StatusOK, rows)
}

func (a *API) VoidsRefundsReport(c *gin.Context) {
	branchID := branchParam(c)
	pg := pagerOf(c)
	from, to := dateRange(c)
	type row struct {
		ID          uint64    `json:"id"`
		CreatedAt   time.Time `json:"created_at"`
		ReceiptNo   string    `json:"receipt_no"`
		Type        string    `json:"type"`
		Item        string    `json:"item"`
		AmountCents int64     `json:"amount_cents"`
		Cashier     string    `json:"cashier"`
		ApprovedBy  string    `json:"approved_by"`
		Reason      string    `json:"reason"`
	}
	base := func() *gorm.DB {
		return a.DB.Table("voids_refunds vr").
			Joins("JOIN sales s ON s.id = vr.sale_id").
			Where("s.branch_id = ? AND vr.created_at >= ? AND vr.created_at < ?", branchID, from, to)
	}
	var agg struct {
		Events  int64
		Voids   int64
		Refunds int64
		Amount  int64
	}
	if err := base().Select(`COUNT(*) AS events, COALESCE(SUM(vr.type='VOID'),0) AS voids,
		COALESCE(SUM(vr.type='REFUND'),0) AS refunds, COALESCE(SUM(vr.amount_cents),0) AS amount`).Scan(&agg).Error; err != nil {
		dbFail(c, err)
		return
	}
	var rows []row
	err := pg.apply(base().
		Select(`vr.id, vr.created_at, s.receipt_no, vr.type,
			COALESCE((SELECT si.name_snapshot FROM sale_items si WHERE si.id = vr.sale_item_id), 'Whole sale') AS item,
			vr.amount_cents, COALESCE(cu.full_name,'') AS cashier, COALESCE(au.full_name,'—') AS approved_by, vr.reason`).
		Joins("LEFT JOIN users cu ON cu.id = vr.cashier_id").
		Joins("LEFT JOIN users au ON au.id = vr.approved_by_id").
		Order("vr.created_at DESC, vr.id DESC")).Scan(&rows).Error
	if err != nil {
		dbFail(c, err)
		return
	}
	pg.respond(c, rows, agg.Events, gin.H{"events": agg.Events, "voids": agg.Voids, "refunds": agg.Refunds, "amount_cents": agg.Amount})
}

func (a *API) StockMovementsReport(c *gin.Context) {
	branchID := branchParam(c)
	pg := pagerOf(c)
	from, to := dateRange(c)
	var agg struct {
		Movements   int64
		Received    int64
		Sold        int64
		Adjustments int64
	}
	if err := a.stockMovementBase(branchID, from, to).Select(`COUNT(*) AS movements,
		COALESCE(SUM(CASE WHEN sm.type='RECEIVE' THEN sm.qty_change END),0) AS received,
		COALESCE(-SUM(CASE WHEN sm.type='SALE' THEN sm.qty_change END),0) AS sold,
		COALESCE(SUM(sm.type IN ('COUNT_ADJUST','DAMAGE')),0) AS adjustments`).Scan(&agg).Error; err != nil {
		dbFail(c, err)
		return
	}
	var rows []stockMovementDTO
	if err := pg.apply(a.stockMovementQuery(branchID, from, to)).Scan(&rows).Error; err != nil {
		dbFail(c, err)
		return
	}
	pg.respond(c, rows, agg.Movements, gin.H{"movements": agg.Movements, "received": agg.Received, "sold": agg.Sold, "adjustments": agg.Adjustments})
}

func (a *API) ActivityLogReport(c *gin.Context) {
	pg := pagerOf(c)
	from, to := dateRange(c)
	type row struct {
		ID         uint64    `json:"id"`
		CreatedAt  time.Time `json:"created_at"`
		User       string    `json:"user"`
		Role       string    `json:"role"`
		Module     string    `json:"module"`
		Action     string    `json:"action"`
		EntityType string    `json:"entity_type"`
		EntityID   *uint64   `json:"entity_id"`
		Device     string    `json:"device"`
	}
	base := func() *gorm.DB {
		return a.DB.Table("activity_logs al").Joins("LEFT JOIN users u ON u.id = al.user_id").
			Where("al.created_at >= ? AND al.created_at < ?", from, to)
	}
	var agg struct {
		Events  int64
		Users   int64
		Modules int64
	}
	if err := base().Select("COUNT(*) AS events, COUNT(DISTINCT al.user_id) AS users, COUNT(DISTINCT al.module) AS modules").Scan(&agg).Error; err != nil {
		dbFail(c, err)
		return
	}
	var rows []row
	err := pg.apply(base().
		Select("al.id, al.created_at, COALESCE(u.full_name,'') AS user, al.role, al.module, al.action, al.entity_type, al.entity_id, al.device").
		Order("al.created_at DESC, al.id DESC")).Scan(&rows).Error
	if err != nil {
		dbFail(c, err)
		return
	}
	pg.respond(c, rows, agg.Events, gin.H{"events": agg.Events, "users": agg.Users, "modules": agg.Modules})
}
