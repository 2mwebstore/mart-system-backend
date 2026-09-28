package models

import "time"

// DailySalesSummary is a pre-aggregated rollup, one row per branch per
// business day, refreshed on each sale (and by a nightly job as a safety
// net) so the dashboard and reports don't scan raw sales/sale_items for
// large date ranges. Populated starting Phase 7 (Reports).
type DailySalesSummary struct {
	BranchID          uint64    `gorm:"primaryKey" json:"branch_id"`
	SummaryDate       time.Time `gorm:"primaryKey;type:date" json:"summary_date"`
	TransactionsCount int64     `gorm:"not null;default:0" json:"transactions_count"`
	GrossSalesCents   int64     `gorm:"not null;default:0" json:"gross_sales_cents"`
	DiscountCents     int64     `gorm:"not null;default:0" json:"discount_cents"`
	RefundCents       int64     `gorm:"not null;default:0" json:"refund_cents"`
	NetSalesCents     int64     `gorm:"not null;default:0" json:"net_sales_cents"`
	CostTotalCents    int64     `gorm:"not null;default:0" json:"cost_total_cents"`
	ItemsSoldQty      int64     `gorm:"not null;default:0" json:"items_sold_qty"`
	Timestamps
}

func (DailySalesSummary) TableName() string { return "daily_sales_summary" }
